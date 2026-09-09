package subscriptionnodes

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"exodus/internal/nodes/grpcauth"
	"exodus/internal/proto"
	"exodus/internal/scheduler"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"
)

type subNodeState struct {
	nodeUUID          string
	nodeName          string
	address           string
	port              int
	apiSchema         string
	apiPath           string
	grpcAuthToken     string
	subpageConfigUUID string
	ctx               context.Context
	cancel            context.CancelFunc
	conn              *grpc.ClientConn
	client            proto.NodeServiceClient
	stream            proto.NodeService_StreamNodeDataClient
	streamCancel      context.CancelFunc
	streamGeneration  uint64
	lastResponseAt    time.Time

	isConnected  bool
	isConnecting bool
	lastError    string
	mutex        sync.RWMutex
}

func (sm *SubNodeMonitor) monitorNode(state *subNodeState) {
	reconnectInterval := scheduler.SubNodeReconnectInterval
	attempts := 0

	for {
		if state.ctx.Err() != nil {
			sm.cfg.Logger.Debug("Subscription node monitor stopped", "node", state.nodeName)
			return
		}

		connected := sm.connectAndStream(state)
		if state.ctx.Err() != nil {
			sm.cfg.Logger.Debug("Subscription node monitor stopped", "node", state.nodeName)
			return
		}

		if connected {
			attempts = 0
		}

		attempts++

		sm.cfg.Logger.Warn(
			"Subscription node disconnected, scheduling reconnect",
			"node", state.nodeName,
			"address", state.address,
			"port", state.port,
			"attempt", attempts,
			"wait", reconnectInterval.String(),
		)

		timer := time.NewTimer(reconnectInterval)
		select {
		case <-state.ctx.Done():
			timer.Stop()
			sm.cfg.Logger.Debug("Subscription node monitor stopped", "node", state.nodeName)
			return
		case <-timer.C:
		}
	}
}

