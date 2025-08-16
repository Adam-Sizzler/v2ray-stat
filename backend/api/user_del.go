package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// DeleteUserRequest представляет структуру JSON-запроса
type DeleteUserRequest struct {
	User       string   `json:"user"`
	InboundTag string   `json:"inbound_tag"`
	Nodes      []string `json:"nodes,omitempty"`
}

// DeleteUserFromNode отправляет gRPC-запрос на ноду для удаления пользователя
func DeleteUserFromNode(node config.NodeConfig, user, inboundTag string, cfg *config.Config) error {
	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		return fmt.Errorf("failed to create client for node %s: %v", node.NodeName, err)
	}
	defer func() { nodeClient.Client = nil }()

	resp, err := nodeClient.Client.DeleteUser(context.Background(), &proto.DeleteUserRequest{
		User:       user,
		InboundTag: inboundTag,
	})
	if err != nil {
		return fmt.Errorf("failed to delete user from node %s: %v", node.NodeName, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Error)
	}
	return nil
}

// DeleteUserHandler обрабатывает HTTP-запросы на удаление пользователя
func DeleteUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Проверка метода HTTP
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed, use POST", http.StatusMethodNotAllowed)
			return
		}

		// Парсим JSON-тело запроса
		var req DeleteUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("failed to parse JSON: %v", err), http.StatusBadRequest)
			return
		}

		// Проверяем обязательные поля
		if req.User == "" || req.InboundTag == "" {
			http.Error(w, "user and inbound_tag are required", http.StatusBadRequest)
			return
		}

		// Определяем целевые ноды
		var targetNodes []config.NodeConfig
		if len(req.Nodes) == 0 {
			targetNodes = GetNodesFromConfig(cfg)
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
				http.Error(w, "no valid nodes found for the provided names", http.StatusBadRequest)
				return
			}
		}

		// Запускаем удаление параллельно
		var wg sync.WaitGroup
		var mu sync.Mutex
		errors := make(map[string]string)

		for _, node := range targetNodes {
			wg.Add(1)
			go func(n config.NodeConfig) {
				defer wg.Done()
				if err := DeleteUserFromNode(n, req.User, req.InboundTag, cfg); err != nil {
					mu.Lock()
					errors[n.NodeName] = err.Error()
					mu.Unlock()
				}
			}(node)
		}

		wg.Wait()

		// Формируем JSON-ответ
		w.Header().Set("Content-Type", "application/json")
		if len(errors) > 0 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(errors)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "User deleted successfully from all specified nodes"})
	}
}
