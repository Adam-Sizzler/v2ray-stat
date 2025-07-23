package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"v2ray-stat/config"
	"v2ray-stat/constant"
	"v2ray-stat/db"
	"v2ray-stat/db/manager"
	"v2ray-stat/lua"
	"v2ray-stat/stats"
	"v2ray-stat/util"
)

// User represents a user entity from the clients_stats table.
type User struct {
	User          string `json:"user"`
	Uuid          string `json:"uuid"`
	Rate          string `json:"rate"`
	Enabled       string `json:"enabled"`
	Created       string `json:"created"`
	Sub_end       string `json:"sub_end"`
	Renew         int    `json:"renew"`
	Lim_ip        int    `json:"lim_ip"`
	Ips           string `json:"ips"`
	Uplink        int64  `json:"uplink"`
	Downlink      int64  `json:"downlink"`
	Sess_uplink   int64  `json:"sess_uplink"`
	Sess_downlink int64  `json:"sess_downlink"`
}

// UsersHandler returns a list of users from the database in JSON format.
func UsersHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting UsersHandler request processing")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		var users []User
		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing query on clients_stats table")
			rows, err := db.Query("SELECT user, uuid, rate, enabled, created, sub_end, renew, lim_ip, ips, uplink, downlink, sess_uplink, sess_downlink FROM clients_stats")
			if err != nil {
				cfg.Logger.Error("Failed to execute SQL query", "error", err)
				return fmt.Errorf("failed to execute SQL query: %v", err)
			}
			defer rows.Close()

			for rows.Next() {
				var user User
				if err := rows.Scan(&user.User, &user.Uuid, &user.Rate, &user.Enabled, &user.Created, &user.Sub_end, &user.Renew, &user.Lim_ip, &user.Ips, &user.Uplink, &user.Downlink, &user.Sess_uplink, &user.Sess_downlink); err != nil {
					cfg.Logger.Error("Failed to scan row", "error", err)
					return fmt.Errorf("failed to scan row: %v", err)
				}
				cfg.Logger.Trace("Read user", "user", user.User, "uuid", user.Uuid, "enabled", user.Enabled)
				users = append(users, user)
			}
			if err := rows.Err(); err != nil {
				cfg.Logger.Error("Error iterating rows", "error", err)
				return fmt.Errorf("error iterating rows: %v", err)
			}

			if len(users) == 0 {
				cfg.Logger.Warn("No users found in clients_stats table")
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in UsersHandler", "error", err)
			http.Error(w, "Error processing data", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Encoding response to JSON", "users_count", len(users))
		if err := json.NewEncoder(w).Encode(users); err != nil {
			cfg.Logger.Error("Failed to encode JSON", "error", err)
			http.Error(w, "Error forming response", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API users: completed successfully", "users_count", len(users))
	}
}

// contains checks if an item exists in a slice.
func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

// appendStats appends content to a strings.Builder.
func appendStats(builder *strings.Builder, content string) {
	builder.WriteString(content)
}

// formatTable formats SQL query results into a table.
func formatTable(rows *sql.Rows, trafficColumns []string, cfg *config.Config) (string, error) {
	columns, err := rows.Columns()
	if err != nil {
		cfg.Logger.Error("Failed to get column names", "error", err)
		return "", fmt.Errorf("failed to get column names: %v", err)
	}

	maxWidths := make([]int, len(columns))
	for i, col := range columns {
		maxWidths[i] = len(col)
	}

	var data [][]string
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			cfg.Logger.Error("Failed to scan row", "error", err)
			return "", fmt.Errorf("failed to scan row: %v", err)
		}

		row := make([]string, len(columns))
		for i, val := range values {
			strVal := fmt.Sprintf("%v", val)
			if len(strVal) > 255 {
				cfg.Logger.Warn("Value too long in column", "column", columns[i], "length", len(strVal))
				strVal = strVal[:255]
			}
			if contains(trafficColumns, columns[i]) {
				if numVal, ok := val.(int64); ok {
					unit := "byte"
					if columns[i] == "Rate" {
						unit = "bps"
					}
					strVal = util.FormatData(float64(numVal), unit)
				}
			}
			row[i] = strVal
			if len(strVal) > maxWidths[i] {
				maxWidths[i] = len(strVal)
			}
		}
		data = append(data, row)
	}

	if len(data) == 0 {
		cfg.Logger.Warn("SQL query result is empty")
	}

	var header strings.Builder
	for i, col := range columns {
		header.WriteString(fmt.Sprintf("%-*s", maxWidths[i]+2, col))
	}
	header.WriteString("\n")

	var separator strings.Builder
	for _, width := range maxWidths {
		separator.WriteString(strings.Repeat("-", width) + "  ")
	}
	separator.WriteString("\n")

	var table strings.Builder
	table.WriteString(header.String())
	table.WriteString(separator.String())
	for _, row := range data {
		for i, val := range row {
			if contains(trafficColumns, columns[i]) {
				table.WriteString(fmt.Sprintf("%*s  ", maxWidths[i], val))
			} else {
				table.WriteString(fmt.Sprintf("%-*s", maxWidths[i]+2, val))
			}
		}
		table.WriteString("\n")
	}

	return table.String(), nil
}

// buildServerStateStats collects server state statistics.
func buildServerStateStats(builder *strings.Builder, cfg *config.Config) {
	cfg.Logger.Debug("Collecting server state statistics")
	appendStats(builder, "➤  Server State:\n")
	appendStats(builder, fmt.Sprintf("%-13s %s\n", "Uptime:", stats.GetUptime(cfg)))
	appendStats(builder, fmt.Sprintf("%-13s %s\n", "Load average:", stats.GetLoadAverage(cfg)))
	appendStats(builder, fmt.Sprintf("%-13s %s\n", "Memory:", stats.GetMemoryUsage(cfg)))
	appendStats(builder, fmt.Sprintf("%-13s %s\n", "Disk usage:", stats.GetDiskUsage(cfg)))
	appendStats(builder, fmt.Sprintf("%-13s %s\n", "Status:", stats.GetStatus(cfg)))
	appendStats(builder, "\n")
}

