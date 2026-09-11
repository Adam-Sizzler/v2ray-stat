package grpcapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/exodus/subscription-page/backend/internal/logger"
	"github.com/exodus/subscription-page/backend/internal/proto"
	"github.com/exodus/subscription-page/backend/internal/srslists"

	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

type NodeServer struct {
	proto.UnimplementedNodeServiceServer

	startedAt time.Time
	version   string
	cpuCount  int
	cpuModel  string
	totalRAM  string

	streamMu      sync.RWMutex
	stream        proto.NodeService_StreamNodeDataServer
	streamStateCh chan struct{}
	streamSendMu  sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan *proto.SubscriptionBridgeResponse
	reqSeq    atomic.Uint64

	subpageConfigsMu sync.RWMutex
	subpageConfigs   map[string][]byte
}

const (
	subNodeStatVersion  = "sub_node_version"
	subNodeStatUptime   = "sub_node_uptime"
	subNodeStatCPUCount = "cpu_count"
	subNodeStatCPUModel = "cpu_model"
	subNodeStatTotalRAM = "total_ram"
)

var panelStreamWaitTimeout = 5 * time.Second

func NewNodeServer(version string) *NodeServer {
	trimmedVersion := strings.TrimSpace(version)
	if trimmedVersion == "" {
		trimmedVersion = "sub"
	}

	return &NodeServer{
		startedAt:      time.Now().UTC(),
		version:        trimmedVersion,
		cpuCount:       runtime.NumCPU(),
		cpuModel:       detectCPUModel(),
		totalRAM:       detectTotalRAM(),
		streamStateCh:  make(chan struct{}),
		pending:        make(map[string]chan *proto.SubscriptionBridgeResponse),
		subpageConfigs: make(map[string][]byte),
	}
}

func (s *NodeServer) StreamNodeData(stream proto.NodeService_StreamNodeDataServer) error {
	log := logger.WithContext("GrpcServer")
	remoteAddr := streamPeerAddress(stream.Context())
	log.Info("Panel stream connected successfully", logger.String("remoteAddr", remoteAddr))

	s.attachStream(stream)
	defer func() {
		s.detachStream(stream)
		log.Warn("Panel stream disconnected", logger.String("remoteAddr", remoteAddr))
	}()

	intervalSeconds := 15
	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	intervalUpdates := make(chan int, 1)
	requestUsers := make(chan struct{}, 1)
	recvErrCh := make(chan error, 1)

	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				recvErrCh <- err
				return
			}
			s.handleStreamRequest(req, intervalUpdates, requestUsers)
		}
	}()

	sendStats := func() error {
		return s.sendResponse(stream, &proto.NodeDataResponse{
			Response: &proto.NodeDataResponse_Stats{
				Stats: &proto.GetApiStatsResponse{
					Stats: s.buildStats(),
				},
			},
		})
	}

	sendUsers := func() error {
		return s.sendResponse(stream, &proto.NodeDataResponse{
			Response: &proto.NodeDataResponse_Users{
				Users: &proto.ListUsersResponse{Users: []*proto.User{}},
			},
		})
	}

	if err := sendStats(); err != nil {
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case recvErr := <-recvErrCh:
			if recvErr == io.EOF {
				log.Info("Panel stream closed by panel", logger.String("remoteAddr", remoteAddr))
				return nil
			}
			log.Warn("Panel stream receive failed", logger.String("remoteAddr", remoteAddr), logger.String("error", recvErr.Error()))
			return recvErr
		case nextInterval := <-intervalUpdates:
			nextInterval = clampInterval(nextInterval)
			if nextInterval == intervalSeconds {
				continue
			}
			intervalSeconds = nextInterval
			ticker.Reset(time.Duration(intervalSeconds) * time.Second)
			log.Info("Panel stream interval updated", logger.Int("intervalSeconds", intervalSeconds))
		case <-requestUsers:
			log.Debug("Panel requested users list")
			if err := sendUsers(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := sendStats(); err != nil {
				return err
			}
		}
	}
}

