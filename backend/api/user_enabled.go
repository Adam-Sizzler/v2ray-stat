package api

import (
	"context"
	"database/sql"
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
	"v2ray-stat/node/proto"
)

// UserEnabledRequest represents the JSON request structure for enabling/disabling a user.
type UserEnabledRequest struct {
	Username string   `json:"username"`
	Enabled  bool     `json:"enabled"`
	Nodes    []string `json:"nodes,omitempty"` // Nodes field is optional
}

// UserEnabledResponse represents the JSON response structure for enabling/disabling a user.
type UserEnabledResponse struct {
	Message      string   `json:"message"`
	SuccessNodes []string `json:"success_nodes,omitempty"`
	Errors       []string `json:"errors,omitempty"`
}

// UserEnabledHandler handles HTTP requests to enable or disable a user on specified nodes.
func UserEnabledHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Received UserEnabled HTTP request", "method", r.Method)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		// Check HTTP method
		if r.Method != http.MethodPatch {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, `{"error": "method not allowed, use PATCH"}`, http.StatusMethodNotAllowed)
			return
		}

		// Decode JSON request body
		var req UserEnabledRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Warn("Failed to parse JSON request", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to parse JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Validate parameters
		if req.Username == "" {
			cfg.Logger.Warn("Missing username field")
			http.Error(w, `{"error": "username is required"}`, http.StatusBadRequest)
			return
		}
		if len(req.Username) > 40 {
			cfg.Logger.Warn("Username too long", "length", len(req.Username))
			http.Error(w, `{"error": "username too long (max 40 characters)"}`, http.StatusBadRequest)
			return
		}

		cfg.Logger.Debug("Request parameters", "username", req.Username, "enabled", req.Enabled, "nodes", req.Nodes)

		// Check user existence in user_traffic
		err := manager.ExecuteHighPriority(func(db *sql.DB) error {
			var count int
			err := db.QueryRow("SELECT COUNT(*) FROM user_traffic WHERE user = ?", req.Username).Scan(&count)
			if err != nil {
				return fmt.Errorf("failed to check user existence: %w", err)
			}
			if count == 0 {
				return fmt.Errorf("user %s not found in user_traffic", req.Username)
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Warn("User check failed", "username", req.Username, "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		// Determine target nodes
		var targetNodes []config.NodeConfig
		if len(req.Nodes) == 0 {
			targetNodes = GetNodesFromConfig(cfg)
			cfg.Logger.Debug("No nodes specified, applying to all nodes", "node_count", len(targetNodes))
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

		// Parallel gRPC requests
		var (
			errors       []string
			successNodes []string
			mu           sync.Mutex
			wg           sync.WaitGroup
		)

		ctx := r.Context()
		for _, node := range targetNodes {
			wg.Add(1)
			go func(node config.NodeConfig) {
				defer wg.Done()

				nodeClient, err := db.NewNodeClient(node, cfg)
				if err != nil {
					errMsg := fmt.Sprintf("failed to create client for node %s: %v", node.NodeName, err)
					cfg.Logger.Error(errMsg)
					mu.Lock()
					errors = append(errors, errMsg)
					mu.Unlock()
					return
				}
				defer func() { nodeClient.Client = nil }()

				grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				resp, err := nodeClient.Client.SetUserEnabled(grpcCtx, &proto.SetUserEnabledRequest{
					Username: req.Username,
					Enabled:  req.Enabled,
				})
				if err != nil {
					errMsg := fmt.Sprintf("failed to set enabled status on node %s: %v", node.NodeName, err)
					cfg.Logger.Error(errMsg)
					mu.Lock()
					errors = append(errors, errMsg)
					mu.Unlock()
					return
				}
				if resp.Status.Code != int32(codes.OK) {
					errMsg := fmt.Sprintf("node %s returned error: %s", node.NodeName, resp.Status.Message)
					cfg.Logger.Warn(errMsg)
					mu.Lock()
					errors = append(errors, errMsg)
					mu.Unlock()
					return
				}

				cfg.Logger.Info("User enabled status updated on node", "node_name", node.NodeName, "username", req.Username, "enabled", req.Enabled)
				mu.Lock()
				successNodes = append(successNodes, node.NodeName)
				mu.Unlock()
			}(node)
		}

		wg.Wait()

		// Update database for successful nodes
		if len(successNodes) > 0 {
			err := manager.ExecuteHighPriority(func(db *sql.DB) error {
				tx, err := db.Begin()
				if err != nil {
					return fmt.Errorf("failed to begin transaction: %w", err)
				}
				defer tx.Rollback()

				enabledStr := "false"
				if req.Enabled {
					enabledStr = "true"
				}
				for _, nodeName := range successNodes {
					_, err := tx.Exec("UPDATE user_traffic SET enabled = ? WHERE node_name = ? AND user = ?", enabledStr, nodeName, req.Username)
					if err != nil {
						return fmt.Errorf("failed to update status for %s on node %s: %w", req.Username, nodeName, err)
					}
				}

				return tx.Commit()
			})
			if err != nil {
				cfg.Logger.Error("Failed to update database status", "username", req.Username, "error", err)
				http.Error(w, fmt.Sprintf(`{"error": "failed to update database status: %v"}`, err), http.StatusInternalServerError)
				return
			}
		}

		// Form response
		resp := UserEnabledResponse{
			SuccessNodes: successNodes,
		}
		if len(errors) > 0 {
			resp.Errors = errors
			if len(successNodes) > 0 {
				resp.Message = fmt.Sprintf("partial success: updated nodes %v", successNodes)
				w.WriteHeader(http.StatusMultiStatus)
				cfg.Logger.Info("Partial success", "username", req.Username, "enabled", req.Enabled, "success_nodes", successNodes, "errors", errors)
			} else {
				resp.Message = fmt.Sprintf("errors occurred: %s", strings.Join(errors, "; "))
				cfg.Logger.Error("Failed to update user status", "username", req.Username, "errors", errors)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			resp.Message = "user status updated successfully"
			cfg.Logger.Info("User status updated successfully", "username", req.Username, "enabled", req.Enabled, "node_count", len(successNodes))
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			cfg.Logger.Error("Failed to encode JSON response", "error", err)
			http.Error(w, `{"error": "internal server error"}`, http.StatusInternalServerError)
			return
		}
	}
}
