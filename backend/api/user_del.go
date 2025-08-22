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
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/backend/users"
	"v2ray-stat/proto"

	"google.golang.org/grpc/codes"
)

// DeleteUserRequest represents the JSON request structure for deleting users.
type DeleteUserRequest struct {
	Users      []string `json:"users"`           // Массив пользователей (может быть строка через запятую)
	InboundTag string   `json:"inbound_tag"`     // Тег входящего соединения
	Nodes      []string `json:"nodes,omitempty"` // Список нод (опционально)
}

// DeleteUsersFromNode sends a gRPC request to a node to delete one or more users.
func DeleteUsersFromNode(ctx context.Context, node config.NodeConfig, usernames []string, inboundTag string, cfg *config.Config) (*proto.ListUsersResponse, error) {
	cfg.Logger.Debug("Deleting users from node", "node_name", node.NodeName, "usernames", usernames, "inbound_tag", inboundTag)

	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create client for node", "node_name", node.NodeName, "error", err)
		return nil, fmt.Errorf("failed to create client for node %s: %w", node.NodeName, err)
	}
	defer func() { nodeClient.Client = nil }() // Ensure client is cleaned up

	grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := nodeClient.Client.DeleteUsers(grpcCtx, &proto.DeleteUsersRequest{
		Usernames:  usernames,
		InboundTag: inboundTag,
	})
	if err != nil {
		cfg.Logger.Error("Failed to delete users via gRPC", "node_name", node.NodeName, "usernames", usernames, "error", err)
		return nil, fmt.Errorf("failed to delete users from node %s: %w", node.NodeName, err)
	}

	if resp == nil || resp.Status == nil {
		cfg.Logger.Error("Node returned nil response or status", "node_name", node.NodeName, "usernames", usernames)
		return nil, fmt.Errorf("node %s returned nil response or status", node.NodeName)
	}

	if resp.Status.Code != int32(codes.OK) {
		cfg.Logger.Error("Node returned error", "node_name", node.NodeName, "usernames", usernames, "status_code", resp.Status.Code, "message", resp.Status.Message)
		return nil, fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Status.Message)
	}

	cfg.Logger.Info("Users deleted successfully from node", "node_name", node.NodeName, "usernames", usernames)
	return resp.Users, nil
}