func (s *NodeServer) QueryPanel(ctx context.Context, req *proto.SubscriptionBridgeRequest) (*proto.SubscriptionBridgeResponse, error) {
	log := logger.WithContext("PanelBridge")
	if req == nil {
		return nil, fmt.Errorf("subscription bridge request is nil")
	}
	if strings.TrimSpace(req.Operation) == "" {
		return nil, fmt.Errorf("subscription bridge operation is required")
	}

	if strings.TrimSpace(req.RequestId) == "" {
		req.RequestId = s.nextRequestID()
	}
	operation := strings.TrimSpace(req.Operation)
	log.Debug("Sending request to panel", logger.String("operation", operation), logger.String("requestId", req.RequestId))

	waitCtx, cancel := context.WithTimeout(ctx, panelStreamWaitTimeout)
	defer cancel()

	for {
		stream, err := s.waitForStream(waitCtx)
		if err != nil {
			log.Error("Panel stream is not connected", err, logger.String("operation", operation), logger.String("requestId", req.RequestId))
			return nil, err
		}

		responseCh := make(chan *proto.SubscriptionBridgeResponse, 1)
		s.pendingMu.Lock()
		s.pending[req.RequestId] = responseCh
		s.pendingMu.Unlock()

		cleanup := func() {
			s.pendingMu.Lock()
			delete(s.pending, req.RequestId)
			s.pendingMu.Unlock()
		}

		if err := s.sendResponse(stream, &proto.NodeDataResponse{
			Response: &proto.NodeDataResponse_SubscriptionRequest{SubscriptionRequest: req},
		}); err != nil {
			cleanup()
			if waitCtx.Err() != nil {
				return nil, fmt.Errorf("panel stream is not connected")
			}
			if err.Error() == "panel stream is not active" {
				continue
			}
			return nil, err
		}

		select {
		case <-waitCtx.Done():
			cleanup()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("panel stream is not connected")
		case <-stream.Context().Done():
			cleanup()
			continue
		case resp := <-responseCh:
			if resp == nil {
				return nil, fmt.Errorf("empty bridge response")
			}
			log.Debug("Received response from panel", logger.String("operation", operation), logger.String("requestId", req.RequestId), logger.Int("statusCode", int(resp.GetStatusCode())))
			return resp, nil
		}
	}
}

func (s *NodeServer) GetCachedSubpageConfig(uuid string) ([]byte, bool) {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil, false
	}

	s.subpageConfigsMu.RLock()
	config, ok := s.subpageConfigs[uuid]
	s.subpageConfigsMu.RUnlock()
	if !ok {
		return nil, false
	}

	clone := make([]byte, len(config))
	copy(clone, config)
	return clone, true
}

func (s *NodeServer) ListUsers(context.Context, *proto.ListUsersRequest) (*proto.ListUsersResponse, error) {
	return &proto.ListUsersResponse{Users: []*proto.User{}}, nil
}

func (s *NodeServer) GetApiStats(context.Context, *proto.GetApiStatsRequest) (*proto.GetApiStatsResponse, error) {
	return &proto.GetApiStatsResponse{Stats: s.buildStats()}, nil
}

func (s *NodeServer) GetLogData(context.Context, *proto.GetLogDataRequest) (*proto.GetLogDataResponse, error) {
	return &proto.GetLogDataResponse{UserLogData: map[string]*proto.UserLogData{}}, nil
}

func (s *NodeServer) AddUsers(context.Context, *proto.AddUsersRequest) (*proto.OperationResponse, error) {
	return &proto.OperationResponse{Status: okStatus("users accepted")}, nil
}

func (s *NodeServer) DeleteUsers(context.Context, *proto.DeleteUsersRequest) (*proto.OperationResponse, error) {
	return &proto.OperationResponse{Status: okStatus("users removed")}, nil
}

func (s *NodeServer) SetUserEnabled(context.Context, *proto.SetUserEnabledRequest) (*proto.OperationResponse, error) {
	return &proto.OperationResponse{Status: okStatus("user status updated")}, nil
}

