package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"v2ray-stat/api"
	"v2ray-stat/config"
	"v2ray-stat/constant"
	"v2ray-stat/db"
	"v2ray-stat/db/manager"
	"v2ray-stat/monitor"
	"v2ray-stat/stats"

	_ "github.com/mattn/go-sqlite3"
)

var (
	uniqueEntries       = make(map[string]map[string]time.Time)
	uniqueEntriesMutex  sync.Mutex
	previousStats       string
	clientPreviousStats string

	// Хранит статус неактивности пользователя
	isInactive      = make(map[string]bool)
	isInactiveMutex sync.Mutex

	timeLocation *time.Location
)

// initTimezone sets the application's timezone based on configuration.
func initTimezone(cfg *config.Config) {
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			cfg.Logger.Warn("Invalid TIMEZONE, using system default", "timezone", cfg.Timezone, "error", err)
			timeLocation = time.Local
		} else {
			timeLocation = loc
		}
	} else {
		timeLocation = time.Local
	}
}

// extractProxyTraffic filters and formats proxy traffic stats from API response.
func extractProxyTraffic(apiData *api.ApiResponse) []string {
	var result []string
	for _, stat := range apiData.Stat {
		if strings.Contains(stat.Name, "user") || strings.Contains(stat.Name, "api") || strings.Contains(stat.Name, "block") {
			continue
		}
		parts := splitAndCleanName(stat.Name)
		if len(parts) > 0 {
			result = append(result, fmt.Sprintf("%s %s", strings.Join(parts, " "), stat.Value))
		}
	}
	return result
}

// extractUserTraffic filters and formats user traffic stats from API response.
func extractUserTraffic(apiData *api.ApiResponse) []string {
	var result []string
	for _, stat := range apiData.Stat {
		if strings.Contains(stat.Name, "user") {
			parts := splitAndCleanName(stat.Name)
			if len(parts) > 0 {
				result = append(result, fmt.Sprintf("%s %s", strings.Join(parts, " "), stat.Value))
			}
		}
	}
	return result
}

// splitAndCleanName splits the V2Ray stat name and returns components.
func splitAndCleanName(name string) []string {
	parts := strings.Split(name, ">>>")
	if len(parts) == 4 {
		return []string{parts[1], parts[3]}
	}
	return nil
}

