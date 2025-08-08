package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DeleteUserRequest представляет структуру JSON-запроса
type DeleteUserRequest struct {
	User       string   `json:"user"`
	InboundTag string   `json:"inbound_tag"`
	Nodes      []string `json:"nodes,omitempty"` // Поле nodes опционально
}

// DeleteUserFromNode отправляет gRPC-запрос на ноду для удаления пользователя
func DeleteUserFromNode(node config.NodeConfig, user, inboundTag string) error {
	var opts []grpc.DialOption

	// Настройка mTLS, если оно указано в конфигурации ноды
	if node.MTLSConfig != nil {
		creds, err := credentials.NewClientTLSFromFile(node.MTLSConfig.CACert, "")
		if err != nil {
			return fmt.Errorf("failed to load CA cert for node %s: %v", node.NodeName, err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Используем полный URL ноды из конфигурации (адрес:порт)
	conn, err := grpc.NewClient(node.URL, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to node %s (%s): %v", node.NodeName, node.URL, err)
	}
	defer conn.Close()

	client := proto.NewNodeServiceClient(conn)
	resp, err := client.DeleteUser(context.Background(), &proto.DeleteUserRequest{
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

		// Удаляем пользователя с каждой ноды и собираем ошибки
		errors := make(map[string]string)
		for _, node := range targetNodes {
			err := DeleteUserFromNode(node, req.User, req.InboundTag)
			if err != nil {
				errors[node.NodeName] = err.Error()
			}
		}

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