// DeleteUserHandler handles HTTP POST requests for deleting users from nodes.
func DeleteUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Received DeleteUser HTTP request", "method", r.Method)

		// Проверка метода HTTP
		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method, "expected", http.MethodPost)
			http.Error(w, `{"error": "method not allowed, use POST"}`, http.StatusMethodNotAllowed)
			return
		}

		// Парсинг JSON запроса
		var req DeleteUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Error("Failed to parse JSON request", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "failed to parse JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Парсинг пользователей, если передана строка через запятую
		if len(req.Users) == 1 {
			req.Users = strings.Split(req.Users[0], ",")
			for i := range req.Users {
				req.Users[i] = strings.TrimSpace(req.Users[i])
			}
		}

		// Валидация полей
		if len(req.Users) == 0 {
			cfg.Logger.Warn("Missing users field")
			http.Error(w, `{"error": "users is required"}`, http.StatusBadRequest)
			return
		}
		if req.InboundTag == "" {
			cfg.Logger.Warn("Missing inbound_tag")
			http.Error(w, `{"error": "inbound_tag is required"}`, http.StatusBadRequest)
			return
		}

		// Валидация имен пользователей
		var validUsers []string
		for _, user := range req.Users {
			if validateUsername(user) {
				validUsers = append(validUsers, user)
			} else {
				cfg.Logger.Warn("Invalid username", "username", user)
				http.Error(w, fmt.Sprintf(`{"error": "username %s contains invalid characters or is too long"}`, user), http.StatusBadRequest)
				return
			}
		}
		if len(validUsers) == 0 {
			cfg.Logger.Warn("No valid users provided")
			http.Error(w, `{"error": "no valid users provided"}`, http.StatusBadRequest)
			return
		}
		req.Users = validUsers

		// Проверка существования пользователей
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
		var nodeClients []*db.NodeClient
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

		// Создание клиентов для всех нод
		for _, node := range targetNodes {
			nodeClient, err := db.NewNodeClient(node, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to create client for node", "node_name", node.NodeName, "error", err)
				http.Error(w, fmt.Sprintf(`{"error": "failed to create client for node %s: %v"}`, node.NodeName, err), http.StatusInternalServerError)
				return
			}
			nodeClients = append(nodeClients, nodeClient)
		}

		// Параллельная обработка нод
		type result struct {
			nodeName string
			users    *proto.ListUsersResponse
			err      error
		}

		resultsCh := make(chan result, len(targetNodes))
		var wg sync.WaitGroup

		ctx := r.Context()
		for i, node := range targetNodes {
			wg.Add(1)
			go func(node config.NodeConfig, nodeClient *db.NodeClient) {
				defer wg.Done()
				users, err := DeleteUsersFromNode(ctx, node, req.Users, req.InboundTag, cfg)
				resultsCh <- result{
					nodeName: node.NodeName,
					users:    users,
					err:      err,
				}
			}(node, nodeClients[i])
		}

		wg.Wait()
		close(resultsCh)

		// Обработка результатов
		results := make(map[string]string)
		var errs []error
		for res := range resultsCh {
			if res.err != nil {
				cfg.Logger.Error("Error deleting users from node", "node_name", res.nodeName, "error", res.err)
				errs = append(errs, fmt.Errorf("failed to delete users from %s: %w", res.nodeName, res.err))
				results[res.nodeName] = res.err.Error()
				continue
			}
			results[res.nodeName] = "success"

			// Обновление базы данных
			if res.users != nil {
				err := manager.ExecuteHighPriority(func(db *sql.DB) error {
					tx, err := db.BeginTx(ctx, nil)
					if err != nil {
						return fmt.Errorf("start transaction for node %s: %w", res.nodeName, err)
					}
					defer tx.Rollback()

					stmtDeleteUser, err := tx.Prepare("DELETE FROM user_traffic WHERE node_name = ? AND user = ?")
					if err != nil {
						return fmt.Errorf("prepare delete user statement: %w", err)
					}
					defer stmtDeleteUser.Close()

					stmtDeleteID, err := tx.Prepare("DELETE FROM user_ids WHERE node_name = ? AND user = ?")
					if err != nil {
						return fmt.Errorf("prepare delete id statement: %w", err)
					}
					defer stmtDeleteID.Close()

					for _, user := range req.Users {
						_, err := stmtDeleteUser.Exec(res.nodeName, user)
						if err != nil {
							return fmt.Errorf("delete user %s from user_traffic: %w", user, err)
						}
						_, err = stmtDeleteID.Exec(res.nodeName, user)
						if err != nil {
							return fmt.Errorf("delete user %s from user_ids: %w", user, err)
						}
					}

					return tx.Commit()
				})
				if err != nil {
					cfg.Logger.Error("Failed to update database for node", "node_name", res.nodeName, "error", err)
					errs = append(errs, fmt.Errorf("failed to update database for %s: %w", res.nodeName, err))
					results[res.nodeName] = err.Error()
					continue
				}
			}
		}

		// Синхронизация с нодами
		if len(errs) == 0 {
			if err := users.SyncUsersWithNode(ctx, manager, nodeClients, cfg); err != nil {
				cfg.Logger.Error("Failed to sync users with nodes", "error", err)
				errs = append(errs, fmt.Errorf("failed to sync users: %w", err))
				for _, node := range targetNodes {
					results[node.NodeName] = err.Error()
				}
			}
		}

		// Закрытие клиентов
		for _, nodeClient := range nodeClients {
			nodeClient.Client = nil
		}

		// Формирование ответа
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if len(errs) > 0 {
			cfg.Logger.Error("Errors occurred during delete user operation", "users", req.Users, "error_count", len(errs), "errors", errs)
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"usernames": req.Users,
				"errors":    results,
			}); err != nil {
				cfg.Logger.Error("Failed to encode JSON response", "error", err)
			}
			return
		}

		cfg.Logger.Info("Delete user operation completed successfully", "users", req.Users, "node_count", len(targetNodes))
		w.WriteHeader(http.StatusOK)
		message := "operation delete completed successfully for all specified nodes"
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"usernames": req.Users,
			"results":   results,
			"message":   message,
		}); err != nil {
			cfg.Logger.Error("Failed to encode JSON response", "error", err)
		}
	}
}
