package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/constant"
	"v2ray-stat/util"
)

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

// buildServerCustomStats collects custom server statistics.
func buildServerCustomStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Collecting custom server statistics")
	serverColumnAliases := map[string]string{
		"node_name":     "Node Name",
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
			} else {
				cfg.Logger.Warn("Invalid server column ignored", "column", col)
			}
		}
		if len(serverCols) == 0 {
			cfg.Logger.Warn("No valid columns specified for server stats")
			return nil
		}
		serverQuery := fmt.Sprintf("SELECT %s FROM bound_traffic ORDER BY %s %s;",
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
			if serverTable == "" {
				cfg.Logger.Warn("No data returned for server stats query")
				appendStats(builder, "No server statistics available.\n")
			} else {
				appendStats(builder, serverTable)
				appendStats(builder, "\n")
			}
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing server custom stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for server stats in configuration")
		appendStats(builder, "No columns specified for server stats.\n")
	}
	return nil
}

// buildClientCustomStats собирает пользовательскую статистику клиентов.
func buildClientCustomStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, sortBy, sortOrder string) error {
	cfg.Logger.Debug("Collecting custom client statistics")

	clientColumnAliases := map[string]string{
		"node_name":     "Node Name",
		"user":          "User",
		"last_seen":     "Last seen",
		"rate":          "Rate",
		"uplink":        "Uplink",
		"downlink":      "Downlink",
		"sess_uplink":   "Sess Up",
		"sess_downlink": "Sess Down",
		"sub_end":       "Sub end",
		"renew":         "Renew",
		"lim_ip":        "Lim IP",
		"ips":           "Ips",
		"created":       "Created",
		"enabled":       "Enabled",
	}
	clientAliases := []string{
		"Rate",
		"Uplink",
		"Downlink",
		"Sess Up",
		"Sess Down",
		"Renew",
		"Lim IP",
	}

	if len(cfg.StatsColumns.Client.Columns) > 0 {
		var clientCols []string
		for _, col := range cfg.StatsColumns.Client.Columns {
			if alias, ok := clientColumnAliases[col]; ok {
				if col == "sub_end" || col == "renew" || col == "lim_ip" || col == "ips" || col == "enabled" {
					clientCols = append(clientCols, fmt.Sprintf("ud.%s AS \"%s\"", col, alias))
				} else {
					clientCols = append(clientCols, fmt.Sprintf("ut.%s AS \"%s\"", col, alias))
				}
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

		// Используем LEFT JOIN для объединения user_traffic и user_data
		clientQuery := fmt.Sprintf(`
			SELECT %s 
			FROM user_traffic ut 
			LEFT JOIN user_data ud ON ut.user = ud.user 
			ORDER BY %s %s;`,
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

// buildNodeStats собирает статистику для конкретной ноды.
func buildNodeStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, nodeName string) error {
	cfg.Logger.Debug("Collecting node statistics", "node_name", nodeName)

	// Server (bound_traffic) statistics
	serverColumnAliases := map[string]string{
		"node_name":     "Node Name",
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

	var serverCols []string
	for _, col := range cfg.StatsColumns.Server.Columns {
		if alias, ok := serverColumnAliases[col]; ok {
			serverCols = append(serverCols, fmt.Sprintf("%s AS \"%s\"", col, alias))
		}
	}
	serverQuery := fmt.Sprintf("SELECT %s FROM bound_traffic WHERE node_name = ? ORDER BY %s %s;",
		strings.Join(serverCols, ", "), cfg.StatsColumns.Server.SortBy, cfg.StatsColumns.Server.SortOrder)

	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Executing node server stats query", "query", serverQuery, "node_name", nodeName)
		rows, err := db.Query(serverQuery, nodeName)
		if err != nil {
			cfg.Logger.Error("Failed to execute node server stats query", "node_name", nodeName, "error", err)
			return fmt.Errorf("failed to execute node server stats query: %v", err)
		}
		defer rows.Close()

		appendStats(builder, fmt.Sprintf("➤  Server Statistics for Node %s:\n", nodeName))
		serverTable, err := formatTable(rows, trafficAliases, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to format node server stats table", "node_name", nodeName, "error", err)
			return fmt.Errorf("failed to format node server stats table: %v", err)
		}
		appendStats(builder, serverTable)
		appendStats(builder, "\n")
		return nil
	})

	if err != nil {
		cfg.Logger.Error("Error processing node server stats", "node_name", nodeName, "error", err)
		return err
	}

	// Client (user_traffic) statistics
	clientColumnAliases := map[string]string{
		"node_name":     "Node Name",
		"user":          "User",
		"last_seen":     "Last seen",
		"rate":          "Rate",
		"uplink":        "Uplink",
		"downlink":      "Downlink",
		"sess_uplink":   "Sess Up",
		"sess_downlink": "Sess Down",
		"sub_end":       "Sub end",
		"renew":         "Renew",
		"lim_ip":        "Lim IP",
		"ips":           "Ips",
		"created":       "Created",
		"enabled":       "Enabled",
	}
	clientAliases := []string{
		"Rate",
		"Uplink",
		"Downlink",
		"Sess Up",
		"Sess Down",
		"Renew",
		"Lim IP",
	}

	var clientCols []string
	for _, col := range cfg.StatsColumns.Client.Columns {
		if alias, ok := clientColumnAliases[col]; ok {
			if col == "sub_end" || col == "renew" || col == "lim_ip" || col == "ips" || col == "enabled" {
				clientCols = append(clientCols, fmt.Sprintf("ud.%s AS \"%s\"", col, alias))
			} else {
				clientCols = append(clientCols, fmt.Sprintf("ut.%s AS \"%s\"", col, alias))
			}
		}
	}
	clientQuery := fmt.Sprintf(`
		SELECT %s 
		FROM user_traffic ut 
		LEFT JOIN user_data ud ON ut.user = ud.user 
		WHERE ut.node_name = ? 
		ORDER BY %s %s;`,
		strings.Join(clientCols, ", "), cfg.StatsColumns.Client.SortBy, cfg.StatsColumns.Client.SortOrder)

	err = manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Executing node client stats query", "query", clientQuery, "node_name", nodeName)
		rows, err := db.Query(clientQuery, nodeName)
		if err != nil {
			cfg.Logger.Error("Failed to execute node client stats query", "node_name", nodeName, "error", err)
			return fmt.Errorf("failed to execute node client stats query: %v", err)
		}
		defer rows.Close()

		appendStats(builder, fmt.Sprintf("➤  Client Statistics for Node %s:\n", nodeName))
		clientTable, err := formatTable(rows, clientAliases, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to format node client stats table", "node_name", nodeName, "error", err)
			return fmt.Errorf("failed to format node client stats table: %v", err)
		}
		appendStats(builder, clientTable)
		return nil
	})

	if err != nil {
		cfg.Logger.Error("Error processing node client stats", "node_name", nodeName, "error", err)
		return err
	}

	return nil
}

// NodeStatsHandler handles requests to /api/v1/node_stats.
func NodeStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting NodeStatsHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		nodeName := r.URL.Query().Get("node_name")
		if nodeName == "" {
			cfg.Logger.Warn("Missing node_name parameter")
			http.Error(w, "Missing node_name parameter", http.StatusBadRequest)
			return
		}

		// Validate node_name against configured nodes
		validNode := false
		for _, node := range cfg.V2rayStat.Nodes {
			if node.Name == nodeName {
				validNode = true
				break
			}
		}
		if !validNode {
			cfg.Logger.Warn("Invalid node_name", "node_name", nodeName)
			http.Error(w, fmt.Sprintf("Invalid node_name: %s", nodeName), http.StatusBadRequest)
			return
		}

		var statsBuilder strings.Builder
		if err := buildNodeStats(&statsBuilder, manager, cfg, nodeName); err != nil {
			cfg.Logger.Error("Failed to retrieve node statistics", "node_name", nodeName, "error", err)
			http.Error(w, fmt.Sprintf("Error retrieving statistics for node %s", nodeName), http.StatusInternalServerError)
			return
		}

		if statsBuilder.String() == "" {
			cfg.Logger.Warn("No statistics available for node", "node_name", nodeName)
			fmt.Fprintf(w, "No statistics available for node %s.\n", nodeName)
			return
		}

		cfg.Logger.Debug("Writing response", "response_length", len(statsBuilder.String()))
		fmt.Fprintln(w, statsBuilder.String())
		cfg.Logger.Info("API node stats: completed successfully", "node_name", nodeName)
	}
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
		validSortColumns := []string{
			"node_name", "user", "last_seen", "rate", "uplink", "downlink", "sess_uplink", "sess_downlink", "created",
		}
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

