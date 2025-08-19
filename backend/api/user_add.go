package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/backend/users" // Импорт пакета users для SyncUsersWithNode
	"v2ray-stat/node/proto"
)

// AddUsersRequest represents the JSON request structure for adding one or more users.
type AddUsersRequest struct {
	Users      []string `json:"users"` // Массив пользователей (может быть строка через запятую)
	InboundTag string   `json:"inbound_tag"`
	Nodes      []string `json:"nodes,omitempty"` // Nodes field is optional
}

// AddUsersToNode sends a gRPC request to a node to add one or more users.
func AddUsersToNode(ctx context.Context, node config.NodeConfig, usernames []string, inboundTag string, cfg *config.Config) error {
	cfg.Logger.Debug("Adding users to node", "node_name", node.NodeName, "usernames", usernames, "inbound_tag", inboundTag)

	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create client for node", "node_name", node.NodeName, "error", err)
		return fmt.Errorf("failed to create client for node %s: %w", node.NodeName, err)
	}
	defer func() { nodeClient.Client = nil }() // Ensure client is cleaned up

	grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := nodeClient.Client.AddUsers(grpcCtx, &proto.AddUsersRequest{
		Usernames:  usernames,
		InboundTag: inboundTag,
	})
	if err != nil {
		cfg.Logger.Error("Failed to add users via gRPC", "node_name", node.NodeName, "usernames", usernames, "error", err)
		return fmt.Errorf("failed to add users to node %s: %w", node.NodeName, err)
	}

	if resp == nil || resp.Status == nil {
		cfg.Logger.Error("Node returned nil response or status", "node_name", node.NodeName, "usernames", usernames)
		return fmt.Errorf("node %s returned nil response or status", node.NodeName)
	}

	if resp.Status.Code != int32(codes.OK) {
		cfg.Logger.Error("Node returned error", "node_name", node.NodeName, "usernames", usernames, "status_code", resp.Status.Code, "message", resp.Status.Message)
		return fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Status.Message)
	}

	cfg.Logger.Info("Users added successfully to node", "node_name", node.NodeName, "usernames", usernames)
	return nil
}

// GetNodesFromConfig returns the list of nodes from the configuration.
func GetNodesFromConfig(cfg *config.Config) []config.NodeConfig {
	return cfg.V2rayStat.Nodes
}

// AddUsersHandler handles HTTP requests to add one or more users to specified nodes.
func AddUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Received AddUsers HTTP request", "method", r.Method)

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, `{"error": "method not allowed, use POST"}`, http.StatusMethodNotAllowed)
			return
		}

		var req AddUsersRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Error("Failed to parse JSON request", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to parse JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		if len(req.Users) == 1 {
			req.Users = strings.Split(req.Users[0], ",")
			for i := range req.Users {
				req.Users[i] = strings.TrimSpace(req.Users[i])
			}
		}

		if len(req.Users) == 0 || req.InboundTag == "" {
			cfg.Logger.Warn("Missing required fields", "users", req.Users, "inbound_tag", req.InboundTag)
			http.Error(w, `{"error": "users and inbound_tag are required"}`, http.StatusBadRequest)
			return
		}

		var validUsers []string
		for _, user := range req.Users {
			if user != "" {
				validUsers = append(validUsers, user)
			}
		}
		if len(validUsers) == 0 {
			cfg.Logger.Warn("No valid users provided")
			http.Error(w, `{"error": "no valid users provided"}`, http.StatusBadRequest)
			return
		}
		req.Users = validUsers

		var targetNodes []config.NodeConfig
		if len(req.Nodes) == 0 {
			targetNodes = GetNodesFromConfig(cfg)
			cfg.Logger.Debug("Using all nodes from config", "node_count", len(targetNodes))
		} else {
			for _, nodeName := range req.Nodes {
				for _, node := range cfg.V2rayStat.Nodes {
					if node.NodeName == nodeName {
						targetNodes = append(targetNodes, node)
						break
					}
				}
			}
			if len(targetNodes) == 0 {
				cfg.Logger.Warn("No valid nodes found", "requested_nodes", req.Nodes)
				http.Error(w, `{"error": "no valid nodes found for the provided names"}`, http.StatusBadRequest)
				return
			}
			cfg.Logger.Debug("Selected nodes", "node_count", len(targetNodes), "nodes", req.Nodes)
		}

		type result struct {
			nodeName string
			err      error
		}

		resultsCh := make(chan result, len(targetNodes))
		var wg sync.WaitGroup

		ctx := r.Context()
		for _, node := range targetNodes {
			wg.Add(1)
			go func(node config.NodeConfig) {
				defer wg.Done()
				err := AddUsersToNode(ctx, node, req.Users, req.InboundTag, cfg)
				resultsCh <- result{
					nodeName: node.NodeName,
					err:      err,
				}
			}(node)
		}

		wg.Wait()
		close(resultsCh)

		results := make(map[string]string)
		var errs []error
		for res := range resultsCh {
			if res.err != nil {
				cfg.Logger.Error("Error adding users to node", "node_name", res.nodeName, "error", res.err)
				errs = append(errs, fmt.Errorf("failed to add users to %s: %w", res.nodeName, res.err))
				results[res.nodeName] = res.err.Error()
				continue
			}
			results[res.nodeName] = "success"
		}

		if len(errs) > 0 {
			cfg.Logger.Error("Errors occurred while adding users", "users", req.Users, "error_count", len(errs), "errors", errs)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"usernames": req.Users,
				"errors":    results,
			}); err != nil {
				cfg.Logger.Error("Failed to encode JSON response", "error", err)
			}
			return
		}

		var nodeClients []*db.NodeClient
		for _, node := range targetNodes {
			client, err := db.NewNodeClient(node, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to create node client for sync", "node_name", node.NodeName, "error", err)
				http.Error(w, fmt.Sprintf(`{"error": "failed to create node client for sync: %v"}`, err), http.StatusInternalServerError)
				return
			}
			nodeClients = append(nodeClients, client)
		}
		defer func() {
			for _, nc := range nodeClients {
				nc.Client = nil
			}
		}()

		if err := users.SyncUsersWithNode(ctx, manager, nodeClients, cfg); err != nil {
			cfg.Logger.Error("Failed to sync users after addition", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to sync users: %v"}`, err), http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Users added and synced successfully to all nodes", "users", req.Users, "node_count", len(targetNodes))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"usernames": req.Users,
			"results":   results,
			"message":   "users added and synced successfully to all specified nodes",
		}); err != nil {
			cfg.Logger.Error("Failed to encode JSON response", "error", err)
		}
	}
}