// buildNetworkStats collects network statistics.
func buildNetworkStats(builder *strings.Builder, cfg *config.Config) {
	trafficMonitor := stats.GetTrafficMonitor()
	if trafficMonitor != nil {
		rxSpeed, txSpeed, rxPacketsPerSec, txPacketsPerSec, totalRxBytes, totalTxBytes := trafficMonitor.GetStats()
		appendStats(builder, fmt.Sprintf("➤  Network (%s):\n", trafficMonitor.Iface))
		appendStats(builder, fmt.Sprintf("rx: %s   %.0f p/s   %s\n", util.FormatData(float64(rxSpeed), "bps"), rxPacketsPerSec, util.FormatData(float64(totalRxBytes), "byte")))
		appendStats(builder, fmt.Sprintf("tx: %s   %.0f p/s   %s\n\n", util.FormatData(float64(txSpeed), "bps"), txPacketsPerSec, util.FormatData(float64(totalTxBytes), "byte")))
	} else {
		cfg.Logger.Warn("Traffic monitor not initialized")
	}
}

// buildServerCustomStats collects custom server statistics.
func buildServerCustomStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Collecting custom server statistics")
	serverColumnAliases := map[string]string{
		"source":        "Source",
		"rate":          "Rate",
		"uplink":        "Uplink",
		"downlink":      "Downlink",
		"sess_uplink":   "Sess Up",
		"sess_downlink": "Sess Down",
	}
	trafficAliases := []string{
		"Rate",
		"Uplink",
		"Downlink",
		"Sess Up",
		"Sess Down",
	}

	if len(cfg.StatsColumns.Server.Columns) > 0 {
		var serverCols []string
		for _, col := range cfg.StatsColumns.Server.Columns {
			if alias, ok := serverColumnAliases[col]; ok {
				serverCols = append(serverCols, fmt.Sprintf("%s AS \"%s\"", col, alias))
			}
		}
		serverQuery := fmt.Sprintf("SELECT %s FROM traffic_stats ORDER BY %s %s;",
			strings.Join(serverCols, ", "), cfg.StatsColumns.Server.SortBy, cfg.StatsColumns.Server.SortOrder)

		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing server custom stats query", "query", serverQuery)
			rows, err := db.Query(serverQuery)
			if err != nil {
				cfg.Logger.Error("Failed to execute server stats query", "error", err)
				return fmt.Errorf("failed to execute server stats query: %v", err)
			}
			defer rows.Close()

			appendStats(builder, "➤  Server Statistics:\n")
			serverTable, err := formatTable(rows, trafficAliases, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to format server stats table", "error", err)
				return fmt.Errorf("failed to format server stats table: %v", err)
			}
			appendStats(builder, serverTable)
			appendStats(builder, "\n")
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing server custom stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for server stats in configuration")
	}
	return nil
}

// buildClientCustomStats collects custom client statistics.
func buildClientCustomStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, sortBy, sortOrder string) error {
	cfg.Logger.Debug("Collecting custom client statistics")

	clientColumnAliases := map[string]string{
		"user":          "User",
		"uuid":          "ID",
		"last_seen":     "Last seen",
		"rate":          "Rate",
		"uplink":        "Uplink",
		"downlink":      "Downlink",
		"sess_uplink":   "Sess Up",
		"sess_downlink": "Sess Down",
		"enabled":       "Enabled",
		"sub_end":       "Sub end",
		"renew":         "Renew",
		"lim_ip":        "Lim",
		"ips":           "Ips",
		"created":       "Created",
	}
	clientAliases := []string{
		"Rate",
		"Uplink",
		"Downlink",
		"Sess Up",
		"Sess Down",
	}

	if len(cfg.StatsColumns.Client.Columns) > 0 {
		var clientCols []string
		for _, col := range cfg.StatsColumns.Client.Columns {
			if alias, ok := clientColumnAliases[col]; ok {
				clientCols = append(clientCols, fmt.Sprintf("%s AS \"%s\"", col, alias))
			}
		}

		clientSortBy := cfg.StatsColumns.Client.SortBy
		if sortBy != "" {
			clientSortBy = sortBy
		}

		clientSortOrder := cfg.StatsColumns.Client.SortOrder
		if sortOrder != "" {
			clientSortOrder = sortOrder
		}

		clientQuery := fmt.Sprintf("SELECT %s FROM clients_stats ORDER BY %s %s;",
			strings.Join(clientCols, ", "), clientSortBy, clientSortOrder)

		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing client stats query", "query", clientQuery)
			rows, err := db.Query(clientQuery)
			if err != nil {
				cfg.Logger.Error("Failed to execute client stats query", "error", err)
				return fmt.Errorf("failed to execute client stats query: %v", err)
			}
			defer rows.Close()

			appendStats(builder, "➤  Client Statistics:\n")
			clientTable, err := formatTable(rows, clientAliases, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to format client stats table", "error", err)
				return fmt.Errorf("failed to format client stats table: %v", err)
			}
			appendStats(builder, clientTable)
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing client stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for client stats in configuration")
	}
	return nil
}

// StatsCustomHandler handles requests to /api/v1/stats_custom.
func StatsCustomHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting StatsCustomHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		sortBy := r.URL.Query().Get("sort_by")
		validSortColumns := []string{"user", "uuid", "last_seen", "rate", "sess_uplink", "sess_downlink", "uplink", "downlink", "enabled", "sub_end", "renew", "lim_ip", "ips", "created"}
		if sortBy != "" && !contains(validSortColumns, sortBy) {
			cfg.Logger.Warn("Invalid sort_by parameter", "sort_by", sortBy)
			http.Error(w, fmt.Sprintf("Invalid sort_by parameter: %s, must be one of %v", sortBy, validSortColumns), http.StatusBadRequest)
			return
		}

		sortOrder := r.URL.Query().Get("sort_order")
		if sortOrder != "" && sortOrder != "ASC" && sortOrder != "DESC" {
			cfg.Logger.Warn("Invalid sort_order parameter", "sort_order", sortOrder)
			http.Error(w, fmt.Sprintf("Invalid sort_order parameter: %s, must be ASC or DESC", sortOrder), http.StatusBadRequest)
			return
		}

		var statsBuilder strings.Builder

		if cfg.Features["system_monitoring"] {
			cfg.Logger.Debug("Collecting system statistics")
			buildServerStateStats(&statsBuilder, cfg)
		}
		if cfg.Features["network"] {
			cfg.Logger.Debug("Collecting network statistics")
			buildNetworkStats(&statsBuilder, cfg)
		}

		if err := buildServerCustomStats(&statsBuilder, manager, cfg); err != nil {
			cfg.Logger.Error("Failed to retrieve server statistics", "error", err)
			http.Error(w, "Error retrieving server statistics", http.StatusInternalServerError)
			return
		}

		if err := buildClientCustomStats(&statsBuilder, manager, cfg, sortBy, sortOrder); err != nil {
			cfg.Logger.Error("Failed to retrieve client statistics", "error", err)
			http.Error(w, "Error retrieving client statistics", http.StatusInternalServerError)
			return
		}

		if statsBuilder.String() == "" {
			cfg.Logger.Warn("No custom columns specified in configuration")
			fmt.Fprintln(w, "No custom columns specified in configuration.")
			return
		}

		cfg.Logger.Debug("Writing response", "response_length", len(statsBuilder.String()))
		fmt.Fprintln(w, statsBuilder.String())
		cfg.Logger.Info("API stats: completed successfully", "sort_by", sortBy, "sort_order", sortOrder)
	}
}