func (sm *SubNodeMonitor) connectAndStream(state *subNodeState) bool {
	state.mutex.Lock()
	if state.isConnecting {
		state.mutex.Unlock()
		return false
	}
	state.isConnecting = true
	state.mutex.Unlock()

	urlTarget := fmt.Sprintf("%s:%d", state.address, state.port)

	apiSchema := strings.ToLower(strings.TrimSpace(state.apiSchema))
	var useMTLS, useTLS, skipVerify bool
	switch apiSchema {
	case "tls":
		if strings.TrimSpace(state.grpcAuthToken) == "" {
			sm.cfg.Logger.Warn("Failed to connect to subscription node: missing global gRPC token", "node", state.nodeName)
			sm.updateConnectionStatus(state.nodeName, false, false, "Missing gRPC token in keygen")
			state.mutex.Lock()
			state.isConnecting = false
			state.mutex.Unlock()
			return false
		}
		useTLS = true
		if sm.cfg != nil && sm.cfg.Backend.AllowInsecureHTTP {
			skipVerify = true
			sm.cfg.Logger.Warn("Subscription TLS verification is disabled by EXODUS_ALLOW_INSECURE_HTTP", "node", state.nodeName)
		}
	default:
		useMTLS = true
	}

	cleanPath := normalizeSubPath(state.apiPath)
	opts, err := grpcauth.GetDialOptions(state.ctx, sm.db, useMTLS, useTLS, skipVerify, cleanPath, state.grpcAuthToken, "")
	if err != nil {
		sm.cfg.Logger.Warn("Failed to build mTLS config for subscription node", "node", state.nodeName, "error", err)
		sm.updateConnectionStatus(state.nodeName, false, false, fmt.Sprintf("mTLS config failed: %v", err))
		state.mutex.Lock()
		state.isConnecting = false
		state.mutex.Unlock()
		return false
	}

	conn, err := grpc.NewClient(urlTarget, opts...)
	if err != nil {
		sm.cfg.Logger.Warn("Failed to connect to subscription node", "node", state.nodeName, "address", urlTarget, "error", err)
		sm.updateConnectionStatus(state.nodeName, false, false, fmt.Sprintf("Connection failed: %v", err))
		state.mutex.Lock()
		state.isConnecting = false
		state.mutex.Unlock()
		return false
	}

	streamCtx, streamCancel := context.WithCancel(state.ctx)

	client := proto.NewNodeServiceClient(conn)
	stream, err := client.StreamNodeData(streamCtx)
	if err != nil {
		streamCancel()
		sm.cfg.Logger.Warn("Failed to create subscription stream", "node", state.nodeName, "error", err)
		_ = conn.Close()
		sm.updateConnectionStatus(state.nodeName, false, false, fmt.Sprintf("Stream failed: %v", err))
		state.mutex.Lock()
		state.isConnecting = false
		state.mutex.Unlock()
		return false
	}

	state.mutex.Lock()
	state.conn = conn
	state.client = client
	state.stream = stream
	state.streamCancel = streamCancel
	state.streamGeneration++
	generation := state.streamGeneration
	state.lastResponseAt = time.Now()
	state.isConnected = true
	state.isConnecting = false
	state.lastError = ""
	state.mutex.Unlock()

	if err := sm.sendNodeRequest(state, &proto.NodeDataRequest{
		Request: &proto.NodeDataRequest_Config{
			Config: &proto.StreamConfig{IntervalSeconds: int32(subNodeStreamInterval / time.Second)},
		},
	}); err != nil {
		sm.cfg.Logger.Warn("Failed to send subscription stream config", "node", state.nodeName, "error", err)
		sm.handleDisconnect(state, fmt.Sprintf("Config failed: %v", err))
		return false
	}

	go sm.watchStreamHeartbeat(state, generation, subNodeStreamIdleTimeout, subNodeStreamWatchInterval)

	sm.pushAssignedSubpageConfig(state)

	sm.updateConnectionStatus(state.nodeName, true, false, "Connected")
	sm.cfg.Logger.Debug("Subscription node connected", "node", state.nodeName, "address", state.address, "port", state.port)

	sm.receiveStream(state)
	return true
}

func (sm *SubNodeMonitor) watchStreamHeartbeat(
	state *subNodeState,
	generation uint64,
	idleTimeout time.Duration,
	watchInterval time.Duration,
) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-state.ctx.Done():
			return
		case <-ticker.C:
			state.mutex.RLock()
			currentGeneration := state.streamGeneration
			lastResponse := state.lastResponseAt
			isConnected := state.isConnected
			state.mutex.RUnlock()

			if currentGeneration != generation || !isConnected {
				return
			}

			if !lastResponse.IsZero() && time.Since(lastResponse) > idleTimeout {
				if sm != nil && sm.cfg != nil {
					sm.cfg.Logger.Warn(
						"Subscription stream idle timeout exceeded, restarting stream",
						"node", state.nodeName,
						"idle_for", time.Since(lastResponse).String(),
						"timeout", idleTimeout.String(),
					)
				}
				sm.handleDisconnect(state, "Stream idle timeout")
				return
			}
		}
	}
}

func (sm *SubNodeMonitor) sendNodeRequest(state *subNodeState, req *proto.NodeDataRequest) error {
	state.mutex.RLock()
	stream := state.stream
	state.mutex.RUnlock()

	if stream == nil {
		return fmt.Errorf("stream is closed")
	}

	return stream.Send(req)
}

func (sm *SubNodeMonitor) markStreamActivity(state *subNodeState) {
	state.mutex.Lock()
	state.lastResponseAt = time.Now()
	state.mutex.Unlock()
}

func normalizeSubPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "/" {
		return ""
	}
	return "/" + strings.Trim(trimmed, "/")
}

func normalizeSubSchema(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "tls":
		return "tls"
	case "mtls":
		return "mtls"
	default:
		return "mtls"
	}
}

func normalizeAssignedSubpageConfigUUID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return trimmed
}
