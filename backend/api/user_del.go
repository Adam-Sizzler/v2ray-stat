package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// DeleteUserRequest represents the JSON request structure for deleting a user.
type DeleteUserRequest struct {
	Username   string   `json:"username"`
	InboundTag string   `json:"inbound_tag"`
	Nodes      []string `json:"nodes,omitempty"` // Nodes field is optional
}

// DeleteUserFromNode sends a gRPC request to a node to delete a user.
func DeleteUserFromNode(ctx context.Context, node config.NodeConfig, username, inboundTag string, cfg *config.Config) error {
	cfg.Logger.Debug("Deleting user from node", "node_name", node.NodeName, "username", username, "inbound_tag", inboundTag)

	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create client for node", "node_name", node.NodeName, "error", err)
		return fmt.Errorf("failed to create client for node %s: %w", node.NodeName, err)
	}
	defer func() { nodeClient.Client = nil }() // Ensure client is cleaned up

	grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := nodeClient.Client.DeleteUser(grpcCtx, &proto.DeleteUserRequest{
		Username:   username,
		InboundTag: inboundTag,
	})
	if err != nil {
		cfg.Logger.Error("Failed to delete user via gRPC", "node_name", node.NodeName, "username", username, "error", err)
		return fmt.Errorf("failed to delete user from node %s: %w", node.NodeName, err)
	}

	if resp.Status.Code != int32(codes.OK) {
		cfg.Logger.Error("Node returned error", "node_name", node.NodeName, "username", username, "status_code", resp.Status.Code, "message", resp.Status.Message)
		return fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Status.Message)
	}

	cfg.Logger.Info("User deleted successfully from node", "node_name", node.NodeName, "username", username)
	return nil
}

// DeleteUserHandler handles HTTP requests to delete a user from specified nodes.
func DeleteUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Received DeleteUser HTTP request", "method", r.Method)

		// Check HTTP method
		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, `{"error": "method not allowed, use POST"}`, http.StatusMethodNotAllowed)
			return
		}

		// Parse JSON request body
		var req DeleteUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Error("Failed to parse JSON request", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to parse JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Validate required fields
		if req.Username == "" || req.InboundTag == "" {
			cfg.Logger.Warn("Missing required fields", "username", req.Username, "inbound_tag", req.InboundTag)
			http.Error(w, `{"error": "username and inbound_tag are required"}`, http.StatusBadRequest)
			return
		}

		// Determine target nodes
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

		// Parallel processing of nodes
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
				err := DeleteUserFromNode(ctx, node, req.Username, req.InboundTag, cfg)
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
				cfg.Logger.Error("Error deleting user from node", "node_name", res.nodeName, "error", res.err)
				errs = append(errs, fmt.Errorf("failed to delete user from %s: %w", res.nodeName, res.err))
				results[res.nodeName] = res.err.Error()
			} else {
				results[res.nodeName] = "success"
			}
		}

		// Form JSON response
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if len(errs) > 0 {
			cfg.Logger.Error("Errors occurred while deleting user", "username", req.Username, "error_count", len(errs), "errors", errs)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": results,
			}); err != nil {
				cfg.Logger.Error("Failed to encode JSON response", "error", err)
			}
			return
		}

		cfg.Logger.Info("User deleted successfully from all nodes", "username", req.Username, "node_count", len(targetNodes))
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"message": "user deleted successfully from all specified nodes",
		}); err != nil {
			cfg.Logger.Error("Failed to encode JSON response", "error", err)
		}
	}
}