// buildTrafficStats collects traffic statistics.
func buildTrafficStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, mode, sortBy, sortOrder string) error {
	cfg.Logger.Debug("Collecting traffic statistics")

	appendStats(builder, "➤  Server Statistics:\n")
	var serverQuery string
	var trafficColsServer []string
	switch mode {
	case "minimal", "standard":
		serverQuery = `
            SELECT source AS "Source",
                   rate AS "Rate",
                   uplink AS "Uplink",
                   downlink AS "Downlink"
            FROM traffic_stats;
        `
		trafficColsServer = []string{"Rate", "Uplink", "Downlink"}
	case "extended", "full":
		serverQuery = `
            SELECT source AS "Source",
                   rate AS "Rate",
                   sess_uplink AS "Sess Up",
                   sess_downlink AS "Sess Down",
                   uplink AS "Uplink",
                   downlink AS "Downlink"
            FROM traffic_stats;
        `
		trafficColsServer = []string{"Rate", "Sess Up", "Sess Down", "Uplink", "Downlink"}
	}

	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Executing server traffic stats query", "query", serverQuery)
		rows, err := db.Query(serverQuery)
		if err != nil {
			cfg.Logger.Error("Failed to execute server stats query", "error", err)
			return fmt.Errorf("failed to execute server stats query: %v", err)
		}
		defer rows.Close()

		serverTable, err := formatTable(rows, trafficColsServer, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to format server stats table", "error", err)
			return err
		}
		appendStats(builder, serverTable)
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error processing server traffic stats", "error", err)
		return err
	}

	appendStats(builder, "\n➤  Client Statistics:\n")
	var clientQuery string
	var trafficColsClients []string
	switch mode {
	case "minimal":
		clientQuery = fmt.Sprintf(`
            SELECT user AS "User", 
                   last_seen AS "Last seen",
                   rate AS "Rate", 
                   uplink AS "Uplink", 
                   downlink AS "Downlink"
            FROM clients_stats
            ORDER BY %s %s;`, sortBy, sortOrder)
		trafficColsClients = []string{"Rate", "Uplink", "Downlink"}
	case "standard":
		clientQuery = fmt.Sprintf(`
            SELECT user AS "User", 
                   last_seen AS "Last seen",
                   rate AS "Rate", 
                   uplink AS "Uplink", 
                   downlink AS "Downlink", 
                   enabled AS "Enabled", 
                   sub_end AS "Sub end",
                   renew AS "Renew", 
                   lim_ip AS "Lim", 
                   ips AS "Ips"
            FROM clients_stats
            ORDER BY %s %s;`, sortBy, sortOrder)
		trafficColsClients = []string{"Rate", "Uplink", "Downlink"}
	case "extended":
		clientQuery = fmt.Sprintf(`
            SELECT user AS "User", 
                   last_seen AS "Last seen",
                   rate AS "Rate", 
                   sess_uplink AS "Sess Up", 
                   sess_downlink AS "Sess Down",
                   uplink AS "Uplink", 
                   downlink AS "Downlink", 
                   enabled AS "Enabled", 
                   sub_end AS "Sub end",
                   renew AS "Renew", 
                   lim_ip AS "Lim", 
                   ips AS "Ips"
            FROM clients_stats
            ORDER BY %s %s;`, sortBy, sortOrder)
		trafficColsClients = []string{"Rate", "Sess Up", "Sess Down", "Uplink", "Downlink"}
	case "full":
		clientQuery = fmt.Sprintf(`
			SELECT user AS "User", 
				   uuid AS "ID",
				   last_seen AS "Last seen",
				   rate AS "Rate",
				   sess_uplink AS "Sess Up", 
				   sess_downlink AS "Sess Down",
				   uplink AS "Uplink", 
				   downlink AS "Downlink", 
				   enabled AS "Enabled", 
				   sub_end AS "Sub end",
				   renew AS "Renew", 
				   lim_ip AS "Lim", 
				   ips AS "Ips",
				   created AS "Created"
		    FROM clients_stats
		    ORDER BY %s %s;`, sortBy, sortOrder)
		trafficColsClients = []string{"Rate", "Sess Up", "Sess Down", "Uplink", "Downlink"}
	}

	err = manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Executing client traffic stats query", "query", clientQuery)
		rows, err := db.Query(clientQuery)
		if err != nil {
			cfg.Logger.Error("Failed to execute client stats query", "error", err)
			return fmt.Errorf("failed to execute client stats query: %v", err)
		}
		defer rows.Close()

		clientTable, err := formatTable(rows, trafficColsClients, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to format client stats table", "error", err)
			return err
		}
		appendStats(builder, clientTable)
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error processing client traffic stats", "error", err)
		return err
	}

	cfg.Logger.Debug("Traffic statistics collected successfully")
	return nil
}

