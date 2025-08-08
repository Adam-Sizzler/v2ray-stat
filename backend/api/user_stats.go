package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/util"
)

// StatsHandler остаётся без изменений
func StatsHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting StatsHandler request processing")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		// Получение параметров запроса
		nodeParam := r.URL.Query().Get("node")
		userParam := r.URL.Query().Get("user")
		sortBy := r.URL.Query().Get("sort_by")
		sortOrder := r.URL.Query().Get("sort_order")
		aggregate := r.URL.Query().Get("aggregate") == "true"

		// Валидация sort_by
		validSortColumns := []string{
			"node_name", "user", "last_seen", "rate", "uplink", "downlink", "sess_uplink", "sess_downlink", "created",
			"inbound_tag", "uuid", // Добавляем новые колонки для сортировки
		}
		if aggregate {
			validSortColumns = []string{
				"user", "last_seen", "rate", "uplink", "downlink", "sess_uplink", "sess_downlink", "created",
				"inbound_tag", "uuid", // Добавляем новые колонки для агрегированной сортировки
			}
		}
		if sortBy != "" && !util.Contains(validSortColumns, sortBy) {
			cfg.Logger.Warn("Invalid sort_by parameter", "sort_by", sortBy)
			http.Error(w, fmt.Sprintf("Invalid sort_by parameter: %s, must be one of %v", sortBy, validSortColumns), http.StatusBadRequest)
			return
		}

		// Валидация sort_order
		if sortOrder != "" && sortOrder != "ASC" && sortOrder != "DESC" {
			cfg.Logger.Warn("Invalid sort_order parameter", "sort_order", sortOrder)
			http.Error(w, fmt.Sprintf("Invalid sort_order parameter: %s, must be ASC or DESC", sortOrder), http.StatusBadRequest)
			return
		}

		var statsBuilder strings.Builder

		// Построение серверной статистики (без изменений)
		if err := buildCustomServerStats(&statsBuilder, manager, cfg, nodeParam, aggregate); err != nil {
			cfg.Logger.Error("Failed to retrieve server statistics", "error", err)
			http.Error(w, "Error retrieving server statistics", http.StatusInternalServerError)
			return
		}

		// Построение клиентской статистики
		if err := buildCustomClientStats(&statsBuilder, manager, cfg, nodeParam, userParam, sortBy, sortOrder, aggregate); err != nil {
			cfg.Logger.Error("Failed to retrieve client statistics", "error", err)
			http.Error(w, "Error retrieving client statistics", http.StatusInternalServerError)
			return
		}

		if statsBuilder.String() == "" {
			cfg.Logger.Warn("No statistics available")
			fmt.Fprintln(w, "No statistics available.")
			return
		}

		cfg.Logger.Debug("Writing response", "response_length", len(statsBuilder.String()))
		fmt.Fprintln(w, statsBuilder.String())
		cfg.Logger.Info("API stats: completed successfully", "node", nodeParam, "user", userParam, "sort_by", sortBy, "sort_order", sortOrder, "aggregate", aggregate)
	}
}