// updateProxyStats updates proxy traffic statistics in the database.
func updateProxyStats(manager *manager.DatabaseManager, apiData *api.ApiResponse, cfg *config.Config) {
	cfg.Logger.Debug("Starting proxy stats update")
	currentStats := extractProxyTraffic(apiData)
	if previousStats == "" {
		previousStats = strings.Join(currentStats, "\n")
		cfg.Logger.Debug("Initialized previousStats", "count", len(currentStats))
		return
	}

	currentValues := make(map[string]int)
	previousValues := make(map[string]int)

	// Parse current stats
	for _, line := range currentStats {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Parsing current stats line", "line", line)
			currentValues[parts[0]+" "+parts[1]] = stringToInt(cfg, parts[2])
		} else {
			cfg.Logger.Warn("Invalid stats line format", "line", line)
		}
	}

	// Parse previous stats
	for _, line := range strings.Split(previousStats, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Parsing previous stats line", "line", line)
			previousValues[parts[0]+" "+parts[1]] = stringToInt(cfg, parts[2])
		}
	}

	uplinkValues := make(map[string]int)
	downlinkValues := make(map[string]int)
	sessUplinkValues := make(map[string]int)
	sessDownlinkValues := make(map[string]int)

	for key, current := range currentValues {
		previous, exists := previousValues[key]
		if !exists {
			cfg.Logger.Warn("Missing previous data for key", "key", key)
			previous = 0
		}
		diff := max(current-previous, 0)
		parts := strings.Fields(key)
		source := parts[0]
		direction := parts[1]

		switch direction {
		case "uplink":
			uplinkValues[source] = diff
			sessUplinkValues[source] = current
		case "downlink":
			downlinkValues[source] = diff
			sessDownlinkValues[source] = current
		}
	}

	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to begin transaction", "error", err)
			return err
		}
		defer tx.Rollback()

		for source := range uplinkValues {
			uplink := uplinkValues[source]
			downlink := downlinkValues[source]
			sessUplink := sessUplinkValues[source]
			sessDownlink := sessDownlinkValues[source]
			previousUplink, uplinkExists := previousValues[source+" uplink"]
			previousDownlink, downlinkExists := previousValues[source+" downlink"]

			if !uplinkExists {
				cfg.Logger.Warn("Missing previous uplink data", "source", source)
				previousUplink = 0
			}
			if !downlinkExists {
				cfg.Logger.Warn("Missing previous downlink data", "source", source)
				previousDownlink = 0
			}

			uplinkOnline := max(sessUplink-previousUplink, 0)
			downlinkOnline := max(sessDownlink-previousDownlink, 0)
			rate := (uplinkOnline + downlinkOnline) * 8 / cfg.V2rayStat.Monitor.TickerInterval

			cfg.Logger.Debug("Updating proxy stats", "source", source, "rate", rate, "uplink", uplink, "downlink", downlink)

			_, err := tx.Exec(`
				INSERT INTO traffic_stats (source, rate, uplink, downlink, sess_uplink, sess_downlink)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(source) DO UPDATE SET
					rate = ?,
					uplink = uplink + ?,
					downlink = downlink + ?,
					sess_uplink = ?,
					sess_downlink = ?`,
				source, rate, uplink, downlink, sessUplink, sessDownlink,
				rate, uplink, downlink, sessUplink, sessDownlink)
			if err != nil {
				cfg.Logger.Error("Failed to update traffic_stats", "source", source, "error", err)
				return err
			}
		}

		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return err
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Failed to update proxy stats", "error", err)
		return
	}

	cfg.Logger.Debug("Finished proxy stats update", "entries", len(currentStats))
	previousStats = strings.Join(currentStats, "\n")
}

