package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/util"
)

// DnsStat represents DNS query statistics.
type DnsStat struct {
	Node   string
	User   string
	Count  int
	Domain string
}

// getDnsStats executes a query and returns formatted DNS statistics.
func getDnsStats(manager *manager.DatabaseManager, cfg *config.Config, node, user, domain, count string) (string, error) {
	// Проверка параметра count
	countInt, err := strconv.Atoi(count)
	if err != nil {
		cfg.Logger.Warn("Invalid count parameter", "count", count, "error", err)
		return "", fmt.Errorf("invalid count parameter: %v", err)
	}
	if countInt <= 0 {
		cfg.Logger.Warn("Count must be positive", "count", count)
		return "", fmt.Errorf("count must be positive: %s", count)
	}
	if countInt > 1000 {
		cfg.Logger.Warn("Count exceeds maximum limit", "count", count)
		return "", fmt.Errorf("count exceeds maximum limit: %s", count)
	}

	// Проверка параметра domain
	if domain != "" {
		if len(domain) > 255 {
			cfg.Logger.Warn("Domain parameter too long", "domain", domain)
			return "", fmt.Errorf("domain parameter too long: %s", domain)
		}
	}

	// Проверка валидности node, если указан
	if node != "" {
		var nodeExists bool
		err = manager.ExecuteLowPriority(func(db *sql.DB) error {
			err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE name = ?)", node).Scan(&nodeExists)
			if err != nil {
				cfg.Logger.Error("Failed to check node existence", "node", node, "error", err)
				return fmt.Errorf("failed to check node existence: %v", err)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if !nodeExists {
			cfg.Logger.Warn("Invalid node parameter", "node", node)
			return "", fmt.Errorf("invalid node parameter: %s", node)
		}
	}

	var statsBuilder strings.Builder
	statsBuilder.WriteString("➤  DNS Query Statistics:\n")

	err = manager.ExecuteLowPriority(func(db *sql.DB) error {
		query := `
			SELECT node_name AS "Node",
				   user AS "User",
				   count AS "Count",
				   domain AS "Domain"
				FROM user_dns
				WHERE 1=1`
		var args []any
		if node != "" {
			query += " AND node_name = ?"
			args = append(args, node)
		}
		if user != "" {
			query += " AND user = ?"
			args = append(args, user)
		}
		if domain != "" {
			query += " AND LOWER(domain) LIKE ?"
			args = append(args, "%"+domain+"%")
		}
		query += " ORDER BY count DESC LIMIT ?"
		args = append(args, countInt)

		cfg.Logger.Debug("Executing query on user_dns table", "node", node, "user", user, "domain", domain, "count", count, "query", query)
		rows, err := db.Query(query, args...)
		if err != nil {
			cfg.Logger.Error("Failed to execute SQL query", "node", node, "user", user, "domain", domain, "error", err)
			return fmt.Errorf("failed to execute SQL query: %v", err)
		}
		defer rows.Close()

		table, err := util.FormatTable(rows, nil, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to format DNS stats table", "error", err)
			return fmt.Errorf("failed to format DNS stats table: %v", err)
		}

		if table == "" {
			cfg.Logger.Warn("No DNS statistics found", "node", node, "user", user, "domain", domain)
			statsBuilder.WriteString("No DNS statistics available.\n")
		} else {
			statsBuilder.WriteString(table)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	return statsBuilder.String(), nil
}

// DnsStatsHandler handles HTTP requests for DNS statistics.
func DnsStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting DnsStatsHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		node := r.URL.Query().Get("node")
		user := r.URL.Query().Get("user")
		count := r.URL.Query().Get("count")
		domain := r.URL.Query().Get("domain")

		if count == "" {
			count = "20"
			cfg.Logger.Debug("Setting default count value", "count", count)
		}

		response, err := getDnsStats(manager, cfg, node, user, domain, count)
		if err != nil {
			cfg.Logger.Error("Error in DnsStatsHandler retrieving stats", "node", node, "user", user, "domain", domain, "error", err)
			http.Error(w, "Error processing data", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Writing response", "node", node, "user", user, "domain", domain, "response_length", len(response))
		fmt.Fprintln(w, response)
		cfg.Logger.Info("API dns_stats: completed successfully", "node", node, "user", user, "domain", domain, "count", count)
	}
}
