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

		// Определяем список нод
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

		// --- Параллельная обработка ---
		type result struct {
			nodeName   string
			credential string
			err        error
		}

		resultsCh := make(chan result, len(targetNodes))
		var wg sync.WaitGroup

		for _, node := range targetNodes {
			wg.Add(1)
			go func(node config.NodeConfig) {
				defer wg.Done()

				credential, err := AddUserToNode(node, req.User, req.InboundTag, cfg)
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
				errs = append(errs, fmt.Errorf("failed to add user to %s: %v", res.nodeName, res.err))
				continue
			}
			results[res.nodeName] = res.credential
		}

		if len(errs) > 0 {
			http.Error(w, fmt.Sprintf("errors occurred: %v", errs), http.StatusInternalServerError)
			return
		}

		// Отдаём успешный результат
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(results)
	}
}