// updateClientStats updates client traffic statistics in the database.
func updateClientStats(manager *manager.DatabaseManager, apiData *api.ApiResponse, cfg *config.Config) {
	cfg.Logger.Debug("Starting client stats update")

	clientCurrentStats := extractUserTraffic(apiData)
	if clientPreviousStats == "" {
		clientPreviousStats = strings.Join(clientCurrentStats, "\n")
		cfg.Logger.Debug("Initialized clientPreviousStats", "stats_count", len(clientCurrentStats))
		return
	}

	clientCurrentValues := make(map[string]int)
	clientPreviousValues := make(map[string]int)

	for _, line := range clientCurrentStats {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Processing client stats line", "line", line)
			clientCurrentValues[parts[0]+" "+parts[1]] = stringToInt(cfg, parts[2])
		} else {
			cfg.Logger.Warn("Invalid client stats line format", "line", line)
		}
	}

	previousLines := strings.SplitSeq(clientPreviousStats, "\n")
	for line := range previousLines {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Processing previous client stats line", "line", line)
			clientPreviousValues[parts[0]+" "+parts[1]] = stringToInt(cfg, parts[2])
		}
	}

	clientUplinkValues := make(map[string]int)
	clientDownlinkValues := make(map[string]int)
	clientSessUplinkValues := make(map[string]int)
	clientSessDownlinkValues := make(map[string]int)

	for key, current := range clientCurrentValues {
		previous, exists := clientPreviousValues[key]
		if !exists {
			cfg.Logger.Warn("Missing previous data for key", "key", key)
			previous = 0
		}
		diff := max(current-previous, 0)
		parts := strings.Fields(key)
		user := parts[0]
		direction := parts[1]

		switch direction {
		case "uplink":
			clientUplinkValues[user] = diff
			clientSessUplinkValues[user] = current
		case "downlink":
			clientDownlinkValues[user] = diff
			clientSessDownlinkValues[user] = current
		}
	}

	for key := range clientPreviousValues {
		parts := strings.Fields(key)
		if len(parts) != 2 {
			cfg.Logger.Warn("Invalid key format in previous stats", "key", key)
			continue
		}
		user := parts[0]
		direction := parts[1]

		switch direction {
		case "uplink":
			if _, exists := clientSessUplinkValues[user]; !exists {
				cfg.Logger.Debug("Setting zero values for uplink", "user", user)
				clientSessUplinkValues[user] = 0
				clientUplinkValues[user] = 0
			}
		case "downlink":
			if _, exists := clientSessDownlinkValues[user]; !exists {
				cfg.Logger.Debug("Setting zero values for downlink", "user", user)
				clientSessDownlinkValues[user] = 0
				clientDownlinkValues[user] = 0
			}
		}
	}

	currentTime := time.Now().In(timeLocation)
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to begin transaction", "error", err)
			return fmt.Errorf("failed to begin transaction: %v", err)
		}
		defer tx.Rollback()

		isInactiveMutex.Lock()
		defer isInactiveMutex.Unlock()

		for user := range clientUplinkValues {
			uplink := clientUplinkValues[user]
			downlink := clientDownlinkValues[user]
			sessUplink := clientSessUplinkValues[user]
			sessDownlink := clientSessDownlinkValues[user]
			previousUplink, uplinkExists := clientPreviousValues[user+" uplink"]
			previousDownlink, downlinkExists := clientPreviousValues[user+" downlink"]

			if !uplinkExists {
				cfg.Logger.Warn("Missing previous uplink data", "user", user)
				previousUplink = 0
			}
			if !downlinkExists {
				cfg.Logger.Warn("Missing previous downlink data", "user", user)
				previousDownlink = 0
			}

			uplinkOnline := max(sessUplink-previousUplink, 0)
			downlinkOnline := max(sessDownlink-previousDownlink, 0)
			rate := (uplinkOnline + downlinkOnline) * 8 / cfg.V2rayStat.Monitor.TickerInterval

			cfg.Logger.Debug("Updating stats for client", "user", user, "rate", rate, "uplink", uplink, "downlink", downlink)

			var lastSeen string
			if rate > cfg.V2rayStat.Monitor.OnlineRateThreshold*1000 {
				lastSeen = "online"
				isInactive[user] = false
				cfg.Logger.Debug("Client is active", "user", user)
			} else {
				if !isInactive[user] {
					lastSeen = currentTime.Truncate(time.Minute).Format("2006-01-02 15:04")
					isInactive[user] = true
					cfg.Logger.Debug("Client transitioned to inactive state", "user", user, "last_seen", lastSeen)
				}
			}

			if lastSeen != "" {
				_, err := tx.Exec(`
					UPDATE clients_stats 
					SET last_seen = ?, rate = ?, uplink = uplink + ?, downlink = downlink + ?, sess_uplink = ?, sess_downlink = ?
					WHERE user = ? AND EXISTS (
						SELECT 1 FROM clients_stats WHERE user = ?
					)`,
					lastSeen, rate, uplink, downlink, sessUplink, sessDownlink, user, user)
				if err != nil {
					cfg.Logger.Error("Failed to execute query for client", "user", user, "error", err)
					return fmt.Errorf("failed to execute query for %s: %v", user, err)
				}
			} else {
				_, err := tx.Exec(`
					UPDATE clients_stats 
					SET rate = ?, uplink = uplink + ?, downlink = downlink + ?, sess_uplink = ?, sess_downlink = ?
					WHERE user = ? AND EXISTS (
						SELECT 1 FROM clients_stats WHERE user = ?
					)`,
					rate, uplink, downlink, sessUplink, sessDownlink, user, user)
				if err != nil {
					cfg.Logger.Error("Failed to execute query for client", "user", user, "error", err)
					return fmt.Errorf("failed to execute query for %s: %v", user, err)
				}
			}
		}

		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return err
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("SQL error in updateClientStats", "error", err)
		return
	}

	clientPreviousStats = strings.Join(clientCurrentStats, "\n")
	cfg.Logger.Debug("Client stats successfully updated", "stats_count", len(clientCurrentStats))
}

