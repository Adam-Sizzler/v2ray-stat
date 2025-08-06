package api

// import (
// 	"fmt"
// 	"net/http"
// 	"v2ray-stat/backend/config"
// 	"v2ray-stat/backend/db/manager"
// )

// // AdjustDateOffsetHandler handles requests to update the subscription date.
// func AdjustDateOffsetHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		cfg.Logger.Debug("Starting AdjustDateOffsetHandler request processing")

// 		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

// 		if r.Method != http.MethodPatch {
// 			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
// 			http.Error(w, "Invalid method. Use PATCH", http.StatusMethodNotAllowed)
// 			return
// 		}

// 		cfg.Logger.Debug("Parsing form data")
// 		if err := r.ParseForm(); err != nil {
// 			cfg.Logger.Error("Failed to parse form data", "error", err)
// 			http.Error(w, "Error parsing form data", http.StatusBadRequest)
// 			return
// 		}

// 		userIdentifier := r.FormValue("user")
// 		subEnd := r.FormValue("sub_end")
// 		cfg.Logger.Trace("Received form parameters", "user", userIdentifier, "sub_end", subEnd)

// 		if userIdentifier == "" || subEnd == "" {
// 			cfg.Logger.Warn("Missing or empty user or sub_end parameters")
// 			http.Error(w, "user and sub_end are required", http.StatusBadRequest)
// 			return
// 		}

// 		err := updateSubscriptionDate(manager, cfg, userIdentifier, subEnd)
// 		if err != nil {
// 			cfg.Logger.Error("Failed to update subscription for user", "user", userIdentifier, "error", err)
// 			http.Error(w, err.Error(), http.StatusInternalServerError)
// 			return
// 		}

// 		cfg.Logger.Debug("Writing response", "user", userIdentifier, "sub_end", subEnd)
// 		w.WriteHeader(http.StatusOK)
// 		_, err = fmt.Fprintf(w, "Subscription date for %s updated with sub_end %s\n", userIdentifier, subEnd)
// 		if err != nil {
// 			cfg.Logger.Error("Failed to write response for user", "user", userIdentifier, "error", err)
// 			http.Error(w, "Error sending response", http.StatusInternalServerError)
// 			return
// 		}

// 		cfg.Logger.Info("API adjust_date: subscription date update completed successfully", "user", userIdentifier)
// 	}
// }