// StatsHandler handles requests to /api/v1/stats.
func StatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting StatsHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		mode := r.URL.Query().Get("mode")
		validModes := []string{"minimal", "standard", "extended", "full"}
		if !contains(validModes, mode) {
			if mode != "" {
				cfg.Logger.Warn("Invalid mode parameter", "mode", mode)
				http.Error(w, fmt.Sprintf("Invalid mode parameter: %s, must be one of %v", mode, validModes), http.StatusBadRequest)
				return
			}
			cfg.Logger.Debug("Setting default mode", "mode", "minimal")
			mode = "minimal"
		}

		sortBy := r.URL.Query().Get("sort_by")
		validSortColumns := []string{"user", "uuid", "last_seen", "rate", "sess_uplink", "sess_downlink", "uplink", "downlink", "enabled", "sub_end", "renew", "lim_ip", "ips", "created"}
		if sortBy == "" {
			sortBy = "user" // Default sort column
			cfg.Logger.Debug("Setting default sort_by", "sort_by", sortBy)
		} else if !contains(validSortColumns, sortBy) {
			cfg.Logger.Warn("Invalid sort_by parameter", "sort_by", sortBy)
			http.Error(w, fmt.Sprintf("Invalid sort_by parameter: %s, must be one of %v", sortBy, validSortColumns), http.StatusBadRequest)
			return
		}

		sortOrder := r.URL.Query().Get("sort_order")
		if sortOrder == "" {
			sortOrder = "ASC" // Default sort order
			cfg.Logger.Debug("Setting default sort_order", "sort_order", sortOrder)
		} else if sortOrder != "ASC" && sortOrder != "DESC" {
			cfg.Logger.Warn("Invalid sort_order parameter", "sort_order", sortOrder)
			http.Error(w, fmt.Sprintf("Invalid sort_order parameter: %s, must be ASC or DESC", sortOrder), http.StatusBadRequest)
			return
		}

		var statsBuilder strings.Builder

		if cfg.Features["system_monitoring"] {
			cfg.Logger.Debug("Collecting system statistics")
			buildServerStateStats(&statsBuilder, cfg)
		}
		if cfg.Features["network"] {
			cfg.Logger.Debug("Collecting network statistics")
			buildNetworkStats(&statsBuilder, cfg)
		}

		if err := buildTrafficStats(&statsBuilder, manager, cfg, mode, sortBy, sortOrder); err != nil {
			cfg.Logger.Error("Failed to retrieve traffic statistics", "error", err)
			http.Error(w, "Error processing statistics", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Writing response", "response_length", len(statsBuilder.String()))
		fmt.Fprintln(w, statsBuilder.String())
		cfg.Logger.Info("API stats/base: completed successfully", "mode", mode, "sort_by", sortBy, "sort_order", sortOrder)
	}
}