func stringToInt(cfg *config.Config, s string) int {
	result, err := strconv.Atoi(s)
	if err != nil {
		cfg.Logger.Warn("Failed to convert string to integer", "string", s, "error", err)
		return 0
	}
	return result
}

func processLogLine(line string, dnsStats map[string]map[string]int, cfg *config.Config) (string, []string, bool) {
	matches := regexp.MustCompile(cfg.Core.AccessLogRegex).FindStringSubmatch(line)
	if len(matches) != 3 && len(matches) != 4 {
		return "", nil, false
	}

	var user, domain, ip string
	if len(matches) == 4 {
		ip = matches[1]
		domain = strings.TrimSpace(matches[2])
		user = strings.TrimSpace(matches[3])
	} else {
		user = strings.TrimSpace(matches[1])
		ip = strings.TrimSpace(matches[2])
		domain = ""
	}

	uniqueEntriesMutex.Lock()
	if uniqueEntries[user] == nil {
		uniqueEntries[user] = make(map[string]time.Time)
	}
	uniqueEntries[user][ip] = time.Now()

	validIPs := []string{}
	for ip, timestamp := range uniqueEntries[user] {
		if time.Since(timestamp) <= 66*time.Second {
			validIPs = append(validIPs, ip)
		}
	}
	uniqueEntriesMutex.Unlock()

	if dnsStats[user] == nil {
		dnsStats[user] = make(map[string]int)
	}
	if domain != "" {
		dnsStats[user][domain]++
	}

	return user, validIPs, true
}

// readNewLines reads new lines from the log file and updates statistics in the database.
func readNewLines(manager *manager.DatabaseManager, file *os.File, offset *int64, cfg *config.Config) {
	cfg.Logger.Debug("Starting processing of new log lines")

	file.Seek(*offset, 0)
	scanner := bufio.NewScanner(file)

	dnsStats := make(map[string]map[string]int)
	ipUpdates := make(map[string][]string)

	for scanner.Scan() {
		line := scanner.Text()
		cfg.Logger.Debug("Processing log line", "line", line)
		user, validIPs, ok := processLogLine(line, dnsStats, cfg)
		if ok {
			cfg.Logger.Trace("Retrieved data for user", "user", user, "valid_ips_count", len(validIPs))
			ipUpdates[user] = validIPs
		} else {
			cfg.Logger.Debug("Invalid regex for line", "line", line)
		}
	}

	if err := scanner.Err(); err != nil {
		cfg.Logger.Error("Error reading log file", "error", err)
		return
	}

	cfg.Logger.Debug("Processed log lines", "ip_updates_count", len(ipUpdates), "dns_stats_count", len(dnsStats))

	for user, validIPs := range ipUpdates {
		cfg.Logger.Debug("Updating IPs for user", "user", user, "ips", validIPs)
		if err := db.UpdateIPInDB(manager, user, validIPs, cfg); err != nil {
			cfg.Logger.Error("Failed to update IPs in database", "user", user, "error", err)
			return
		}
	}

	if len(dnsStats) > 0 {
		for user, domains := range dnsStats {
			cfg.Logger.Trace("Updating DNS records for user", "user", user, "domains", domains)
		}
		if err := db.UpsertDNSRecordsBatch(manager, dnsStats, cfg); err != nil {
			cfg.Logger.Error("Failed to update dns_stats", "error", err)
			return
		}
	} else {
		cfg.Logger.Debug("No DNS records to update")
	}

	pos, err := file.Seek(0, 1)
	if err != nil {
		cfg.Logger.Error("Error getting file position", "error", err)
		return
	}
	*offset = pos
	cfg.Logger.Debug("Finished processing new log lines", "offset", pos)
}

