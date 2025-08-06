package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// SetEnabledHandler handles requests to toggle a user's enabled status.
func SetEnabledHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting SetEnabledHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodPatch {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use PATCH", http.StatusMethodNotAllowed)
			return
		}

		cfg.Logger.Debug("Parsing form data")
		if err := r.ParseForm(); err != nil {
			cfg.Logger.Error("Failed to parse form data", "error", err)
			http.Error(w, "Error parsing form data", http.StatusBadRequest)
			return
		}

		userIdentifier := r.FormValue("user")
		enabledStr := r.FormValue("enabled")
		cfg.Logger.Trace("Received form parameters", "user", userIdentifier, "enabled", enabledStr)

		if userIdentifier == "" {
			cfg.Logger.Warn("Empty user identifier")
			http.Error(w, "User field is required", http.StatusBadRequest)
			return
		}
		if len(userIdentifier) > 60 {
			cfg.Logger.Warn("User identifier too long", "length", len(userIdentifier))
			http.Error(w, "User identifier too long (max 60 characters)", http.StatusBadRequest)
			return
		}
		if enabledStr != "" && len(enabledStr) > 60 {
			cfg.Logger.Warn("Enabled value too long", "length", len(enabledStr))
			http.Error(w, "Enabled value too long (max 40 characters)", http.StatusBadRequest)
			return
		}

		var enabled bool
		if enabledStr == "" {
			enabled = true
			enabledStr = "true"
			cfg.Logger.Debug("Enabled not specified, set to true")
		} else {
			var err error
			enabled, err = strconv.ParseBool(enabledStr)
			if err != nil {
				cfg.Logger.Warn("Invalid enabled value", "enabled", enabledStr, "error", err)
				http.Error(w, "Enabled must be true or false", http.StatusBadRequest)
				return
			}
			cfg.Logger.Debug("Enabled value parsed successfully", "enabled", enabled)
		}

		cfg.Logger.Debug("Updating user status", "user", userIdentifier, "enabled", enabled)
		err := manager.ExecuteHighPriority(func(db1 *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for status update")
			tx, err := db1.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			// if err := db.ToggleUserEnabled(manager, cfg, userIdentifier, enabled); err != nil {
			// 	cfg.Logger.Error("Failed to toggle user status in configuration", "user", userIdentifier, "enabled", enabled, "error", err)
			// 	return fmt.Errorf("failed to toggle user status: %v", err)
			// }

			cfg.Logger.Debug("Executing status update query")
			result, err := tx.Exec("UPDATE clients_stats SET enabled = ? WHERE user = ?", enabledStr, userIdentifier)
			if err != nil {
				cfg.Logger.Error("Failed to update status in database", "user", userIdentifier, "enabled", enabledStr, "error", err)
				return fmt.Errorf("failed to update status for %s: %v", userIdentifier, err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
			if rowsAffected == 0 {
				cfg.Logger.Warn("User not found in database", "user", userIdentifier)
				return fmt.Errorf("user %s not found", userIdentifier)
			}

			cfg.Logger.Debug("Committing transaction")
			if err := tx.Commit(); err != nil {
				cfg.Logger.Error("Failed to commit transaction", "error", err)
				return fmt.Errorf("failed to commit transaction: %v", err)
			}

			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in SetEnabledHandler", "error", err)
			http.Error(w, "Error updating status", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API set_enabled: user status updated successfully", "user", userIdentifier, "enabled", enabled)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "User status updated successfully")
	}
}