// ResetTrafficHandler handles requests to reset traffic statistics.
func ResetTrafficHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting ResetTrafficHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use POST", http.StatusMethodNotAllowed)
			return
		}

		cfg.Logger.Trace("Checking request parameters", "query_params", r.URL.Query().Encode())

		cfg.Logger.Debug("Retrieving traffic monitor")
		trafficMonitor := stats.GetTrafficMonitor()
		if trafficMonitor == nil {
			cfg.Logger.Error("Traffic monitor not initialized")
			http.Error(w, "Traffic monitor not initialized", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Resetting traffic statistics")
		err := trafficMonitor.ResetTraffic(cfg)
		if err != nil {
			cfg.Logger.Error("Failed to reset traffic", "error", err)
			http.Error(w, fmt.Sprintf("Failed to reset traffic: %v", err), http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Traffic reset successfully")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Traffic reset successfully")
	}
}

// DnsStat represents DNS query statistics.
type DnsStat struct {
	User   string
	Count  int
	Domain string
}

// getDnsStats executes a query and returns formatted DNS statistics.
func getDnsStats(manager *manager.DatabaseManager, cfg *config.Config, user, count string) (string, error) {
	if user == "" {
		cfg.Logger.Warn("Missing user parameter")
		return "", fmt.Errorf("missing user parameter")
	}

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

	var statsBuilder strings.Builder
	statsBuilder.WriteString(" 📊 DNS Query Statistics:\n")
	statsBuilder.WriteString(fmt.Sprintf("%-12s %-6s %-s\n", "User", "Count", "Domain"))
	statsBuilder.WriteString("-------------------------------------------------------------\n")

	err = manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Executing query on dns_stats table", "user", user, "count", count)
		rows, err := db.Query(`
			SELECT user AS "User", count AS "Count", domain AS "Domain"
			FROM dns_stats
			WHERE user = ?
			ORDER BY count DESC
			LIMIT ?`, user, count)
		if err != nil {
			cfg.Logger.Error("Failed to execute SQL query", "user", user, "error", err)
			return fmt.Errorf("failed to execute SQL query: %v", err)
		}
		defer rows.Close()

		var stats []DnsStat
		for rows.Next() {
			var stat DnsStat
			if err := rows.Scan(&stat.User, &stat.Count, &stat.Domain); err != nil {
				cfg.Logger.Error("Failed to scan row", "error", err)
				return fmt.Errorf("failed to scan row: %v", err)
			}
			if stat.User == "" {
				cfg.Logger.Warn("Empty user found in DNS stats", "domain", stat.Domain)
				continue
			}
			cfg.Logger.Trace("Read DNS stat", "user", stat.User, "count", stat.Count, "domain", stat.Domain)
			stats = append(stats, stat)
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Error iterating rows", "error", err)
			return fmt.Errorf("error iterating rows: %v", err)
		}

		if len(stats) == 0 {
			cfg.Logger.Warn("No DNS statistics found", "user", user)
		}

		cfg.Logger.Debug("Formatting DNS statistics", "stats_count", len(stats))
		for _, stat := range stats {
			statsBuilder.WriteString(fmt.Sprintf("%-12s %-6d %-s\n", stat.User, stat.Count, stat.Domain))
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

		user := r.URL.Query().Get("user")
		count := r.URL.Query().Get("count")

		if user == "" {
			cfg.Logger.Warn("Missing user parameter")
			http.Error(w, "Missing user parameter", http.StatusBadRequest)
			return
		}

		if count == "" {
			count = "20"
			cfg.Logger.Debug("Setting default count value", "count", count)
		}

		response, err := getDnsStats(manager, cfg, user, count)
		if err != nil {
			cfg.Logger.Error("Error in DnsStatsHandler retrieving stats", "user", user, "error", err)
			http.Error(w, "Error processing data", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Writing response", "user", user, "response_length", len(response))
		fmt.Fprintln(w, response)
		cfg.Logger.Info("API dns_stats: completed successfully", "user", user, "count", count)
	}
}

// UpdateIPLimitHandler updates the IP limit for a user in the clients_stats table.
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
		if len(userIdentifier) > 40 {
			cfg.Logger.Warn("User identifier too long", "length", len(userIdentifier))
			http.Error(w, "User identifier too long (max 40 characters)", http.StatusBadRequest)
			return
		}
		if ipLimit != "" && len(ipLimit) > 40 {
			cfg.Logger.Warn("lim_ip value too long", "length", len(ipLimit))
			http.Error(w, "lim_ip value too long (max 40 characters)", http.StatusBadRequest)
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
			result, err := tx.Exec("UPDATE clients_stats SET lim_ip = ? WHERE user = ?", ipLimitInt, userIdentifier)
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

			cfg.Logger.Info("IP limit updated successfully", "user", userIdentifier, "lim_ip", ipLimitInt)
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error in UpdateIPLimitHandler", "error", err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		cfg.Logger.Debug("IP limit update request completed successfully", "user", userIdentifier, "lim_ip", ipLimitInt)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "IP limit updated successfully")
	}
}

// DeleteDNSStatsHandler deletes all records from the dns_stats table.
func DeleteDNSStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting DeleteDNSStatsHandler request processing")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use POST", http.StatusMethodNotAllowed)
			return
		}

		cfg.Logger.Trace("Request to delete DNS stats records", "remote_addr", r.RemoteAddr)
		err := manager.ExecuteHighPriority(func(db1 *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for deleting records")
			tx, err := db1.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			cfg.Logger.Debug("Executing delete query for dns_stats")
			result, err := tx.Exec("DELETE FROM dns_stats")
			if err != nil {
				cfg.Logger.Error("Failed to delete records from dns_stats", "error", err)
				return fmt.Errorf("failed to delete records from dns_stats: %v", err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
			if rowsAffected == 0 {
				cfg.Logger.Warn("No rows affected during deletion", "table", "dns_stats")
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
			http.Error(w, "Failed to delete DNS stats records", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("DNS stats deletion request completed successfully", "remote_addr", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "DNS stats records deleted successfully")
	}
}

// ResetTrafficStatsHandler resets traffic statistics in the traffic_stats table.
func ResetTrafficStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting ResetTrafficStatsHandler request processing")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use POST", http.StatusMethodNotAllowed)
			return
		}

		cfg.Logger.Trace("Request to reset traffic stats", "remote_addr", r.RemoteAddr)
		err := manager.ExecuteHighPriority(func(db1 *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for resetting stats")
			tx, err := db1.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			cfg.Logger.Debug("Executing reset traffic stats query")
			result, err := tx.Exec("UPDATE traffic_stats SET uplink = 0, downlink = 0")
			if err != nil {
				cfg.Logger.Error("Failed to reset traffic stats", "error", err)
				return fmt.Errorf("failed to reset traffic stats: %v", err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
			if rowsAffected == 0 {
				cfg.Logger.Warn("No rows affected during reset", "table", "traffic_stats")
			}

			cfg.Logger.Debug("Committing transaction")
			if err := tx.Commit(); err != nil {
				cfg.Logger.Error("Failed to commit transaction", "error", err)
				return fmt.Errorf("failed to commit transaction: %v", err)
			}

			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in ResetTrafficStatsHandler", "error", err)
			http.Error(w, "Failed to reset traffic stats", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Traffic stats reset request completed successfully", "remote_addr", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Traffic stats reset successfully")
	}
}

// ResetClientsStatsHandler resets traffic statistics in the clients_stats table.
func ResetClientsStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting ResetClientsStatsHandler request processing")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use POST", http.StatusMethodNotAllowed)
			return
		}

		cfg.Logger.Trace("Request to reset client traffic stats", "remote_addr", r.RemoteAddr)
		err := manager.ExecuteHighPriority(func(db1 *sql.DB) error {
			cfg.Logger.Debug("Starting transaction for resetting client stats")
			tx, err := db1.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			cfg.Logger.Debug("Executing reset client traffic stats query")
			result, err := tx.Exec("UPDATE clients_stats SET uplink = 0, downlink = 0")
			if err != nil {
				cfg.Logger.Error("Failed to reset client traffic stats", "error", err)
				return fmt.Errorf("failed to reset client traffic stats: %v", err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get affected rows", "error", err)
				return fmt.Errorf("failed to get affected rows: %v", err)
			}
			if rowsAffected == 0 {
				cfg.Logger.Warn("No rows affected during reset", "table", "clients_stats")
			}

			cfg.Logger.Debug("Committing transaction")
			if err := tx.Commit(); err != nil {
				cfg.Logger.Error("Failed to commit transaction", "error", err)
				return fmt.Errorf("failed to commit transaction: %v", err)
			}

			cfg.Logger.Info("Client traffic stats reset successfully", "rows_affected", rowsAffected)
			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in ResetClientsStatsHandler", "error", err)
			http.Error(w, "Failed to reset client traffic stats", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Client traffic stats reset request completed successfully", "remote_addr", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Client traffic stats reset successfully")
	}
}

// UpdateRenewHandler updates the renew value for a user in the clients_stats table.
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
			result, err := tx.Exec("UPDATE clients_stats SET renew = ? WHERE user = ?", renew, userIdentifier)
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

		cfg.Logger.Info("Renew update request completed successfully", "user", userIdentifier, "renew", renew)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Renew value updated successfully")
	}
}

// AddUserToConfig adds a user to the configuration file.
func AddUserToConfig(user, credential, inboundTag string, cfg *config.Config) error {
	cfg.Logger.Debug("Starting user addition to configuration", "user", user, "inboundTag", inboundTag)
	configPath := cfg.Core.Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to read config.json: %v", err)
	}

	proxyType := cfg.V2rayStat.Type
	var configData any
	var protocol string

	switch proxyType {
	case "xray":
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		found := false
		for i, inbound := range cfgXray.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Protocol
				for _, client := range inbound.Settings.Clients {
					if protocol == "vless" && client.ID == credential {
						cfg.Logger.Warn("User with this ID already exists", "credential", credential)
						return fmt.Errorf("user with this id already exists")
					} else if protocol == "trojan" && client.Password == credential {
						cfg.Logger.Warn("User with this password already exists", "credential", credential)
						return fmt.Errorf("user with this password already exists")
					}
				}
				newClient := config.XrayClient{Email: user}
				switch protocol {
				case "vless":
					newClient.ID = credential
				case "trojan":
					newClient.Password = credential
				}
				cfgXray.Inbounds[i].Settings.Clients = append(cfgXray.Inbounds[i].Settings.Clients, newClient)
				found = true
				break
			}
		}
		if !found {
			cfg.Logger.Warn("Inbound not found", "inboundTag", inboundTag)
			return fmt.Errorf("inbound with tag %s not found", inboundTag)
		}
		configData = cfgXray

	case "singbox":
		var cfgSingBox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingBox); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		found := false
		for i, inbound := range cfgSingBox.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Type
				for _, user := range inbound.Users {
					if protocol == "vless" && user.UUID == credential {
						cfg.Logger.Warn("User with this UUID already exists", "credential", credential)
						return fmt.Errorf("user with this uuid already exists")
					} else if protocol == "trojan" && user.Password == credential {
						cfg.Logger.Warn("User with this password already exists", "credential", credential)
						return fmt.Errorf("user with this password already exists")
					}
				}
				newUser := config.SingboxClient{Name: user}
				switch protocol {
				case "vless":
					newUser.UUID = credential
				case "trojan":
					newUser.Password = credential
				}
				cfgSingBox.Inbounds[i].Users = append(cfgSingBox.Inbounds[i].Users, newUser)
				found = true
				break
			}
		}
		if !found {
			cfg.Logger.Warn("Inbound not found", "inboundTag", inboundTag)
			return fmt.Errorf("inbound with tag %s not found", inboundTag)
		}
		configData = cfgSingBox

	default:
		cfg.Logger.Warn("Unsupported core type", "proxyType", proxyType)
		return fmt.Errorf("unsupported core type: %s", proxyType)
	}

	updateData, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		cfg.Logger.Error("Failed to marshal JSON", "error", err)
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}
	if err := os.WriteFile(configPath, updateData, 0644); err != nil {
		cfg.Logger.Error("Failed to write config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to write config.json: %v", err)
	}

	cfg.Logger.Info("User added to configuration", "user", user, "inboundTag", inboundTag)

	if cfg.Features["auth_lua"] {
		cfg.Logger.Debug("Adding user to auth.lua", "user", user)
		var credentialToAdd string
		if protocol == "trojan" {
			hash := sha256.Sum224([]byte(credential))
			credentialToAdd = hex.EncodeToString(hash[:])
			cfg.Logger.Trace("Hashed credential for trojan", "credential", credentialToAdd)
		} else {
			credentialToAdd = credential
			cfg.Logger.Trace("Using raw credential for vless", "credential", credentialToAdd)
		}
		if err := lua.AddUserToAuthLua(cfg, user, credentialToAdd); err != nil {
			cfg.Logger.Error("Failed to add user to auth.lua", "user", user, "error", err)
		} else {
			cfg.Logger.Info("User added to auth.lua", "user", user)
		}
	}

	return nil
}

