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
	"v2ray-stat/backend/users"
	"v2ray-stat/node/proto"
)

// SetUsersEnabledRequest represents the JSON request structure for enabling/disabling users.
type SetUsersEnabledRequest struct {
	Users   []string `json:"users"` // Массив пользователей (может быть строка через запятую)
	Enabled bool     `json:"enabled"`
	Nodes   []string `json:"nodes,omitempty"` // Nodes field is optional
}

// SetUsersEnabledToNode sends a gRPC request to a node to enable or disable users.
func SetUsersEnabledToNode(ctx context.Context, node config.NodeConfig, usernames []string, enabled bool, cfg *config.Config) error {
	cfg.Logger.Debug("Setting enabled status for users on node", "node_name", node.NodeName, "usernames", usernames, "enabled", enabled)

	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create client for node", "node_name", node.NodeName, "error", err)
		return fmt.Errorf("failed to create client for node %s: %w", node.NodeName, err)
	}
	defer func() { nodeClient.Client = nil }() // Ensure client is cleaned up

	grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := nodeClient.Client.SetUserEnabled(grpcCtx, &proto.SetUserEnabledRequest{
		Usernames: usernames,
		Enabled:   enabled,
	})
	if err != nil {
		cfg.Logger.Error("Failed to set enabled status via gRPC", "node_name", node.NodeName, "usernames", usernames, "error", err)
		return fmt.Errorf("failed to set enabled status on node %s: %w", node.NodeName, err)
	}

	if resp == nil || resp.Status == nil {
		cfg.Logger.Error("Node returned nil response or status", "node_name", node.NodeName, "usernames", usernames)
		return fmt.Errorf("node %s returned nil response or status", node.NodeName)
	}

	if resp.Status.Code != int32(codes.OK) {
		cfg.Logger.Error("Node returned error", "node_name", node.NodeName, "usernames", usernames, "status_code", resp.Status.Code, "message", resp.Status.Message)
		return fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Status.Message)
	}

	cfg.Logger.Info("Users enabled status updated successfully on node", "node_name", node.NodeName, "usernames", usernames, "enabled", enabled)
	return nil
}

// SetUsersEnabledHandler handles HTTP requests to enable or disable multiple users on specified nodes.
func SetUserEnabledHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Received SetUsersEnabled HTTP request", "method", r.Method)

		if r.Method != http.MethodPatch {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, `{"error": "method not allowed, use PATCH"}`, http.StatusMethodNotAllowed)
			return
		}

		var req SetUsersEnabledRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Error("Failed to parse JSON request", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to parse JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Если users пришла как строка через запятую, распарсить
		if len(req.Users) == 1 {
			req.Users = strings.Split(req.Users[0], ",")
			for i := range req.Users {
				req.Users[i] = strings.TrimSpace(req.Users[i])
			}
		}

		if len(req.Users) == 0 {
			cfg.Logger.Warn("Missing users field")
			http.Error(w, `{"error": "users is required"}`, http.StatusBadRequest)
			return
		}

		var validUsers []string
		for _, user := range req.Users {
			if user != "" {
				if len(user) > 40 {
					cfg.Logger.Warn("Username too long", "username", user, "length", len(user))
					http.Error(w, fmt.Sprintf(`{"error": "username %s too long (max 40 characters)"}`, user), http.StatusBadRequest)
					return
				}
				validUsers = append(validUsers, user)
			}
		}
		if len(validUsers) == 0 {
			cfg.Logger.Warn("No valid users provided")
			http.Error(w, `{"error": "no valid users provided"}`, http.StatusBadRequest)
			return
		}
		req.Users = validUsers

		// Проверка существования пользователей в базе
		err := manager.ExecuteHighPriority(func(db *sql.DB) error {
			for _, user := range req.Users {
				var count int
				err := db.QueryRow("SELECT COUNT(*) FROM user_traffic WHERE user = ?", user).Scan(&count)
				if err != nil {
					return fmt.Errorf("failed to check user existence for %s: %w", user, err)
				}
				if count == 0 {
					return fmt.Errorf("user %s not found in user_traffic", user)
				}
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Warn("User check failed", "users", req.Users, "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		// Определение целевых нод
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

		// Параллельная обработка нод
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
				err := SetUsersEnabledToNode(ctx, node, req.Users, req.Enabled, cfg)
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
				cfg.Logger.Error("Error setting enabled status for users on node", "node_name", res.nodeName, "error", res.err)
				errs = append(errs, fmt.Errorf("failed to set enabled status for users on %s: %w", res.nodeName, res.err))
				results[res.nodeName] = res.err.Error()
				continue
			}
			results[res.nodeName] = "success"
		}

		if len(errs) > 0 {
			cfg.Logger.Error("Errors occurred while setting enabled status", "users", req.Users, "error_count", len(errs), "errors", errs)
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

		// Обновление базы данных для успешных нод
		err = manager.ExecuteHighPriority(func(db *sql.DB) error {
			tx, err := db.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin transaction: %w", err)
			}
			defer tx.Rollback()

			enabledStr := "false"
			if req.Enabled {
				enabledStr = "true"
			}
			for _, nodeName := range targetNodes {
				for _, user := range req.Users {
					_, err := tx.Exec("UPDATE user_traffic SET enabled = ? WHERE node_name = ? AND user = ?", enabledStr, nodeName.NodeName, user)
					if err != nil {
						return fmt.Errorf("failed to update status for %s on node %s: %w", user, nodeName.NodeName, err)
					}
				}
			}
			return tx.Commit()
		})
		if err != nil {
			cfg.Logger.Error("Failed to update database status", "users", req.Users, "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to update database status: %v"}`, err), http.StatusInternalServerError)
			return
		}

		// Вызов синхронизации
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
			cfg.Logger.Error("Failed to sync users after setting enabled status", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to sync users: %v"}`, err), http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Users enabled status updated and synced successfully", "users", req.Users, "enabled", req.Enabled, "node_count", len(targetNodes))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"usernames": req.Users,
			"results":   results,
			"message":   fmt.Sprintf("users %s status updated and synced successfully to all specified nodes", statusString(req.Enabled)),
		}); err != nil {
			cfg.Logger.Error("Failed to encode JSON response", "error", err)
		}
	}
}

// statusString returns a string representation of the enabled status.
func statusString(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