// buildAggregateServerStats collects aggregated server statistics grouped by source.
func buildAggregateServerStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Collecting aggregated server statistics")
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
			if col == "node_name" {
				continue // Пропускаем node_name для агрегированной статистики
			}
			if alias, ok := serverColumnAliases[col]; ok {
				if col == "source" {
					serverCols = append(serverCols, col)
				} else {
					serverCols = append(serverCols, fmt.Sprintf("SUM(%s) AS \"%s\"", col, alias))
				}
			} else {
				cfg.Logger.Warn("Invalid server column ignored", "column", col)
			}
		}
		if len(serverCols) == 0 {
			cfg.Logger.Warn("No valid columns specified for aggregate server stats")
			appendStats(builder, "No valid columns specified for aggregate server stats.\n")
			return nil
		}
		serverQuery := fmt.Sprintf("SELECT %s FROM bound_traffic GROUP BY source ORDER BY SUM(%s) %s;",
			strings.Join(serverCols, ", "), cfg.StatsColumns.Server.SortBy, cfg.StatsColumns.Server.SortOrder)

		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing aggregate server stats query", "query", serverQuery)
			rows, err := db.Query(serverQuery)
			if err != nil {
				cfg.Logger.Error("Failed to execute aggregate server stats query", "error", err)
				return fmt.Errorf("failed to execute aggregate server stats query: %v", err)
			}
			defer rows.Close()

			columns, err := rows.Columns()
			if err != nil {
				cfg.Logger.Error("Failed to get columns from aggregate server stats query", "error", err)
				return fmt.Errorf("failed to get columns: %v", err)
			}
			cfg.Logger.Debug("Aggregate server stats columns returned", "columns", columns)

			appendStats(builder, "➤  Aggregate Server Statistics:\n")
			serverTable, err := formatTable(rows, trafficAliases, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to format aggregate server stats table", "error", err)
				return fmt.Errorf("failed to format aggregate server stats table: %v", err)
			}
			if serverTable == "" {
				cfg.Logger.Warn("No data returned for aggregate server stats query")
				appendStats(builder, "No aggregate server statistics available.\n")
			} else {
				appendStats(builder, serverTable)
				appendStats(builder, "\n")
			}
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing aggregate server stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for aggregate server stats in configuration")
		appendStats(builder, "No columns specified for aggregate server stats.\n")
	}
	return nil
}