// AddUserHandler handles HTTP requests to add a new user.
func AddUserHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting AddUserHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use POST", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			cfg.Logger.Error("Failed to parse form data", "error", err)
			http.Error(w, "Error parsing form data", http.StatusBadRequest)
			return
		}

		userIdentifier := r.FormValue("user")
		credential := r.FormValue("credential")
		inboundTag := r.FormValue("inboundTag")

		if userIdentifier == "" || credential == "" {
			cfg.Logger.Warn("Missing or empty user or credential parameters")
			http.Error(w, "user and credential are required", http.StatusBadRequest)
			return
		}

		if len(userIdentifier) > 40 || len(credential) > 40 {
			cfg.Logger.Warn("User or credential too long", "user_length", len(userIdentifier), "credential_length", len(credential))
			http.Error(w, "user or credential too long (max 40 characters)", http.StatusBadRequest)
			return
		}

		if inboundTag == "" {
			inboundTag = "vless-in"
			cfg.Logger.Debug("Setting default inboundTag", "inboundTag", inboundTag)
		}

		cfg.Logger.Trace("Request parameters", "user", userIdentifier, "credential", credential, "inboundTag", inboundTag)

		cfg.Logger.Debug("Adding user to configuration", "user", userIdentifier)
		err := AddUserToConfig(userIdentifier, credential, inboundTag, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to add user", "user", userIdentifier, "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		cfg.Logger.Info("API add_user: user added successfully", "user", userIdentifier)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "User added successfully")
	}
}

// saveConfig saves configuration data to a file.
func saveConfig(w http.ResponseWriter, configPath string, configData any, cfg *config.Config, logMessage string) error {
	cfg.Logger.Debug("Marshaling JSON for configuration", "path", configPath)
	updateData, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		cfg.Logger.Error("Failed to marshal JSON", "error", err)
		if w != nil {
			http.Error(w, "Error updating configuration", http.StatusInternalServerError)
		}
		return err
	}

	cfg.Logger.Debug("Writing configuration file", "path", configPath)
	if err := os.WriteFile(configPath, updateData, 0644); err != nil {
		cfg.Logger.Error("Failed to write config.json", "path", configPath, "error", err)
		if w != nil {
			http.Error(w, "Error saving configuration", http.StatusInternalServerError)
		}
		return err
	}

	cfg.Logger.Info(logMessage)
	return nil
}

