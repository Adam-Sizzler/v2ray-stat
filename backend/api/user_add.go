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

// AddUserToNode отправляет gRPC-запрос на ноду для добавления пользователя
func AddUserToNode(node config.NodeConfig, user, inboundTag string) (string, error) {
	var opts []grpc.DialOption

	// Настройка mTLS, если оно указано в конфигурации ноды
	if node.MTLSConfig != nil {
		creds, err := credentials.NewClientTLSFromFile(node.MTLSConfig.CACert, "")
		if err != nil {
			return "", fmt.Errorf("failed to load CA cert for node %s: %v", node.Name, err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Используем полный URL ноды из конфигурации (адрес:порт)
	conn, err := grpc.Dial(node.URL, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to connect to node %s (%s): %v", node.Name, node.URL, err)
	}
	defer conn.Close()

	client := proto.NewNodeServiceClient(conn)
	resp, err := client.AddUser(context.Background(), &proto.AddUserRequest{
		User:       user,
		InboundTag: inboundTag,
	})
	if err != nil {
		return "", fmt.Errorf("failed to add user to node %s: %v", node.Name, err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("node %s returned error: %s", node.Name, resp.Error)
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
		user := r.URL.Query().Get("user")
		inboundTag := r.URL.Query().Get("inbound_tag")
		nodeParam := r.URL.Query().Get("nodes") // Параметр nodes может быть пустым или списком через запятую

		var targetNodes []config.NodeConfig
		if nodeParam == "" {
			// Если параметр nodes не указан, берем все ноды из конфигурации
			targetNodes = GetNodesFromConfig(cfg)
		} else {
			// Если указаны конкретные ноды, фильтруем их по именам
			nodeNames := strings.SplitSeq(nodeParam, ",")
			for nodeName := range nodeNames {
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

		results := make(map[string]string)
		for _, node := range targetNodes {
			credential, err := AddUserToNode(node, user, inboundTag)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to add user to %s: %v", node.Name, err), http.StatusInternalServerError)
				return
			}
			results[node.Name] = credential
		}

		// Формируем JSON-ответ
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}
