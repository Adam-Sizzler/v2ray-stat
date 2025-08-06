package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// UpdateIPLimitHandler updates the IP limit for a user in the user_data table.
func UpdateIPLimitHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting UpdateIPLimitHandler request processing")

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
		ipLimit := r.FormValue("lim_ip")
		cfg.Logger.Trace("Received form parameters", "user", userIdentifier, "lim_ip", ipLimit)

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
		if ipLimit != "" && len(ipLimit) > 10 {
			cfg.Logger.Warn("lim_ip value too long", "length", len(ipLimit))
			http.Error(w, "lim_ip value too long (max 60 characters)", http.StatusBadRequest)
			return
		}

		var ipLimitInt int
		if ipLimit == "" {
			ipLimitInt = 0
			cfg.Logger.Debug("lim_ip not specified, set to 0")
		} else {
			var err error
			ipLimitInt, err = strconv.Atoi(ipLimit)
			if err != nil {
				cfg.Logger.Warn("Invalid lim_ip value", "lim_ip", ipLimit, "error", err)
				http.Error(w, "lim_ip must be a number", http.StatusBadRequest)
				return
			}
			if ipLimitInt < 0 || ipLimitInt > 100 {
				cfg.Logger.Warn("Invalid lim_ip value", "lim_ip", ipLimitInt)
				http.Error(w, "lim_ip must be between 0 and 100", http.StatusBadRequest)
				return
			}
		}

		cfg.Logger.Debug("Updating IP limit for user", "user", userIdentifier, "lim_ip", ipLimitInt)
		err := manager.ExecuteHighPriority(func(db1 *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for IP limit update")
			tx, err := db1.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			cfg.Logger.Debug("Executing IP limit update query")
			result, err := tx.Exec("UPDATE user_data SET lim_ip = ? WHERE user = ?", ipLimitInt, userIdentifier)
			if err != nil {
				cfg.Logger.Error("Failed to update lim_ip for user", "user", userIdentifier, "error", err)
				return fmt.Errorf("failed to update lim_ip for user %s: %v", userIdentifier, err)
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

			cfg.Logger.Debug("IP limit updated successfully", "user", userIdentifier, "lim_ip", ipLimitInt)
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error in UpdateIPLimitHandler", "error", err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		cfg.Logger.Info("API update_lim_ip: IP limit update request completed successfully", "user", userIdentifier, "lim_ip", ipLimitInt)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "IP limit updated successfully")
	}
}
