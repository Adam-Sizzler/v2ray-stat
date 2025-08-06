package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// UpdateRenewHandler updates the renew value for a user in the user_data table.
func UpdateRenewHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting UpdateRenewHandler request processing")

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
		renewStr := r.FormValue("renew")
		cfg.Logger.Trace("Received form parameters", "user", userIdentifier, "renew", renewStr)

		if userIdentifier == "" {
			cfg.Logger.Warn("Empty user identifier")
			http.Error(w, "User field is required", http.StatusBadRequest)
			return
		}
		if len(userIdentifier) > 40 {
			cfg.Logger.Warn("User identifier too long", "length", len(userIdentifier))
			http.Error(w, "User identifier too long (max 40 characters)", http.StatusBadRequest)
			return
		}
		if renewStr != "" && len(renewStr) > 40 {
			cfg.Logger.Warn("Renew value too long", "length", len(renewStr))
			http.Error(w, "Renew value too long (max 40 characters)", http.StatusBadRequest)
			return
		}

		var renew int
		if renewStr == "" {
			renew = 0
			cfg.Logger.Debug("Renew not specified, set to 0")
		} else {
			var err error
			renew, err = strconv.Atoi(renewStr)
			if err != nil {
				cfg.Logger.Warn("Invalid renew value", "renew", renewStr, "error", err)
				http.Error(w, "Renew must be an integer", http.StatusBadRequest)
				return
			}
			if renew < 0 {
				cfg.Logger.Warn("Invalid renew value", "renew", renew)
				http.Error(w, "Renew cannot be negative", http.StatusBadRequest)
				return
			}
		}

		cfg.Logger.Debug("Updating renew value for user", "user", userIdentifier, "renew", renew)
		err := manager.ExecuteHighPriority(func(db1 *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for renew update")
			tx, err := db1.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			cfg.Logger.Debug("Executing renew update query")
			result, err := tx.Exec("UPDATE user_data SET renew = ? WHERE user = ?", renew, userIdentifier)
			if err != nil {
				cfg.Logger.Error("Failed to update renew for user", "user", userIdentifier, "error", err)
				return fmt.Errorf("failed to update renew for user %s: %v", userIdentifier, err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "user", userIdentifier, "error", err)
				return fmt.Errorf("failed to get affected rows for user %s: %v", userIdentifier, err)
			}
			if rowsAffected == 0 {
				cfg.Logger.Warn("User not found", "user", userIdentifier)
				return fmt.Errorf("user '%s' not found", userIdentifier)
			}

			cfg.Logger.Debug("Committing transaction")
			if err := tx.Commit(); err != nil {
				cfg.Logger.Error("Failed to commit transaction", "error", err)
				return fmt.Errorf("failed to commit transaction: %v", err)
			}

			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in UpdateRenewHandler", "error", err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		cfg.Logger.Info("API update_renew: renew update request completed successfully", "user", userIdentifier, "renew", renew)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Renew value updated successfully")
	}
}
