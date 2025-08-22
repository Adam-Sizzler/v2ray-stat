package users

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Task represents a task for the worker pool, containing node name and response data.
type Task struct {
	NodeName string
	Response *proto.NodeDataResponse
}

// processTask processes a single NodeDataResponse task.
func processTask(ctx context.Context, manager *manager.DatabaseManager, nodeName string, resp *proto.NodeDataResponse, store *IPStore, cfg *config.Config, nodeClients []*db.NodeClient) error {
	switch resp.Response.(type) {
	case *proto.NodeDataResponse_Stats:
		apiData := convertProtoToApiResponse(resp.GetStats())
		if err := updateProxyStats(manager, nodeName, apiData, cfg); err != nil {
			return fmt.Errorf("update proxy stats for %s: %w", nodeName, err)
		}
		if err := updateUserStats(manager, nodeName, apiData, cfg); err != nil {
			return fmt.Errorf("update user stats for %s: %w", nodeName, err)
		}
		cfg.Logger.Debug("Processed stats response", "node_name", nodeName)

	case *proto.NodeDataResponse_Users:
		// Найти правильный nodeClient по nodeName
		var nodeClient *db.NodeClient
		for _, nc := range nodeClients {
			if nc.NodeName == nodeName {
				nodeClient = nc
				break
			}
		}
		if nodeClient == nil {
			cfg.Logger.Error("No node client found for node", "node_name", nodeName)
			return fmt.Errorf("no node client found for node %s", nodeName)
		}
		if err := SyncUsersWithNode(ctx, manager, []*db.NodeClient{nodeClient}, cfg); err != nil {
			return fmt.Errorf("sync users for %s: %w", nodeName, err)
		}
		cfg.Logger.Debug("Processed users response", "node_name", nodeName)

	case *proto.NodeDataResponse_LogData:
		logData := resp.GetLogData()
		dnsStats := make(map[string]map[string]int)
		for user, data := range logData.UserLogData {
			store.AddIPs(user, data.ValidIps)
			dnsStats[user] = make(map[string]int)
			for domain, count := range data.DnsStats {
				dnsStats[user][domain] = int(count)
			}
		}
		if len(dnsStats) > 0 {
			if err := UpsertDNSRecordsBatch(manager, dnsStats, nodeName, cfg); err != nil {
				return fmt.Errorf("update user_dns for %s: %w", nodeName, err)
			}
		} else {
			cfg.Logger.Debug("No DNS records to update from node", "node_name", nodeName)
		}
		ipUpdates := store.CollectAndCleanup(66 * time.Second)
		if len(ipUpdates) > 0 {
			if err := UpdateIPsBatch(manager, ipUpdates, cfg); err != nil {
				return fmt.Errorf("update IPs for %s: %w", nodeName, err)
			}
		}
		cfg.Logger.Debug("Processed log data response", "node_name", nodeName)
	}
	return nil
}