// buildAggregateClientStats собирает агрегированную статистику клиентов, сгруппированную по пользователям.
func buildAggregateClientStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, sortBy, sortOrder string) error {
	cfg.Logger.Debug("Collecting aggregated client statistics")

	clientColumnAliases := map[string]string{
		"user":          "User",
		"last_seen":     "Last seen",
		"rate":          "Rate",
		"uplink":        "Uplink",
		"downlink":      "Downlink",
		"sess_uplink":   "Sess Up",
		"sess_downlink": "Sess Down",
		"sub_end":       "Sub end",
		"renew":         "Renew",
		"lim_ip":        "Lim IP",
		"ips":           "Ips",
		"created":       "Created",
		"enabled":       "Enabled",
	}
	clientAliases := []string{
		"Rate",
		"Uplink",
		"Downlink",
		"Sess Up",
		"Sess Down",
		"Renew",
		"Lim IP",
	}

	if len(cfg.StatsColumns.Client.Columns) > 0 {
		var clientCols []string
		for _, col := range cfg.StatsColumns.Client.Columns {
			if col == "node_name" {
				continue // Пропускаем node_name для агрегированной статистики
			}
			if alias, ok := clientColumnAliases[col]; ok {
				switch col {
				case "user", "last_seen", "created":
					if col == "last_seen" {
						clientCols = append(clientCols, fmt.Sprintf("MAX(ut.%s) AS \"%s\"", col, alias))
					} else {
						clientCols = append(clientCols, fmt.Sprintf("MIN(ut.%s) AS \"%s\"", col, alias))
					}
				case "sub_end", "renew", "lim_ip", "ips", "enabled":
					clientCols = append(clientCols, fmt.Sprintf("MIN(ud.%s) AS \"%s\"", col, alias))
				default:
					clientCols = append(clientCols, fmt.Sprintf("SUM(ut.%s) AS \"%s\"", col, alias))
				}
			} else {
				cfg.Logger.Warn("Invalid client column ignored", "column", col)
			}
		}
		if len(clientCols) == 0 {
			cfg.Logger.Warn("No valid columns specified for aggregate client stats")
			appendStats(builder, "No valid columns specified for aggregate client stats.\n")
			return nil
		}

		clientSortBy := cfg.StatsColumns.Client.SortBy
		if sortBy != "" {
			clientSortBy = sortBy
		}

		clientSortOrder := cfg.StatsColumns.Client.SortOrder
		if sortOrder != "" {
			clientSortOrder = sortOrder
		}

		clientQuery := fmt.Sprintf(`
			SELECT %s 
			FROM user_traffic ut 
			LEFT JOIN user_data ud ON ut.user = ud.user 
			GROUP BY ut.user 
			ORDER BY %s %s;`,
			strings.Join(clientCols, ", "), clientSortBy, clientSortOrder)

		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing aggregate client stats query", "query", clientQuery)
			rows, err := db.Query(clientQuery)
			if err != nil {
				cfg.Logger.Error("Failed to execute aggregate client stats query", "error", err)
				return fmt.Errorf("failed to execute aggregate client stats query: %v", err)
			}
			defer rows.Close()

			columns, err := rows.Columns()
			if err != nil {
				cfg.Logger.Error("Failed to get columns from aggregate client stats query", "error", err)
				return fmt.Errorf("failed to get columns: %v", err)
			}
			cfg.Logger.Debug("Aggregate client stats columns returned", "columns", columns)

			appendStats(builder, "➤  Aggregate Client Statistics:\n")
			clientTable, err := formatTable(rows, clientAliases, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to format aggregate client stats table", "error", err)
				return fmt.Errorf("failed to format aggregate client stats table: %v", err)
			}
			if clientTable == "" {
				cfg.Logger.Warn("No data returned for aggregate client stats query")
				appendStats(builder, "No aggregate client statistics available.\n")
			} else {
				appendStats(builder, clientTable)
			}
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing aggregate client stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for aggregate client stats in configuration")
		appendStats(builder, "No columns specified for aggregate client stats.\n")
	}
	return nil
}

