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

// resetTrafficStats resets traffic statistics in the bound_traffic table, optionally filtered by node names.
func resetTrafficStats(manager *manager.DatabaseManager, cfg *config.Config, nodes []string) (int64, error) {
	var rowsAffected int64
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Starting transaction for resetting traffic stats")
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		if len(nodes) == 0 {
			// Сбрасываем uplink и downlink для всех записей
			cfg.Logger.Debug("Executing reset query for all bound_traffic records")
			result, err := tx.Exec("UPDATE bound_traffic SET uplink = 0, downlink = 0")
			if err != nil {
				cfg.Logger.Error("Failed to reset traffic stats in bound_traffic", "error", err)
				return fmt.Errorf("failed to reset traffic stats in bound_traffic: %v", err)
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
			query := fmt.Sprintf("UPDATE bound_traffic SET uplink = 0, downlink = 0 WHERE node_name IN (%s)", strings.Join(placeholders, ","))
			cfg.Logger.Debug("Executing reset query for bound_traffic with nodes", "nodes", nodes)
			result, err := tx.Exec(query, args...)
			if err != nil {
				cfg.Logger.Error("Failed to reset traffic stats in bound_traffic for nodes", "nodes", nodes, "error", err)
				return fmt.Errorf("failed to reset traffic stats in bound_traffic for nodes %v: %v", nodes, err)
			}
			rowsAffected, err = result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
		}

		if rowsAffected == 0 {
			cfg.Logger.Warn("No rows affected during reset", "table", "bound_traffic", "nodes", nodes)
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

// ResetTrafficStatsHandler handles HTTP requests to reset traffic statistics in the bound_traffic table.
func ResetTrafficStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting ResetTrafficStatsHandler request processing")

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

		cfg.Logger.Trace("Request to reset traffic stats", "remote_addr", r.RemoteAddr, "nodes", nodes)

		rowsAffected, err := resetTrafficStats(manager, cfg, nodes)
		if err != nil {
			cfg.Logger.Error("Error in ResetTrafficStatsHandler", "error", err)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to reset traffic stats: %v", err)})
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API reset_traffic_stats: traffic stats reset request completed successfully", "remote_addr", r.RemoteAddr, "rows_affected", rowsAffected, "nodes", nodes)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":       "Traffic stats reset successfully",
			"rows_affected": rowsAffected,
		})
	}
}
