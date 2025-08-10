package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// updateIPLimit updates the IP limit for a user in the user_data table.
func updateIPLimit(manager *manager.DatabaseManager, cfg *config.Config, userIdentifier string, ipLimit int) (int64, error) {
	var rowsAffected int64
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Starting transaction for IP limit update")
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Executing IP limit update query for user", "user", userIdentifier)
		result, err := tx.Exec("UPDATE user_data SET lim_ip = ? WHERE user = ?", ipLimit, userIdentifier)
		if err != nil {
			cfg.Logger.Error("Failed to update lim_ip for user", "user", userIdentifier, "error", err)
			return fmt.Errorf("failed to update lim_ip for user %s: %v", userIdentifier, err)
		}

		rowsAffected, err = result.RowsAffected()
		if err != nil {
			cfg.Logger.Error("Failed to get affected rows", "user", userIdentifier, "error", err)
			return fmt.Errorf("failed to get affected rows for user %s: %v", userIdentifier, err)
		}
		if rowsAffected == 0 {
			cfg.Logger.Warn("No rows affected during update", "user", userIdentifier)
			return fmt.Errorf("user '%s' not found", userIdentifier)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}

		cfg.Logger.Debug("IP limit updated successfully", "user", userIdentifier, "lim_ip", ipLimit, "rows_affected", rowsAffected)
		return nil
	})
	return rowsAffected, err
}

// UpdateIPLimitHandler handles HTTP requests to update the IP limit for a user in the user_data table.
func UpdateIPLimitHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting UpdateIPLimitHandler request processing")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodPatch {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid method. Use PATCH"})
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Парсим JSON-тело запроса
		var requestBody struct {
			User  string `json:"user"`
			LimIP *int   `json:"lim_ip"` // Используем указатель для обработки null
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			cfg.Logger.Error("Failed to parse JSON body", "error", err)
			json.NewEncoder(w).Encode(map[string]string{"error": "Error parsing JSON body"})
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		userIdentifier := requestBody.User
		var ipLimitInt int
		if requestBody.LimIP != nil {
			ipLimitInt = *requestBody.LimIP
		} else {
			ipLimitInt = 0
			cfg.Logger.Debug("lim_ip not specified in JSON, set to 0")
		}

		cfg.Logger.Trace("Received parameters", "user", userIdentifier, "lim_ip", ipLimitInt)

		// Валидация параметров
		if userIdentifier == "" {
			cfg.Logger.Warn("Empty user identifier")
			json.NewEncoder(w).Encode(map[string]string{"error": "User field is required"})
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(userIdentifier) > 40 {
			cfg.Logger.Warn("User identifier too long", "length", len(userIdentifier))
			json.NewEncoder(w).Encode(map[string]string{"error": "User identifier too long (max 40 characters)"})
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if ipLimitInt < 0 || ipLimitInt > 100 {
			cfg.Logger.Warn("Invalid lim_ip value", "lim_ip", ipLimitInt)
			json.NewEncoder(w).Encode(map[string]string{"error": "lim_ip must be between 0 and 100"})
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		rowsAffected, err := updateIPLimit(manager, cfg, userIdentifier, ipLimitInt)
		if err != nil {
			cfg.Logger.Error("Error in UpdateIPLimitHandler", "error", err)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to update IP limit: %v", err)})
			w.WriteHeader(http.StatusNotFound)
			return
		}

		cfg.Logger.Info("API update_ip_limit: IP limit update request completed successfully", "user", userIdentifier, "lim_ip", ipLimitInt, "rows_affected", rowsAffected)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":       "IP limit updated successfully",
			"rows_affected": rowsAffected,
			"user":          userIdentifier,
			"lim_ip":        ipLimitInt,
		})
	}
}