// buildCustomServerStats (без изменений)
func buildCustomServerStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, nodeParam string, aggregate bool) error {
	cfg.Logger.Debug("Collecting server statistics", "node", nodeParam, "aggregate", aggregate)

	serverColumnAliases := map[string]string{
		"node_name":     "Node",
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
				if aggregate && col == "node_name" {
					continue
				}
				if aggregate && col != "source" && col != "node_name" {
					serverCols = append(serverCols, fmt.Sprintf("SUM(%s) AS \"%s\"", col, alias))
				} else {
					serverCols = append(serverCols, fmt.Sprintf("%s AS \"%s\"", col, alias))
				}
			} else {
				cfg.Logger.Warn("Invalid server column ignored", "column", col)
			}
		}
		if len(serverCols) == 0 {
			cfg.Logger.Warn("No valid columns specified for server stats")
			return nil
		}

		serverQuery := fmt.Sprintf("SELECT %s FROM bound_traffic", strings.Join(serverCols, ", "))
		var whereClauses []string
		if nodeParam != "" {
			nodes := strings.Split(nodeParam, ",")
			for i, node := range nodes {
				nodes[i] = fmt.Sprintf("'%s'", strings.TrimSpace(node))
			}
			whereClauses = append(whereClauses, fmt.Sprintf("node_name IN (%s)", strings.Join(nodes, ", ")))
		}
		if len(whereClauses) > 0 {
			serverQuery += " WHERE " + strings.Join(whereClauses, " AND ")
		}
		if aggregate {
			serverQuery += fmt.Sprintf(" GROUP BY source ORDER BY SUM(%s) %s;", cfg.StatsColumns.Server.SortBy, cfg.StatsColumns.Server.SortOrder)
		} else {
			serverQuery += fmt.Sprintf(" ORDER BY %s %s;", cfg.StatsColumns.Server.SortBy, cfg.StatsColumns.Server.SortOrder)
		}

		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing server stats query", "query", serverQuery)
			rows, err := db.Query(serverQuery)
			if err != nil {
				cfg.Logger.Error("Failed to execute server stats query", "error", err)
				return fmt.Errorf("failed to execute server stats query: %v", err)
			}
			defer rows.Close()

			rowCount := 0
			for rows.Next() {
				rowCount++
			}
			cfg.Logger.Debug("Server stats query returned rows", "count", rowCount)
			rows, err = db.Query(serverQuery)
			if err != nil {
				cfg.Logger.Error("Failed to re-execute server stats query", "error", err)
				return fmt.Errorf("failed to re-execute server stats query: %v", err)
			}
			defer rows.Close()

			util.AppendStats(builder, "➤  Server Statistics:\n")
			serverTable, err := util.FormatTable(rows, trafficAliases, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to format server stats table", "error", err)
				return fmt.Errorf("failed to format server stats table: %v", err)
			}
			if serverTable == "" {
				cfg.Logger.Warn("No data returned for server stats query")
				util.AppendStats(builder, "No server statistics available.\n")
			} else {
				util.AppendStats(builder, serverTable)
				util.AppendStats(builder, "\n")
			}
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing server stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for server stats in configuration")
		util.AppendStats(builder, "No columns specified for server stats.\n")
	}
	return nil
}

