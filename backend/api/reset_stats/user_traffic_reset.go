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

// resetClientsStats resets traffic statistics in the user_traffic table, optionally filtered by node names.
func resetClientsStats(manager *manager.DatabaseManager, cfg *config.Config, nodes []string) (int64, error) {
	var rowsAffected int64
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Starting transaction for resetting client stats")
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		if len(nodes) == 0 {
			// Сбрасываем uplink и downlink для всех записей
			cfg.Logger.Debug("Executing reset query for all user_traffic records")
			result, err := tx.Exec("UPDATE user_traffic SET uplink = 0, downlink = 0")
			if err != nil {
				cfg.Logger.Error("Failed to reset client traffic stats in user_traffic", "error", err)
				return fmt.Errorf("failed to reset client traffic stats in user_traffic: %v", err)
			}
			rowsAffected, err = result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
		} else {
			// Сбрасываем uplink и downlink для указанных нод
			placeholders := make([]string, len(nodes))
			args := make([]any, len(nodes))
			for i, node := range nodes {
				placeholders[i] = "?"
				args[i] = node
			}
			query := fmt.Sprintf("UPDATE user_traffic SET uplink = 0, downlink = 0 WHERE node_name IN (%s)", strings.Join(placeholders, ","))
			cfg.Logger.Debug("Executing reset query for user_traffic with nodes", "nodes", nodes)
			result, err := tx.Exec(query, args...)
			if err != nil {
				cfg.Logger.Error("Failed to reset client traffic stats in user_traffic for nodes", "nodes", nodes, "error", err)
				return fmt.Errorf("failed to reset client traffic stats in user_traffic for nodes %v: %v", nodes, err)
			}
			rowsAffected, err = result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
		}

		if rowsAffected == 0 {
			cfg.Logger.Warn("No rows affected during reset", "table", "user_traffic", "nodes", nodes)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}

		return nil
	})
	return rowsAffected, err
}

// ResetClientsStatsHandler handles HTTP requests to reset client traffic statistics in the user_traffic table.
func ResetClientsStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting ResetClientsStatsHandler request processing")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid method. Use POST"})
			w.WriteHeader(http.StatusMethodNotAllowed)
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

		cfg.Logger.Trace("Request to reset client traffic stats", "remote_addr", r.RemoteAddr, "nodes", nodes)

		rowsAffected, err := resetClientsStats(manager, cfg, nodes)
		if err != nil {
			cfg.Logger.Error("Error in ResetClientsStatsHandler", "error", err)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to reset client traffic stats: %v", err)})
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API reset_clients_stats: client traffic stats reset request completed successfully", "remote_addr", r.RemoteAddr, "rows_affected", rowsAffected, "nodes", nodes)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":       "Client traffic stats reset successfully",
			"rows_affected": rowsAffected,
		})
	}
}