func (s *NodeServer) SubmitTask(ctx context.Context, task *proto.NodeTask) (*statuspb.Status, error) {
	if task == nil {
		return &statuspb.Status{Code: int32(codes.InvalidArgument), Message: "task is nil"}, nil
	}

	operation := strings.TrimSpace(task.Operation)
	switch operation {
	case "sync_srs_lists":
		var payload struct {
			SRSLists []srslists.ListItem `json:"srs_lists"`
		}
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			logger.WithContext("SRSService").Warn("Invalid sync_srs_lists payload", logger.String("task_id", task.TaskId), logger.String("error", err.Error()))
			return &statuspb.Status{
				Code:    int32(codes.InvalidArgument),
				Message: fmt.Sprintf("invalid sync_srs_lists payload: %v", err),
			}, nil
		}

		summary, err := srslists.SyncLists(ctx, payload.SRSLists)
		if err != nil {
			return &statuspb.Status{Code: int32(codes.FailedPrecondition), Message: err.Error()}, nil
		}
		return &statuspb.Status{
			Code: int32(codes.OK),
			Message: fmt.Sprintf(
				"success: total=%d configured=%d downloaded=%d failed=%d",
				summary.Total,
				summary.Configured,
				summary.Downloaded,
				summary.Failed,
			),
		}, nil
	case "":
		return okStatus("task accepted"), nil
	default:
		return okStatus(fmt.Sprintf("task accepted: %s", operation)), nil
	}
}

func (s *NodeServer) GetTaskStatus(context.Context, *proto.TaskStatusRequest) (*proto.TaskStatusResponse, error) {
	return &proto.TaskStatusResponse{Status: "success"}, nil
}

func (s *NodeServer) handleStreamRequest(req *proto.NodeDataRequest, intervalUpdates chan<- int, requestUsers chan<- struct{}) {
	if req == nil {
		return
	}

	switch payload := req.GetRequest().(type) {
	case *proto.NodeDataRequest_Config:
		if payload.Config == nil {
			return
		}
		if payload.Config.IntervalSeconds > 0 {
			select {
			case intervalUpdates <- int(payload.Config.IntervalSeconds):
			default:
			}
		}
	case *proto.NodeDataRequest_ListUsers:
		select {
		case requestUsers <- struct{}{}:
		default:
		}
	case *proto.NodeDataRequest_SubscriptionResponse:
		s.handleBridgeResponse(payload.SubscriptionResponse)
	case *proto.NodeDataRequest_SubpageConfigUpdate:
		s.applySubpageConfigUpdate(payload.SubpageConfigUpdate)
	}
}

func (s *NodeServer) handleBridgeResponse(resp *proto.SubscriptionBridgeResponse) {
	if resp == nil {
		return
	}
	requestID := strings.TrimSpace(resp.GetRequestId())
	if requestID == "" {
		return
	}

	s.pendingMu.Lock()
	responseCh, ok := s.pending[requestID]
	if ok {
		delete(s.pending, requestID)
	}
	s.pendingMu.Unlock()
	if !ok {
		return
	}

	select {
	case responseCh <- resp:
	default:
	}
}

func (s *NodeServer) applySubpageConfigUpdate(update *proto.SubpageConfigUpdate) {
	log := logger.WithContext("SubpageConfigService")
	if update == nil {
		return
	}
	uuid := strings.TrimSpace(update.GetUuid())
	if uuid == "" {
		s.subpageConfigsMu.Lock()
		s.subpageConfigs = make(map[string][]byte)
		s.subpageConfigsMu.Unlock()
		log.Warn("Subscription page config list cannot be fetched")
		return
	}

	payload := update.GetConfig()
	clone := make([]byte, len(payload))
	copy(clone, payload)

	s.subpageConfigsMu.Lock()
	s.subpageConfigs[uuid] = clone
	count := len(s.subpageConfigs)
	s.subpageConfigsMu.Unlock()

	log.Info("Subscription page config updated from panel", logger.String("uuid", uuid))
	log.Debug("Subpage config cache updated", logger.String("uuid", uuid), logger.Int("bytes", len(clone)), logger.Int("total", count))
}

func (s *NodeServer) buildStats() []*proto.Stat {
	uptimeSeconds := int64(time.Since(s.startedAt).Seconds())
	if uptimeSeconds < 0 {
		uptimeSeconds = 0
	}

	return []*proto.Stat{
		{Name: subNodeStatVersion, Value: s.version},
		{Name: subNodeStatUptime, Value: strconv.FormatInt(uptimeSeconds, 10)},
		{Name: subNodeStatCPUCount, Value: strconv.Itoa(s.cpuCount)},
		{Name: subNodeStatCPUModel, Value: s.cpuModel},
		{Name: subNodeStatTotalRAM, Value: s.totalRAM},
	}
}

func (s *NodeServer) currentStream() proto.NodeService_StreamNodeDataServer {
	s.streamMu.RLock()
	stream := s.stream
	s.streamMu.RUnlock()
	return stream
}

