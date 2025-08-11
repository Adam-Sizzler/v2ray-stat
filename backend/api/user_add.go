package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// AddUserRequest представляет структуру JSON-запроса
type AddUserRequest struct {
	User       string   `json:"user"`
	InboundTag string   `json:"inbound_tag"`
	Nodes      []string `json:"nodes,omitempty"` // Поле nodes опционально
}

// AddUserToNode отправляет gRPC-запрос на ноду для добавления пользователя
func AddUserToNode(node config.NodeConfig, user, inboundTag string, cfg *config.Config) (string, error) {
	nodeClient, err := db.NewNodeClient(node, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create client for node %s: %v", node.NodeName, err)
	}
	defer func() { /* Закрыть conn если нужно */ nodeClient.Client = nil }()

	resp, err := nodeClient.Client.AddUser(context.Background(), &proto.AddUserRequest{
		User:       user,
		InboundTag: inboundTag,
	})
	if err != nil {
		return "", fmt.Errorf("failed to add user to node %s: %v", node.NodeName, err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("node %s returned error: %s", node.NodeName, resp.Error)
	}
	return resp.Credential, nil
}

// GetNodesFromConfig возвращает список нод из конфигурации
func GetNodesFromConfig(cfg *config.Config) []config.NodeConfig {
	return cfg.V2rayStat.Nodes
}

// AddUserHandler обрабатывает запрос на добавление пользователя
func AddUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Проверка метода HTTP
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed, use POST", http.StatusMethodNotAllowed)
			return
		}

		// Парсим JSON-тело запроса
		var req AddUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("failed to parse JSON: %v", err), http.StatusBadRequest)
			return
		}

		// Проверяем обязательные поля
		if req.User == "" || req.InboundTag == "" {
			http.Error(w, "user and inbound_tag are required", http.StatusBadRequest)
			return
		}

		var targetNodes []config.NodeConfig
		if len(req.Nodes) == 0 {
			// Если nodes не указаны, берем все ноды из конфигурации
			targetNodes = GetNodesFromConfig(cfg)
		} else {
			// Фильтруем ноды по указанным именам
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

		results := make(map[string]string)
		for _, node := range targetNodes {
			// ИЗМЕНЕНИЕ: Добавляем cfg в вызов
			credential, err := AddUserToNode(node, req.User, req.InboundTag, cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to add user to %s: %v", node.NodeName, err), http.StatusInternalServerError)
				return
			}
			results[node.NodeName] = credential
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(results)
	}
}
