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

// AddUserRequest represents the JSON request structure for adding a user.
type AddUserRequest struct {
	Username   string   `json:"username"`
	InboundTag string   `json:"inbound_tag"`
	Nodes      []string `json:"nodes,omitempty"` // Nodes field is optional
}

// AddUserToNode sends a gRPC request to a node to add a user.
func AddUserToNode(ctx context.Context, node config.NodeConfig, username, inboundTag string, cfg *config.Config) (string, error) {
	cfg.Logger.Debug("Adding user to node", "node_name", node.NodeName, "username", username, "inbound_tag", inboundTag)

	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create client for node", "node_name", node.NodeName, "error", err)
		return "", fmt.Errorf("failed to create client for node %s: %w", node.NodeName, err)
	}
	defer func() { nodeClient.Client = nil }() // Ensure client is cleaned up

	grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := nodeClient.Client.AddUser(grpcCtx, &proto.AddUserRequest{
		Username:   username,
		InboundTag: inboundTag,
	})
	if err != nil {
		cfg.Logger.Error("Failed to add user via gRPC", "node_name", node.NodeName, "username", username, "error", err)
		return "", fmt.Errorf("failed to add user to node %s: %w", node.NodeName, err)
	}

	if resp.Status.Code != int32(codes.OK) {
		cfg.Logger.Error("Node returned error", "node_name", node.NodeName, "username", username, "status_code", resp.Status.Code, "message", resp.Status.Message)
		return "", fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Status.Message)
	}

	cfg.Logger.Info("User added successfully to node", "node_name", node.NodeName, "username", username, "credential", resp.Credential)
	return resp.Credential, nil
}

// GetNodesFromConfig returns the list of nodes from the configuration.
func GetNodesFromConfig(cfg *config.Config) []config.NodeConfig {
	return cfg.V2rayStat.Nodes
}

// AddUserHandler handles HTTP requests to add a user to specified nodes.
func AddUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Received AddUser HTTP request", "method", r.Method)

		// Check HTTP method
		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "method not allowed, use POST", http.StatusMethodNotAllowed)
			return
		}

		// Parse JSON request body
		var req AddUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Error("Failed to parse JSON request", "error", err)
			http.Error(w, fmt.Sprintf("failed to parse JSON: %v", err), http.StatusBadRequest)
			return
		}

		// Validate required fields
		if req.Username == "" || req.InboundTag == "" {
			cfg.Logger.Warn("Missing required fields", "username", req.Username, "inbound_tag", req.InboundTag)
			http.Error(w, "username and inbound_tag are required", http.StatusBadRequest)
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
				http.Error(w, "no valid nodes found for the provided names", http.StatusBadRequest)
				return
			}
			cfg.Logger.Debug("Selected nodes", "node_count", len(targetNodes), "nodes", req.Nodes)
		}

		// Parallel processing of nodes
		type result struct {
			nodeName   string
			credential string
			err        error
		}

		resultsCh := make(chan result, len(targetNodes))
		var wg sync.WaitGroup

		ctx := r.Context()
		for _, node := range targetNodes {
			wg.Add(1)
			go func(node config.NodeConfig) {
				defer wg.Done()

				credential, err := AddUserToNode(ctx, node, req.Username, req.InboundTag, cfg)
				resultsCh <- result{
					nodeName:   node.NodeName,
					credential: credential,
					err:        err,
				}
			}(node)
		}

		wg.Wait()
		close(resultsCh)

		results := make(map[string]string)
		var errs []error
		for res := range resultsCh {
			if res.err != nil {
				cfg.Logger.Error("Error adding user to node", "node_name", res.nodeName, "error", res.err)
				errs = append(errs, fmt.Errorf("failed to add user to %s: %w", res.nodeName, res.err))
				continue
			}
			results[res.nodeName] = res.credential
		}

		if len(errs) > 0 {
			cfg.Logger.Error("Errors occurred while adding user", "error_count", len(errs), "errors", errs)
			http.Error(w, fmt.Sprintf("errors occurred: %v", errs), http.StatusInternalServerError)
			return
		}

		// Return successful response
		cfg.Logger.Info("User added successfully to all nodes", "username", req.Username, "node_count", len(results))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(results); err != nil {
			cfg.Logger.Error("Failed to encode JSON response", "error", err)
		}
	}
}
