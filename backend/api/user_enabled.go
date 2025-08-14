package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// UserEnabledHandler обрабатывает запросы на включение/выключение пользователя на указанных нодах.
func UserEnabledHandler(manager *manager.DatabaseManager, cfg *config.Config, nodeClients []*db.NodeClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Начало обработки запроса UserEnabledHandler")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodPatch {
			cfg.Logger.Warn("Неверный метод HTTP", "method", r.Method)
			http.Error(w, "Неверный метод. Используйте PATCH", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			cfg.Logger.Error("Ошибка разбора формы", "error", err)
			http.Error(w, "Ошибка разбора формы", http.StatusBadRequest)
			return
		}

		userIdentifier := r.FormValue("user")
		enabledStr := r.FormValue("enabled")
		nodesStr := r.FormValue("nodes")
		cfg.Logger.Debug("Получены параметры формы", "user", userIdentifier, "enabled", enabledStr, "nodes", nodesStr) // Изменен уровень на Debug для безопасности

		if userIdentifier == "" {
			cfg.Logger.Warn("Пустой идентификатор пользователя")
			http.Error(w, "Поле user обязательно", http.StatusBadRequest)
			return
		}
		if len(userIdentifier) > 40 {
			cfg.Logger.Warn("Идентификатор пользователя слишком длинный", "length", len(userIdentifier))
			http.Error(w, "Идентификатор пользователя слишком длинный (макс. 40 символов)", http.StatusBadRequest)
			return
		}
		if enabledStr == "" {
			enabledStr = "true"
			cfg.Logger.Debug("Enabled не указан, устанавливаем true")
		} else if len(enabledStr) > 40 {
			cfg.Logger.Warn("Значение enabled слишком длинное", "length", len(enabledStr))
			http.Error(w, "Значение enabled слишком длинное (макс. 40 символов)", http.StatusBadRequest)
			return
		}

		enabled, err := strconv.ParseBool(enabledStr)
		if err != nil {
			cfg.Logger.Warn("Неверное значение enabled", "enabled", enabledStr, "error", err)
			http.Error(w, "Enabled должно быть true или false", http.StatusBadRequest)
			return
		}

		// Проверка существования пользователя в user_traffic
		err = manager.ExecuteHighPriority(func(db *sql.DB) error {
			var count int
			err := db.QueryRow("SELECT COUNT(*) FROM user_traffic WHERE user = ?", userIdentifier).Scan(&count)
			if err != nil {
				return fmt.Errorf("failed to check user existence: %v", err)
			}
			if count == 0 {
				return fmt.Errorf("user %s not found in user_traffic", userIdentifier)
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Warn("User check failed", "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var targetNodes []string
		if nodesStr == "" {
			for _, node := range cfg.V2rayStat.Nodes {
				targetNodes = append(targetNodes, node.NodeName)
			}
			cfg.Logger.Debug("Nodes не указаны, применяем ко всем нодам", "nodes", targetNodes)
		} else {
			targetNodes = strings.Split(nodesStr, ",")
			// Проверка существования указанных нод
			for _, nodeName := range targetNodes {
				found := false
				for _, node := range cfg.V2rayStat.Nodes {
					if node.NodeName == nodeName {
						found = true
						break
					}
				}
				if !found {
					cfg.Logger.Warn("Node not found in config", "node_name", nodeName)
					http.Error(w, fmt.Sprintf("Нода %s не найдена", nodeName), http.StatusBadRequest)
					return
				}
			}
			cfg.Logger.Debug("Указаны nodes", "nodes", targetNodes)
		}

		var errors []string
		successNodes := make([]string, 0)

		for _, nodeName := range targetNodes {
			var nodeClient *db.NodeClient
			for _, nc := range nodeClients {
				if nc.NodeName == nodeName {
					nodeClient = nc
					break
				}
			}
			if nodeClient == nil {
				errMsg := fmt.Sprintf("Нода %s не найдена", nodeName)
				cfg.Logger.Warn(errMsg)
				errors = append(errors, errMsg)
				continue
			}

			grpcCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := nodeClient.Client.SetEnabled(grpcCtx, &proto.SetEnabledRequest{User: userIdentifier, Enabled: enabled})
			if err != nil {
				errMsg := fmt.Sprintf("Ошибка gRPC на ноде %s: %v", nodeName, err)
				cfg.Logger.Error(errMsg)
				errors = append(errors, errMsg)
				continue
			}
			if resp.Error != "" {
				errMsg := fmt.Sprintf("Ошибка на ноде %s: %s", nodeName, resp.Error)
				cfg.Logger.Warn(errMsg)
				errors = append(errors, errMsg)
				continue
			}

			successNodes = append(successNodes, nodeName)
		}

		if len(successNodes) > 0 {
			err := manager.ExecuteHighPriority(func(db *sql.DB) error {
				tx, err := db.Begin()
				if err != nil {
					return fmt.Errorf("не удалось начать транзакцию: %v", err)
				}
				defer tx.Rollback()

				for _, nodeName := range successNodes {
					_, err := tx.Exec("UPDATE user_traffic SET enabled = ? WHERE node_name = ? AND user = ?", enabledStr, nodeName, userIdentifier)
					if err != nil {
						return fmt.Errorf("не удалось обновить статус для %s на ноде %s: %v", userIdentifier, nodeName, err)
					}
				}

				return tx.Commit()
			})
			if err != nil {
				cfg.Logger.Error("Ошибка обновления статуса в БД", "error", err)
				http.Error(w, "Ошибка обновления статуса в БД", http.StatusInternalServerError)
				return
			}
		}

		if len(errors) > 0 {
			if len(successNodes) > 0 {
				// Частичный успех
				w.WriteHeader(http.StatusMultiStatus) // Используем 207 Multi-Status
				fmt.Fprintf(w, "Частичный успех: обновлены ноды %v; ошибки: %s", successNodes, strings.Join(errors, "; "))
				cfg.Logger.Info("Partial success", "success_nodes", successNodes, "errors", errors)
				return
			}
			// Полный провал
			errMsg := fmt.Sprintf("Ошибки при обновлении статуса: %s", strings.Join(errors, "; "))
			cfg.Logger.Error(errMsg)
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Статус пользователя обновлён успешно", "user", userIdentifier, "enabled", enabled, "nodes", targetNodes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Статус пользователя обновлён успешно")
	}
}