// DeleteUserFromConfig removes a user from the configuration files.
func DeleteUserFromConfig(userIdentifier, inboundTag string, cfg *config.Config) error {
	cfg.Logger.Debug("Starting user deletion from configuration", "user", userIdentifier, "inboundTag", inboundTag)
	start := time.Now()
	configPath := cfg.Core.Config
	disabledUsersPath := filepath.Join(cfg.Core.Dir, ".disabled_users")
	proxyType := cfg.V2rayStat.Type

	var userRemoved bool

	switch proxyType {
	case "xray":
		cfg.Logger.Debug("Reading main config for Xray", "path", configPath)
		mainConfigData, err := os.ReadFile(configPath)
		if err != nil {
			cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
			return fmt.Errorf("failed to read config.json: %v", err)
		}
		var mainConfig config.ConfigXray
		if err := json.Unmarshal(mainConfigData, &mainConfig); err != nil {
			cfg.Logger.Error("Failed to parse JSON for config.json", "error", err)
			return fmt.Errorf("failed to parse JSON for config.json: %v", err)
		}

		cfg.Logger.Debug("Reading disabled users config", "path", disabledUsersPath)
		var disabledConfig config.DisabledUsersConfigXray
		disabledConfigData, err := os.ReadFile(disabledUsersPath)
		if err == nil && len(disabledConfigData) > 0 {
			if err := json.Unmarshal(disabledConfigData, &disabledConfig); err != nil {
				cfg.Logger.Error("Failed to parse JSON for .disabled_users", "error", err)
				return fmt.Errorf("failed to parse JSON for .disabled_users: %v", err)
			}
		} else {
			disabledConfig = config.DisabledUsersConfigXray{Inbounds: []config.XrayInbound{}}
		}

		// Function to remove user from inbounds (Xray)
		removeXrayUser := func(inbounds []config.XrayInbound) ([]config.XrayInbound, bool) {
			for i, inbound := range inbounds {
				if inbound.Tag == inboundTag {
					updatedClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients))
					for _, client := range inbound.Settings.Clients {
						if client.Email != userIdentifier {
							updatedClients = append(updatedClients, client)
						}
					}
					if len(updatedClients) < len(inbound.Settings.Clients) {
						inbounds[i].Settings.Clients = updatedClients
						return inbounds, true
					}
				}
			}
			return inbounds, false
		}

		// Check and remove from config.json
		mainUpdated, removedFromMain := removeXrayUser(mainConfig.Inbounds)
		if removedFromMain {
			mainConfig.Inbounds = mainUpdated
			logMessage := fmt.Sprintf("User %s successfully removed from config.json, inbound %s [%v]", userIdentifier, inboundTag, time.Since(start))
			if err := saveConfig(nil, configPath, mainConfig, cfg, logMessage); err != nil {
				return err
			}
			userRemoved = true
		}

		// Check and remove from .disabled_users
		disabledUpdated, removedFromDisabled := removeXrayUser(disabledConfig.Inbounds)
		if removedFromDisabled {
			disabledConfig.Inbounds = disabledUpdated
			if len(disabledConfig.Inbounds) > 0 {
				logMessage := fmt.Sprintf("User %s successfully removed from .disabled_users, inbound %s [%v]", userIdentifier, inboundTag, time.Since(start))
				if err := saveConfig(nil, disabledUsersPath, disabledConfig, cfg, logMessage); err != nil {
					return err
				}
			} else {
				cfg.Logger.Debug("Removing empty .disabled_users file", "path", disabledUsersPath)
				if err := os.Remove(disabledUsersPath); err != nil && !os.IsNotExist(err) {
					cfg.Logger.Error("Failed to remove empty .disabled_users", "error", err)
					return fmt.Errorf("failed to remove empty .disabled_users: %v", err)
				}
				cfg.Logger.Info("User removed from .disabled_users", "user", userIdentifier, "inboundTag", inboundTag)
			}
			userRemoved = true
		}

	case "singbox":
		cfg.Logger.Debug("Reading main config for Singbox", "path", configPath)
		mainConfigData, err := os.ReadFile(configPath)
		if err != nil {
			cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
			return fmt.Errorf("failed to read config.json: %v", err)
		}
		var mainConfig config.ConfigSingbox
		if err := json.Unmarshal(mainConfigData, &mainConfig); err != nil {
			cfg.Logger.Error("Failed to parse JSON for config.json", "error", err)
			return fmt.Errorf("failed to parse JSON for config.json: %v", err)
		}

		cfg.Logger.Debug("Reading disabled users config", "path", disabledUsersPath)
		var disabledConfig config.DisabledUsersConfigSingbox
		disabledConfigData, err := os.ReadFile(disabledUsersPath)
		if err == nil && len(disabledConfigData) > 0 {
			if err := json.Unmarshal(disabledConfigData, &disabledConfig); err != nil {
				cfg.Logger.Error("Failed to parse JSON for .disabled_users", "error", err)
				return fmt.Errorf("failed to parse JSON for .disabled_users: %v", err)
			}
		} else {
			disabledConfig = config.DisabledUsersConfigSingbox{Inbounds: []config.SingboxInbound{}}
		}

		// Function to remove user from inbounds (Singbox)
		removeSingboxUser := func(inbounds []config.SingboxInbound) ([]config.SingboxInbound, bool) {
			for i, inbound := range inbounds {
				if inbound.Tag == inboundTag {
					updatedUsers := make([]config.SingboxClient, 0, len(inbound.Users))
					for _, user := range inbound.Users {
						if user.Name != userIdentifier {
							updatedUsers = append(updatedUsers, user)
						}
					}
					if len(updatedUsers) < len(inbound.Users) {
						inbounds[i].Users = updatedUsers
						return inbounds, true
					}
				}
			}
			return inbounds, false
		}

		// Check and remove from config.json
		mainUpdated, removedFromMain := removeSingboxUser(mainConfig.Inbounds)
		if removedFromMain {
			mainConfig.Inbounds = mainUpdated
			logMessage := fmt.Sprintf("User %s successfully removed from config.json, inbound %s [%v]", userIdentifier, inboundTag, time.Since(start))
			if err := saveConfig(nil, configPath, mainConfig, cfg, logMessage); err != nil {
				return err
			}
			userRemoved = true
		}

		// Check and remove from .disabled_users
		disabledUpdated, removedFromDisabled := removeSingboxUser(disabledConfig.Inbounds)
		if removedFromDisabled {
			disabledConfig.Inbounds = disabledUpdated
			if len(disabledConfig.Inbounds) > 0 {
				logMessage := fmt.Sprintf("User %s successfully removed from .disabled_users, inbound %s [%v]", userIdentifier, inboundTag, time.Since(start))
				if err := saveConfig(nil, disabledUsersPath, disabledConfig, cfg, logMessage); err != nil {
					return err
				}
			} else {
				cfg.Logger.Debug("Removing empty .disabled_users file", "path", disabledUsersPath)
				if err := os.Remove(disabledUsersPath); err != nil && !os.IsNotExist(err) {
					cfg.Logger.Error("Failed to remove empty .disabled_users", "error", err)
					return fmt.Errorf("failed to remove empty .disabled_users: %v", err)
				}
				cfg.Logger.Info("User removed from .disabled_users", "user", userIdentifier, "inboundTag", inboundTag)
			}
			userRemoved = true
		}
	}

	// Handle auth.lua update if user was removed
	if userRemoved && cfg.Features["auth_lua"] {
		cfg.Logger.Debug("Deleting user from auth.lua", "user", userIdentifier)
		if err := lua.DeleteUserFromAuthLua(cfg, userIdentifier); err != nil {
			cfg.Logger.Error("Failed to delete user from auth.lua", "user", userIdentifier, "error", err)
		} else {
			cfg.Logger.Info("User removed from auth.lua", "user", userIdentifier)
		}
	}

	// If user not found
	if !userRemoved {
		cfg.Logger.Warn("User not found in configuration", "user", userIdentifier, "inboundTag", inboundTag)
		return fmt.Errorf("user %s not found in inbound %s in either config.json or .disabled_users", userIdentifier, inboundTag)
	}

	cfg.Logger.Info("User deleted successfully", "user", userIdentifier, "inboundTag", inboundTag)
	return nil
}