// monitorUsersAndLogs starts the task of monitoring users and logs.
func monitorUsersAndLogs(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		accessLog, err := os.OpenFile(cfg.Core.AccessLog, os.O_RDONLY|os.O_CREATE, 0644)
		if err != nil {
			cfg.Logger.Error("Failed to open log file", "file", cfg.Core.AccessLog, "error", err)
			return
		}
		defer accessLog.Close()

		var accessOffset int64
		accessLog.Seek(0, 2)
		accessOffset, err = accessLog.Seek(0, 1)
		if err != nil {
			cfg.Logger.Error("Error getting log file position", "error", err)
			return
		}
		cfg.Logger.Info("Initialized log monitoring", "file", cfg.Core.AccessLog, "offset", accessOffset)

		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()

		dailyTicker := time.NewTicker(24 * time.Hour)
		defer dailyTicker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := db.AddUserToDB(manager, cfg); err != nil {
					cfg.Logger.Error("Failed to add users", "error", err)
				}
				if err := db.DelUserFromDB(manager, cfg); err != nil {
					cfg.Logger.Error("Failed to delete users", "error", err)
				}

				apiData, err := api.GetApiResponse(cfg)
				if err != nil {
					cfg.Logger.Error("Failed to retrieve API data", "error", err)
				} else {
					updateProxyStats(manager, apiData, cfg)
					updateClientStats(manager, apiData, cfg)
				}
				readNewLines(manager, accessLog, &accessOffset, cfg)

			case <-dailyTicker.C:
				if err := accessLog.Close(); err != nil {
					cfg.Logger.Error("Error closing log file", "file", cfg.Core.AccessLog, "error", err)
				}
				accessLog, err = os.OpenFile(cfg.Core.AccessLog, os.O_RDONLY|os.O_CREATE|os.O_TRUNC, 0644)
				if err != nil {
					cfg.Logger.Error("Error reopening log file after truncation", "file", cfg.Core.AccessLog, "error", err)
					return
				}
				accessOffset = 0
				cfg.Logger.Info("Log file successfully truncated", "file", cfg.Core.AccessLog)

			case <-ctx.Done():
				cfg.Logger.Debug("Log monitoring stopped")
				return
			}
		}
	}()
}

// withServerHeader adds a Server header to all responses.
func withServerHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHeader := fmt.Sprintf("MuxCloud/%s (WebServer)", constant.Version)
		w.Header().Set("Server", serverHeader)
		w.Header().Set("X-Powered-By", "MuxCloud")
		next.ServeHTTP(w, r)
	})
}

// startAPIServer starts the API server.
func startAPIServer(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config, wg *sync.WaitGroup) {
	server := &http.Server{
		Addr:    cfg.V2rayStat.Address + ":" + cfg.V2rayStat.Port,
		Handler: withServerHeader(http.DefaultServeMux),
	}

	// Placeholder
	http.HandleFunc("/", api.Answer())

	// Read-only endpoints (no token required)
	http.HandleFunc("/api/v1/users", api.UsersHandler(manager, cfg))
	http.HandleFunc("/api/v1/stats", api.StatsCustomHandler(manager, cfg))
	http.HandleFunc("/api/v1/stats/base", api.StatsHandler(manager, cfg))
	http.HandleFunc("/api/v1/dns_stats", api.DnsStatsHandler(manager, cfg))

	// Data-modifying endpoints (token required)
	http.HandleFunc("/api/v1/add_user", api.TokenAuthMiddleware(cfg, api.AddUserHandler(cfg)))
	http.HandleFunc("/api/v1/bulk_add_users", api.TokenAuthMiddleware(cfg, api.BulkAddUsersHandler(cfg)))
	http.HandleFunc("/api/v1/delete_user", api.TokenAuthMiddleware(cfg, api.DeleteUserHandler(cfg)))
	http.HandleFunc("/api/v1/set_enabled", api.TokenAuthMiddleware(cfg, api.SetEnabledHandler(manager, cfg)))
	http.HandleFunc("/api/v1/update_lim_ip", api.TokenAuthMiddleware(cfg, api.UpdateIPLimitHandler(manager, cfg)))
	http.HandleFunc("/api/v1/adjust_date", api.TokenAuthMiddleware(cfg, api.AdjustDateOffsetHandler(manager, cfg)))
	http.HandleFunc("/api/v1/update_renew", api.TokenAuthMiddleware(cfg, api.UpdateRenewHandler(manager, cfg)))
	http.HandleFunc("/api/v1/delete_dns_stats", api.TokenAuthMiddleware(cfg, api.DeleteDNSStatsHandler(manager, cfg)))
	http.HandleFunc("/api/v1/reset_traffic", api.TokenAuthMiddleware(cfg, api.ResetTrafficHandler(cfg)))
	http.HandleFunc("/api/v1/reset_traffic_stats", api.TokenAuthMiddleware(cfg, api.ResetTrafficStatsHandler(manager, cfg)))
	http.HandleFunc("/api/v1/reset_clients_stats", api.TokenAuthMiddleware(cfg, api.ResetClientsStatsHandler(manager, cfg)))

	cfg.Logger.Debug("Starting API server", "address", server.Addr)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			cfg.Logger.Fatal("Failed to start server", "error", err)
		}
	}()

	<-ctx.Done()

	cfg.Logger.Debug("Shutting down API server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		cfg.Logger.Error("Error shutting down server", "error", err)
	}
	wg.Done()
}

