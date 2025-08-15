package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// UserEnabledRequest представляет тело JSON-запроса для эндпоинта /api/v1/user/enabled
type UserEnabledRequest struct {
	User    string   `json:"user"`
	Enabled bool     `json:"enabled"`
	Nodes   []string `json:"nodes,omitempty"`
}

// UserEnabledResponse представляет JSON-ответ эндпоинта
type UserEnabledResponse struct {
	Message      string   `json:"message"`
	SuccessNodes []string `json:"success_nodes,omitempty"`
	Errors       []string `json:"errors,omitempty"`
}

// UserEnabledHandler обрабатывает запросы на включение/выключение пользователя на указанных нодах.
func UserEnabledHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Начало обработки запроса UserEnabledHandler")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodPatch {
			cfg.Logger.Warn("Неверный метод HTTP", "method", r.Method)
			http.Error(w, `{"error": "Неверный метод. Используйте PATCH"}`, http.StatusMethodNotAllowed)
			return
		}

		// Декодируем JSON-тело запроса
		var req UserEnabledRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			cfg.Logger.Warn("Ошибка разбора JSON", "error", err)
			http.Error(w, `{"error": "Ошибка разбора JSON"}`, http.StatusBadRequest)
			return
		}

		// Валидация параметров
		if req.User == "" {
			cfg.Logger.Warn("Пустой идентификатор пользователя")
			http.Error(w, `{"error": "Поле user обязательно"}`, http.StatusBadRequest)
			return
		}
		if len(req.User) > 40 {
			cfg.Logger.Warn("Идентификатор пользователя слишком длинный", "length", len(req.User))
			http.Error(w, `{"error": "Идентификатор пользователя слишком длинный (макс. 40 символов)"}`, http.StatusBadRequest)
			return
		}

		cfg.Logger.Debug("Получены параметры запроса", "user", req.User, "enabled", req.Enabled, "nodes", req.Nodes)

		// Проверка существования пользователя в user_traffic
		err := manager.ExecuteHighPriority(func(db *sql.DB) error {
			var count int
			err := db.QueryRow("SELECT COUNT(*) FROM user_traffic WHERE user = ?", req.User).Scan(&count)
			if err != nil {
				return fmt.Errorf("failed to check user existence: %v", err)
			}
			if count == 0 {
				return fmt.Errorf("user %s not found in user_traffic", req.User)
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Warn("User check failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		// Определяем целевые ноды
		var targetNodes []config.NodeConfig
		if len(req.Nodes) == 0 {
			// Если nodes не указаны, берем все ноды из конфигурации
			targetNodes = GetNodesFromConfig(cfg)
			cfg.Logger.Debug("Nodes не указаны, применяем ко всем нодам", "nodes", targetNodes)
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
				cfg.Logger.Warn("No valid nodes found for the provided names", "nodes", req.Nodes)
				http.Error(w, `{"error": "no valid nodes found for the provided names"}`, http.StatusBadRequest)
				return
			}
			cfg.Logger.Debug("Указаны nodes", "nodes", targetNodes)
		}

		var errors []string
		successNodes := make([]string, 0)

		// Отправка gRPC-запросов на ноды
		for _, node := range targetNodes {
			nodeClient, err := db.NewNodeClient(node, cfg)
			if err != nil {
				errMsg := fmt.Sprintf("failed to create client for node %s: %v", node.NodeName, err)
				cfg.Logger.Error(errMsg)
				errors = append(errors, errMsg)
				continue
			}
			defer func() { nodeClient.Client = nil }()

			grpcCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := nodeClient.Client.SetEnabled(grpcCtx, &proto.SetEnabledRequest{User: req.User, Enabled: req.Enabled})
			if err != nil {
				errMsg := fmt.Sprintf("Ошибка gRPC на ноде %s: %v", node.NodeName, err)
				cfg.Logger.Error(errMsg)
				errors = append(errors, errMsg)
				continue
			}
			if resp.Error != "" {
				errMsg := fmt.Sprintf("Ошибка на ноде %s: %s", node.NodeName, resp.Error)
				cfg.Logger.Warn(errMsg)
				errors = append(errors, errMsg)
				continue
			}

			successNodes = append(successNodes, node.NodeName)
		}

		// Обновление статуса в БД для успешных нод
		if len(successNodes) > 0 {
			err := manager.ExecuteHighPriority(func(db *sql.DB) error {
				tx, err := db.Begin()
				if err != nil {
					return fmt.Errorf("не удалось начать транзакцию: %v", err)
				}
				defer tx.Rollback()

				enabledStr := "false"
				if req.Enabled {
					enabledStr = "true"
				}
				for _, nodeName := range successNodes {
					_, err := tx.Exec("UPDATE user_traffic SET enabled = ? WHERE node_name = ? AND user = ?", enabledStr, nodeName, req.User)
					if err != nil {
						return fmt.Errorf("не удалось обновить статус для %s на ноде %s: %v", req.User, nodeName, err)
					}
				}

				return tx.Commit()
			})
			if err != nil {
				cfg.Logger.Error("Ошибка обновления статуса в БД", "error", err)
				http.Error(w, fmt.Sprintf(`{"error": "Ошибка обновления статуса в БД: %v"}`, err), http.StatusInternalServerError)
				return
			}
		}

		// Формирование ответа
		resp := UserEnabledResponse{
			SuccessNodes: successNodes,
		}
		if len(errors) > 0 {
			resp.Errors = errors
			if len(successNodes) > 0 {
				// Частичный успех
				resp.Message = fmt.Sprintf("Частичный успех: обновлены ноды %v", successNodes)
				w.WriteHeader(http.StatusMultiStatus) // 207 Multi-Status
				cfg.Logger.Info("Partial success", "success_nodes", successNodes, "errors", errors)
			} else {
				// Полный провал
				resp.Message = fmt.Sprintf("Ошибки при обновлении статуса: %s", strings.Join(errors, "; "))
				cfg.Logger.Error(resp.Message)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			resp.Message = "Статус пользователя обновлён успешно"
			w.WriteHeader(http.StatusOK)
			cfg.Logger.Info("Статус пользователя обновлён успешно", "user", req.User, "enabled", req.Enabled, "nodes", targetNodes)
		}

		// Отправка JSON-ответа
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			cfg.Logger.Error("Ошибка сериализации ответа", "error", err)
			http.Error(w, `{"error": "Внутренняя ошибка сервера"}`, http.StatusInternalServerError)
			return
		}
	}
}
