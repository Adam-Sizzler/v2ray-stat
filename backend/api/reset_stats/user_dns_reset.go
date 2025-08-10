package reset_stats

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// DeleteDNSStatsHandler deletes records from the user_dns table, optionally filtered by node names.
func DeleteDNSStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting DeleteDNSStatsHandler request processing")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, `{"error": "Invalid method. Use POST"}`, http.StatusMethodNotAllowed)
			return
		}

		// Получаем параметр nodes из URL
		nodesParam := r.URL.Query().Get("nodes")
		var nodes []string
		if nodesParam != "" {
			nodes = strings.Split(nodesParam, ",")
			for i, node := range nodes {
				nodes[i] = strings.TrimSpace(node)
			}
		}

		cfg.Logger.Trace("Request to delete DNS stats records", "remote_addr", r.RemoteAddr, "nodes", nodes)

		var rowsAffected int64
		err := manager.ExecuteHighPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for deleting records")
			tx, err := db.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			if len(nodes) == 0 {
				// Удаляем все записи, если nodes не указаны
				cfg.Logger.Debug("Executing delete query for all user_dns records")
				result, err := tx.Exec("DELETE FROM user_dns")
				if err != nil {
					cfg.Logger.Error("Failed to delete records from user_dns", "error", err)
					return fmt.Errorf("failed to delete records from user_dns: %v", err)
				}
				rowsAffected, err = result.RowsAffected()
				if err != nil {
					cfg.Logger.Error("Failed to get affected rows", "error", err)
					return fmt.Errorf("failed to get affected rows: %v", err)
				}
			} else {
				// Удаляем записи для указанных нод
				placeholders := make([]string, len(nodes))
				args := make([]any, len(nodes))
				for i, node := range nodes {
					placeholders[i] = "?"
					args[i] = node
				}
				query := fmt.Sprintf("DELETE FROM user_dns WHERE node_name IN (%s)", strings.Join(placeholders, ","))
				cfg.Logger.Debug("Executing delete query for user_dns with nodes", "nodes", nodes)
				result, err := tx.Exec(query, args...)
				if err != nil {
					cfg.Logger.Error("Failed to delete records from user_dns for nodes", "nodes", nodes, "error", err)
					return fmt.Errorf("failed to delete records from user_dns for nodes %v: %v", nodes, err)
				}
				rowsAffected, err = result.RowsAffected()
				if err != nil {
					cfg.Logger.Error("Failed to get affected rows", "error", err)
					return fmt.Errorf("failed to get affected rows: %v", err)
				}
			}

			if rowsAffected == 0 {
				cfg.Logger.Warn("No rows affected during deletion", "table", "user_dns", "nodes", nodes)
			}

			cfg.Logger.Debug("Committing transaction")
			if err := tx.Commit(); err != nil {
				cfg.Logger.Error("Failed to commit transaction", "error", err)
				return fmt.Errorf("failed to commit transaction: %v", err)
			}

			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in DeleteDNSStatsHandler", "error", err)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to delete DNS stats records: %v", err)})
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API delete_user_dns: DNS stats deletion request completed successfully", "remote_addr", r.RemoteAddr, "rows_affected", rowsAffected, "nodes", nodes)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":       "DNS stats records deleted successfully",
			"rows_affected": rowsAffected,
		})
	}
}