// buildCustomClientStats collects client statistics with node, user, inbound_tag, and uuid.
func buildCustomClientStats(builder *strings.Builder, manager *manager.DatabaseManager, cfg *config.Config, nodeParam, userParam, sortBy, sortOrder string, aggregate bool) error {
	cfg.Logger.Debug("Collecting client statistics", "node", nodeParam, "user", userParam, "aggregate", aggregate)

	clientColumnAliases := map[string]string{
		"node_name":     "Node",
		"user":          "User",
		"last_seen":     "Last seen",
		"rate":          "Rate",
		"uplink":        "Uplink",
		"downlink":      "Downlink",
		"sess_uplink":   "Sess Up",
		"sess_downlink": "Sess Down",
		"sub_end":       "Sub end",
		"renew":         "Renew",
		"lim_ip":        "Lim",
		"ips":           "Ips",
		"created":       "Created",
		"enabled":       "Enabled",
		"inbound_tag":   "Inbound Tag",
		"uuid":          "UUID",
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
				if aggregate && col == "node_name" {
					continue // Пропускаем node_name для агрегированной статистики
				}
				switch col {
				case "user", "last_seen", "created":
					if aggregate {
						if col == "last_seen" {
							clientCols = append(clientCols, fmt.Sprintf("MAX(ut.%s) AS \"%s\"", col, alias))
						} else {
							clientCols = append(clientCols, fmt.Sprintf("MIN(ut.%s) AS \"%s\"", col, alias))
						}
					} else {
						clientCols = append(clientCols, fmt.Sprintf("ut.%s AS \"%s\"", col, alias))
					}
				case "sub_end", "renew", "lim_ip", "ips", "enabled":
					if aggregate {
						clientCols = append(clientCols, fmt.Sprintf("MIN(ud.%s) AS \"%s\"", col, alias))
					} else {
						clientCols = append(clientCols, fmt.Sprintf("ud.%s AS \"%s\"", col, alias))
					}
				case "inbound_tag", "uuid":
					if aggregate {
						clientCols = append(clientCols, fmt.Sprintf("MIN(uu.%s) AS \"%s\"", col, alias))
					} else {
						clientCols = append(clientCols, fmt.Sprintf("uu.%s AS \"%s\"", col, alias))
					}
				default:
					if aggregate {
						clientCols = append(clientCols, fmt.Sprintf("SUM(ut.%s) AS \"%s\"", col, alias))
					} else {
						clientCols = append(clientCols, fmt.Sprintf("ut.%s AS \"%s\"", col, alias))
					}
				}
			} else {
				cfg.Logger.Warn("Invalid client column ignored", "column", col)
			}
		}
		if len(clientCols) == 0 {
			cfg.Logger.Warn("No valid columns specified for client stats")
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

		// Формируем запрос с LEFT JOIN на user_uuids
		clientQuery := fmt.Sprintf("SELECT %s FROM user_traffic ut LEFT JOIN user_data ud ON ut.user = ud.user LEFT JOIN user_uuids uu ON ut.user = uu.user AND ut.node_name = uu.node_name", strings.Join(clientCols, ", "))
		var whereClauses []string
		if nodeParam != "" {
			nodes := strings.Split(nodeParam, ",")
			for i, node := range nodes {
				nodes[i] = fmt.Sprintf("'%s'", strings.TrimSpace(node))
			}
			whereClauses = append(whereClauses, fmt.Sprintf("ut.node_name IN (%s)", strings.Join(nodes, ", ")))
		}
		if userParam != "" {
			users := strings.Split(userParam, ",")
			for i, user := range users {
				users[i] = fmt.Sprintf("'%s'", strings.TrimSpace(user))
			}
			whereClauses = append(whereClauses, fmt.Sprintf("ut.user IN (%s)", strings.Join(users, ", ")))
		}
		if len(whereClauses) > 0 {
			clientQuery += " WHERE " + strings.Join(whereClauses, " AND ")
		}
		if aggregate {
			clientQuery += fmt.Sprintf(" GROUP BY ut.user ORDER BY %s %s;", clientSortBy, clientSortOrder)
		} else {
			clientQuery += fmt.Sprintf(" ORDER BY %s %s;", clientSortBy, clientSortOrder)
		}

		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing client stats query", "query", clientQuery)
			rows, err := db.Query(clientQuery)
			if err != nil {
				cfg.Logger.Error("Failed to execute client stats query", "error", err)
				return fmt.Errorf("failed to execute client stats query: %v", err)
			}
			defer rows.Close()

			rowCount := 0
			for rows.Next() {
				rowCount++
			}
			cfg.Logger.Debug("Client stats query returned rows", "count", rowCount)
			rows, err = db.Query(clientQuery)
			if err != nil {
				cfg.Logger.Error("Failed to re-execute client stats query", "error", err)
				return fmt.Errorf("failed to re-execute client stats query: %v", err)
			}
			defer rows.Close()

			if aggregate {
				util.AppendStats(builder, "➤  Aggregate Client Statistics:\n")
			} else {
				util.AppendStats(builder, "➤  Client Statistics:\n")
			}
			clientTable, err := util.FormatTable(rows, clientAliases, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to format client stats table", "error", err)
				return fmt.Errorf("failed to format client stats table: %v", err)
			}
			if clientTable == "" {
				cfg.Logger.Warn("No data returned for client stats query")
				if aggregate {
					util.AppendStats(builder, "No aggregate client statistics available.\n")
				} else {
					util.AppendStats(builder, "No client statistics available.\n")
				}
			} else {
				util.AppendStats(builder, clientTable)
			}
			return nil
		})

		if err != nil {
			cfg.Logger.Error("Error processing client stats", "error", err)
			return err
		}
	} else {
		cfg.Logger.Warn("No columns specified for client stats in configuration")
		util.AppendStats(builder, "No columns specified for client stats.\n")
	}
	return nil
}