// DeleteUserHandler handles HTTP requests to delete a user.
func DeleteUserHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting DeleteUserHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodDelete {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use DELETE", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			cfg.Logger.Error("Failed to parse form data", "error", err)
			http.Error(w, "Error parsing form data", http.StatusBadRequest)
			return
		}

		userIdentifier := r.FormValue("user")
		inboundTag := r.FormValue("inboundTag")
		if userIdentifier == "" {
			cfg.Logger.Warn("Missing or empty user parameter")
			http.Error(w, "user parameter is required", http.StatusBadRequest)
			return
		}
		if inboundTag == "" {
			inboundTag = "vless-in"
			cfg.Logger.Debug("Setting default inboundTag", "inboundTag", inboundTag)
		}

		cfg.Logger.Debug("Deleting user from configuration", "user", userIdentifier, "inboundTag", inboundTag)
		err := DeleteUserFromConfig(userIdentifier, inboundTag, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to delete user", "user", userIdentifier, "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		cfg.Logger.Info("API delete_user: user deleted successfully", "user", userIdentifier, "inboundTag", inboundTag)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "User deleted successfully")
	}
}

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
		if len(userIdentifier) > 40 {
			cfg.Logger.Warn("User identifier too long", "length", len(userIdentifier))
			http.Error(w, "User identifier too long (max 40 characters)", http.StatusBadRequest)
			return
		}
		if enabledStr != "" && len(enabledStr) > 40 {
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

			if err := db.ToggleUserEnabled(manager, cfg, userIdentifier, enabled); err != nil {
				cfg.Logger.Error("Failed to toggle user status in configuration", "user", userIdentifier, "enabled", enabled, "error", err)
				return fmt.Errorf("failed to toggle user status: %v", err)
			}

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

		cfg.Logger.Info("User status updated successfully", "user", userIdentifier, "enabled", enabled)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "User status updated successfully")
	}
}

// updateSubscriptionDate updates the subscription date for a user.
func updateSubscriptionDate(manager *manager.DatabaseManager, cfg *config.Config, userIdentifier, subEnd string) error {
	cfg.Logger.Debug("Starting subscription date update", "user", userIdentifier)

	// Validate input parameters
	if userIdentifier == "" {
		cfg.Logger.Warn("Empty user identifier")
		return fmt.Errorf("user identifier is empty")
	}
	if len(userIdentifier) > 40 {
		cfg.Logger.Warn("User identifier too long", "length", len(userIdentifier))
		return fmt.Errorf("user identifier too long (max 40 characters)")
	}
	if subEnd != "" && len(subEnd) > 40 {
		cfg.Logger.Warn("Subscription date too long", "length", len(subEnd))
		return fmt.Errorf("subscription date too long (max 40 characters)")
	}

	// Validate subEnd format if provided
	if subEnd != "" {
		_, err := time.Parse("2006-01-02-15", subEnd)
		if err != nil {
			cfg.Logger.Warn("Invalid subscription date format", "subEnd", subEnd, "error", err)
			return fmt.Errorf("invalid subscription date format: %v", err)
		}
	}

	cfg.Logger.Debug("Querying current subscription date from database", "user", userIdentifier)
	baseDate := time.Now().UTC()
	var subEndStr string
	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		return db.QueryRow("SELECT sub_end FROM clients_stats WHERE user = ?", userIdentifier).Scan(&subEndStr)
	})
	if err != nil && err != sql.ErrNoRows {
		cfg.Logger.Error("Failed to query database", "user", userIdentifier, "error", err)
		return fmt.Errorf("failed to query database: %v", err)
	}
	if err == sql.ErrNoRows {
		cfg.Logger.Warn("No record found for user", "user", userIdentifier)
	}
	cfg.Logger.Trace("Retrieved current subscription date", "subEndStr", subEndStr)

	if subEndStr != "" {
		cfg.Logger.Debug("Parsing current subscription date", "subEndStr", subEndStr)
		baseDate, err = time.Parse("2006-01-02-15", subEndStr)
		if err != nil {
			cfg.Logger.Error("Failed to parse current subscription date", "subEndStr", subEndStr, "error", err)
			return fmt.Errorf("failed to parse current subscription date: %v", err)
		}
		cfg.Logger.Trace("Parsed current subscription date", "baseDate", baseDate)
	}

	cfg.Logger.Debug("Updating subscription date", "user", userIdentifier, "subEnd", subEnd)
	err = db.AdjustDateOffset(manager, cfg, userIdentifier, subEnd, baseDate)
	if err != nil {
		cfg.Logger.Error("Failed to update subscription date", "user", userIdentifier, "error", err)
		return fmt.Errorf("failed to update subscription date: %v", err)
	}

	err = db.CheckExpiredSubscriptions(manager, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to check expired subscriptions", "error", err)
		return fmt.Errorf("failed to check expired subscriptions: %v", err)
	}

	cfg.Logger.Info("Subscription date updated successfully", "user", userIdentifier)
	return nil
}

// AdjustDateOffsetHandler handles requests to update the subscription date.
func AdjustDateOffsetHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting AdjustDateOffsetHandler request processing")

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
		subEnd := r.FormValue("sub_end")
		cfg.Logger.Trace("Received form parameters", "user", userIdentifier, "sub_end", subEnd)

		if userIdentifier == "" || subEnd == "" {
			cfg.Logger.Warn("Missing or empty user or sub_end parameters")
			http.Error(w, "user and sub_end are required", http.StatusBadRequest)
			return
		}

		err := updateSubscriptionDate(manager, cfg, userIdentifier, subEnd)
		if err != nil {
			cfg.Logger.Error("Failed to update subscription for user", "user", userIdentifier, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Writing response", "user", userIdentifier, "sub_end", subEnd)
		w.WriteHeader(http.StatusOK)
		_, err = fmt.Fprintf(w, "Subscription date for %s updated with sub_end %s\n", userIdentifier, subEnd)
		if err != nil {
			cfg.Logger.Error("Failed to write response for user", "user", userIdentifier, "error", err)
			http.Error(w, "Error sending response", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("Subscription date update completed successfully", "user", userIdentifier)
	}
}

// Answer handles basic server information requests.
func Answer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverHeader := fmt.Sprintf("MuxCloud/%s (WebServer)", constant.Version)
		w.Header().Set("Server", serverHeader)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Powered-By", "MuxCloud")
		fmt.Fprintf(w, "MuxCloud / %s\n", constant.Version)
	}
}