// MonitorNodeData manages streaming for traffic stats, users, and log data using a worker pool.
func MonitorNodeData(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config, wg *sync.WaitGroup) {
	defer wg.Done() // сигналим о завершении

	go func() {
		// Load initial user inactivity status
		if err := LoadIsInactiveFromLastSeen(manager, cfg); err != nil {
			cfg.Logger.Error("Failed to load initial user inactivity status", "error", err)
		}

		// Create IPStore for log data
		store := NewIPStore()

		// Create a channel for tasks to be processed by workers
		taskCh := make(chan Task, len(nodeClients)*2) // Buffered channel based on node count
		var workerWG sync.WaitGroup

		// Start worker pool (number of workers based on CPU count or node count)
		numWorkers := max(4, min(len(nodeClients)/10, runtime.NumCPU()*2))
		for i := 0; i < numWorkers; i++ {
			workerWG.Add(1)
			go func(workerID int) {
				defer workerWG.Done()
				for {
					select {
					case task, ok := <-taskCh:
						if !ok {
							cfg.Logger.Debug("Worker stopped", "worker_id", workerID)
							return
						}
						// Process the task, passing nodeClients
						if err := processTask(ctx, manager, task.NodeName, task.Response, store, cfg, nodeClients); err != nil {
							cfg.Logger.Error("Failed to process task", "node_name", task.NodeName, "error", err)
						}
					case <-ctx.Done():
						cfg.Logger.Debug("Worker stopped due to context cancellation", "worker_id", workerID)
						return
					}
				}
			}(i)
		}

		// Start a goroutine for each node to handle streaming
		var nodeWG sync.WaitGroup
		nodeWG.Add(len(nodeClients))

		// Track connection state for each node
		type nodeState struct {
			isConnected bool
			stream      proto.NodeService_StreamNodeDataClient
			mutex       sync.Mutex
		}
		nodeStates := make(map[string]*nodeState)
		for _, nc := range nodeClients {
			nodeStates[nc.NodeName] = &nodeState{isConnected: false}
		}

		for _, nc := range nodeClients {
			go func(nc *db.NodeClient) {
				defer nodeWG.Done()

				// Ticker for periodic connection checks (every 30 seconds)
				connTicker := time.NewTicker(30 * time.Second)
				defer connTicker.Stop()

				// User sync ticker (every 60 minutes)
				userTicker := time.NewTicker(60 * time.Minute)
				defer userTicker.Stop()

				for {
					select {
					case <-ctx.Done():
						cfg.Logger.Debug("Node monitoring stopped", "node_name", nc.NodeName)
						nodeStates[nc.NodeName].mutex.Lock()
						if nodeStates[nc.NodeName].stream != nil {
							nodeStates[nc.NodeName].stream.CloseSend()
						}
						nodeStates[nc.NodeName].isConnected = false
						nodeStates[nc.NodeName].mutex.Unlock()
						return

					case <-connTicker.C:
						nodeStates[nc.NodeName].mutex.Lock()
						if nodeStates[nc.NodeName].isConnected {
							// Connection already active, skip
							cfg.Logger.Debug("Connection already active", "node_name", nc.NodeName)
							nodeStates[nc.NodeName].mutex.Unlock()
							continue
						}
						nodeStates[nc.NodeName].mutex.Unlock()

						// Attempt to establish a new connection
						cfg.Logger.Debug("Attempting to establish connection", "node_name", nc.NodeName)
						stream, err := nc.Client.StreamNodeData(ctx)
						if err != nil {
							cfg.Logger.Error("Failed to open stream", "node_name", nc.NodeName, "error", err)
							continue
						}

						nodeStates[nc.NodeName].mutex.Lock()
						nodeStates[nc.NodeName].stream = stream
						nodeStates[nc.NodeName].isConnected = true
						nodeStates[nc.NodeName].mutex.Unlock()

						// Send initial config with TickerInterval
						if err := stream.Send(&proto.NodeDataRequest{
							Request: &proto.NodeDataRequest_Config{
								Config: &proto.StreamConfig{
									IntervalSeconds: int32(cfg.V2rayStat.Monitor.TickerInterval),
								},
							},
						}); err != nil {
							cfg.Logger.Error("Failed to send initial config", "node_name", nc.NodeName, "error", err)
							nodeStates[nc.NodeName].mutex.Lock()
							nodeStates[nc.NodeName].isConnected = false
							nodeStates[nc.NodeName].stream.CloseSend()
							nodeStates[nc.NodeName].stream = nil
							nodeStates[nc.NodeName].mutex.Unlock()
							continue
						}
						cfg.Logger.Info("Sent initial config", "node_name", nc.NodeName, "interval_seconds", cfg.V2rayStat.Monitor.TickerInterval)

						// Send initial ListUsers request
						if err := stream.Send(&proto.NodeDataRequest{
							Request: &proto.NodeDataRequest_ListUsers{
								ListUsers: &proto.ListUsersRequest{},
							},
						}); err != nil {
							cfg.Logger.Error("Failed to send initial list users request", "node_name", nc.NodeName, "error", err)
							nodeStates[nc.NodeName].mutex.Lock()
							nodeStates[nc.NodeName].isConnected = false
							nodeStates[nc.NodeName].stream.CloseSend()
							nodeStates[nc.NodeName].stream = nil
							nodeStates[nc.NodeName].mutex.Unlock()
							continue
						}
						cfg.Logger.Debug("Sent initial list users request", "node_name", nc.NodeName)

						// Start a goroutine to handle stream responses
						go func() {
							for {
								resp, err := stream.Recv()
								if err == io.EOF {
									cfg.Logger.Info("Stream closed by node", "node_name", nc.NodeName)
									nodeStates[nc.NodeName].mutex.Lock()
									nodeStates[nc.NodeName].isConnected = false
									nodeStates[nc.NodeName].stream = nil
									nodeStates[nc.NodeName].mutex.Unlock()
									return
								}
								if err != nil {
									if status.Code(err) == codes.Canceled && ctx.Err() != nil {
										cfg.Logger.Debug("Stream closed due to context cancellation", "node_name", nc.NodeName)
									} else {
										cfg.Logger.Error("Stream error", "node_name", nc.NodeName, "error", err)
									}
									nodeStates[nc.NodeName].mutex.Lock()
									nodeStates[nc.NodeName].isConnected = false
									nodeStates[nc.NodeName].stream = nil
									nodeStates[nc.NodeName].mutex.Unlock()
									return
								}

								// Send response to worker pool
								select {
								case taskCh <- Task{NodeName: nc.NodeName, Response: resp}:
									cfg.Logger.Debug("Sent response to worker pool", "node_name", nc.NodeName)
								case <-ctx.Done():
									cfg.Logger.Debug("Stopped sending to task channel due to context cancellation", "node_name", nc.NodeName)
									nodeStates[nc.NodeName].mutex.Lock()
									nodeStates[nc.NodeName].isConnected = false
									nodeStates[nc.NodeName].stream.CloseSend()
									nodeStates[nc.NodeName].stream = nil
									nodeStates[nc.NodeName].mutex.Unlock()
									return
								}
							}
						}()

					case <-userTicker.C:
						nodeStates[nc.NodeName].mutex.Lock()
						if !nodeStates[nc.NodeName].isConnected || nodeStates[nc.NodeName].stream == nil {
							cfg.Logger.Debug("No active connection for user sync", "node_name", nc.NodeName)
							nodeStates[nc.NodeName].mutex.Unlock()
							continue
						}
						if err := nodeStates[nc.NodeName].stream.Send(&proto.NodeDataRequest{
							Request: &proto.NodeDataRequest_ListUsers{
								ListUsers: &proto.ListUsersRequest{},
							},
						}); err != nil {
							cfg.Logger.Error("Failed to send list users request", "node_name", nc.NodeName, "error", err)
							nodeStates[nc.NodeName].isConnected = false
							nodeStates[nc.NodeName].stream.CloseSend()
							nodeStates[nc.NodeName].stream = nil
							nodeStates[nc.NodeName].mutex.Unlock()
							continue
						}
						cfg.Logger.Debug("Sent list users request", "node_name", nc.NodeName)
						nodeStates[nc.NodeName].mutex.Unlock()
					}
				}
			}(nc)
		}

		// Wait for all node goroutines to complete
		nodeWG.Wait()
		// Close task channel to stop workers
		close(taskCh)
		// Wait for all workers to finish
		workerWG.Wait()
		cfg.Logger.Debug("Node data monitoring stopped")
	}()
}