func (s *NodeServer) waitForStream(ctx context.Context) (proto.NodeService_StreamNodeDataServer, error) {
	for {
		stream, changed := s.streamSnapshot()
		if stream != nil {
			return stream, nil
		}

		select {
		case <-ctx.Done():
			if ctx.Err() != nil && ctx.Err() == context.Canceled {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("panel stream is not connected")
		case <-changed:
		}
	}
}

func (s *NodeServer) streamSnapshot() (proto.NodeService_StreamNodeDataServer, <-chan struct{}) {
	s.streamMu.RLock()
	stream := s.stream
	changed := s.streamStateCh
	s.streamMu.RUnlock()
	return stream, changed
}

func (s *NodeServer) attachStream(stream proto.NodeService_StreamNodeDataServer) {
	s.streamMu.Lock()
	s.stream = stream
	s.notifyStreamStateChangeLocked()
	s.streamMu.Unlock()

	s.failPending("panel stream replaced")
}

func (s *NodeServer) detachStream(stream proto.NodeService_StreamNodeDataServer) {
	s.streamMu.Lock()
	if s.stream == stream {
		s.stream = nil
		s.notifyStreamStateChangeLocked()
	}
	s.streamMu.Unlock()

	s.failPending("panel stream disconnected")
}

func (s *NodeServer) notifyStreamStateChangeLocked() {
	close(s.streamStateCh)
	s.streamStateCh = make(chan struct{})
}

func (s *NodeServer) sendResponse(stream proto.NodeService_StreamNodeDataServer, resp *proto.NodeDataResponse) error {
	s.streamSendMu.Lock()
	defer s.streamSendMu.Unlock()

	current := s.currentStream()
	if current == nil || current != stream {
		return fmt.Errorf("panel stream is not active")
	}

	return stream.Send(resp)
}

func (s *NodeServer) failPending(reason string) {
	s.pendingMu.Lock()
	pending := s.pending
	s.pending = make(map[string]chan *proto.SubscriptionBridgeResponse)
	s.pendingMu.Unlock()

	for requestID, responseCh := range pending {
		resp := &proto.SubscriptionBridgeResponse{
			RequestId:  requestID,
			StatusCode: 503,
			Error:      reason,
		}
		select {
		case responseCh <- resp:
		default:
		}
	}
}

func (s *NodeServer) nextRequestID() string {
	next := s.reqSeq.Add(1)
	return "sub-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(next, 10)
}

func okStatus(message string) *statuspb.Status {
	return &statuspb.Status{Code: int32(codes.OK), Message: message}
}

func clampInterval(seconds int) int {
	if seconds < 5 {
		return 5
	}
	if seconds > 300 {
		return 300
	}
	return seconds
}

func streamPeerAddress(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range []string{"cf-connecting-ip", "x-real-ip", "true-client-ip", "x-client-ip"} {
			if vals := md.Get(key); len(vals) > 0 {
				val := strings.TrimSpace(vals[0])
				if val != "" {
					return val
				}
			}
		}
		if vals := md.Get("x-forwarded-for"); len(vals) > 0 {
			raw := strings.TrimSpace(vals[0])
			if raw != "" {
				parts := strings.Split(raw, ",")
				for _, p := range parts {
					cleaned := strings.TrimSpace(p)
					if cleaned != "" {
						return cleaned
					}
				}
			}
		}
	}

	peerInfo, ok := peer.FromContext(ctx)
	if !ok || peerInfo.Addr == nil {
		return "unknown"
	}
	addrStr := peerInfo.Addr.String()
	if host, _, err := net.SplitHostPort(addrStr); err == nil && host != "" {
		return host
	}
	return addrStr
}

func detectCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			value := strings.TrimSpace(parts[1])
			if value != "" {
				return value
			}
		}
	}
	return "unknown"
}

func detectTotalRAM() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil || kb <= 0 {
			continue
		}
		return formatIECBytes(kb * 1024)
	}
	return ""
}

func formatIECBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(bytes)
	unitIdx := 0
	for value >= 1024 && unitIdx < len(units)-1 {
		value /= 1024
		unitIdx++
	}
	if unitIdx == 0 {
		return fmt.Sprintf("%d %s", bytes, units[unitIdx])
	}
	return fmt.Sprintf("%.2f %s", value, units[unitIdx])
}
