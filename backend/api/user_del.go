package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DeleteUserFromNode отправляет gRPC-запрос на ноду для удаления пользователя
func DeleteUserFromNode(node config.NodeConfig, user, inboundTag string) error {
	var opts []grpc.DialOption

	// Настройка mTLS, если оно указано в конфигурации ноды
	if node.MTLSConfig != nil {
		creds, err := credentials.NewClientTLSFromFile(node.MTLSConfig.CACert, "")
		if err != nil {
			return fmt.Errorf("failed to load CA cert for node %s: %v", node.Name, err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Используем полный URL ноды из конфигурации (адрес:порт)
	conn, err := grpc.NewClient(node.URL, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to node %s (%s): %v", node.Name, node.URL, err)
	}
	defer conn.Close()

	client := proto.NewNodeServiceClient(conn)
	resp, err := client.DeleteUser(context.Background(), &proto.DeleteUserRequest{
		User:       user,
		InboundTag: inboundTag,
	})
	if err != nil {
		return fmt.Errorf("failed to delete user from node %s: %v", node.Name, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("node %s returned error: %s", node.Name, resp.Error)
	}
	return nil
}

// DeleteUserHandler обрабатывает HTTP-запросы на удаление пользователя
func DeleteUserHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		inboundTag := r.URL.Query().Get("inbound_tag")
		nodeParam := r.URL.Query().Get("nodes") // Параметр nodes может быть пустым или списком через запятую

		// Проверка обязательных полей
		if user == "" || inboundTag == "" {
			http.Error(w, "user and inbound_tag parameters are required", http.StatusBadRequest)
			return
		}

		// Определяем целевые ноды
		var targetNodes []config.NodeConfig
		if nodeParam == "" {
			// Если параметр nodes не указан, берем все ноды из конфигурации
			targetNodes = cfg.V2rayStat.Nodes
		} else {
			// Если указаны конкретные ноды, фильтруем их по именам
			nodeNames := strings.Split(nodeParam, ",")
			for _, nodeName := range nodeNames {
				for _, node := range cfg.V2rayStat.Nodes {
					if node.Name == nodeName {
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
			err := DeleteUserFromNode(node, user, inboundTag)
			if err != nil {
				errors[node.Name] = err.Error()
			}
		}

		// Формируем ответ
		w.Header().Set("Content-Type", "application/json")
		if len(errors) > 0 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(errors)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "User deleted successfully from all specified nodes")
	}
}