func main() {
	// Load configuration
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}
	initTimezone(&cfg)

	// Setup context and signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize database
	memDB, fileDB, err := db.InitDatabase(&cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to initialize database", "error", err)
	}
	defer memDB.Close()
	defer fileDB.Close()

	manager, err := manager.NewDatabaseManager(memDB, ctx, 1, 300, 500, &cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to create DatabaseManager", "error", err)
	}

	isInactive, err = db.LoadIsInactiveFromLastSeen(manager, &cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to load initial status", "error", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start tasks
	var wg sync.WaitGroup
	wg.Add(1)
	go startAPIServer(ctx, manager, &cfg, &wg)
	monitorUsersAndLogs(ctx, manager, &cfg, &wg)
	db.MonitorSubscriptionsAndSync(ctx, manager, fileDB, &cfg, &wg)
	monitor.MonitorExcessIPs(ctx, manager, &cfg, &wg)
	monitor.MonitorBannedLog(ctx, &cfg, &wg)

	if cfg.Features["network"] {
		if err := stats.InitNetworkMonitoring(&cfg); err != nil {
			cfg.Logger.Error("Failed to initialize network monitoring", "error", err)
		}
		stats.MonitorNetwork(ctx, &cfg, &wg)
	}

	if cfg.Features["telegram"] {
		stats.MonitorDailyReport(ctx, manager, &cfg, &wg)
		stats.MonitorStats(ctx, &cfg, &wg)
	}

	log.Printf("[START] v2ray-stat application %s, with core: %s", constant.Version, cfg.V2rayStat.Type)

	// Wait for termination signal
	<-sigChan
	cfg.Logger.Info("Received termination signal, saving data")
	cancel()
	wg.Wait()

	// Ensure file database exists before final synchronization
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer syncCancel()
	if _, err := os.Stat(cfg.Paths.Database); os.IsNotExist(err) {
		cfg.Logger.Warn("File database does not exist, recreating", "path", cfg.Paths.Database)
		if fileDB, err = db.OpenAndInitDB(cfg.Paths.Database, "file", &cfg); err != nil {
			cfg.Logger.Error("Failed to recreate file database", "path", cfg.Paths.Database, "error", err)
		} else {
			defer fileDB.Close()
		}
	} else if err != nil {
		cfg.Logger.Error("Failed to check file database existence", "path", cfg.Paths.Database, "error", err)
	}

	// Use a new context for final synchronization with increased timeout
	if err := manager.SyncDBWithContext(syncCtx, fileDB, "memory to file"); err != nil {
		cfg.Logger.Error("Failed to perform final database synchronization", "error", err)
	} else {
		cfg.Logger.Info("Database synchronized successfully (memory to file)")
	}

	// Close manager
	manager.Close()
	log.Printf("[STOP] Program terminated")
}