// AggregateStatsHandler handles requests to /api/v1/aggregate_stats.
func AggregateStatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting AggregateStatsHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		sortBy := r.URL.Query().Get("sort_by")
		validSortColumns := []string{
			"user", "last_seen", "rate", "uplink", "downlink", "sess_uplink", "sess_downlink", "created",
		}
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

		if err := buildAggregateServerStats(&statsBuilder, manager, cfg); err != nil {
			cfg.Logger.Error("Failed to retrieve aggregate server statistics", "error", err)
			http.Error(w, "Error retrieving aggregate server statistics", http.StatusInternalServerError)
			return
		}

		if err := buildAggregateClientStats(&statsBuilder, manager, cfg, sortBy, sortOrder); err != nil {
			cfg.Logger.Error("Failed to retrieve aggregate client statistics", "error", err)
			http.Error(w, "Error retrieving aggregate client statistics", http.StatusInternalServerError)
			return
		}

		if statsBuilder.String() == "" {
			cfg.Logger.Warn("No aggregate statistics available")
			fmt.Fprintln(w, "No aggregate statistics available.")
			return
		}

		cfg.Logger.Debug("Writing response", "response_length", len(statsBuilder.String()))
		fmt.Fprintln(w, statsBuilder.String())
		cfg.Logger.Info("API aggregate stats: completed successfully", "sort_by", sortBy, "sort_order", sortOrder)
	}
}
