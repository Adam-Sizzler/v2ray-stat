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

func initTimezone(cfg *config.Config) {
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			log.Printf("Некорректная TIMEZONE '%s', используется системная: %v", cfg.Timezone, err)
			timeLocation = time.Local
		} else {
			timeLocation = loc
		}
	} else {
		timeLocation = time.Local
	}
}

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

func splitAndCleanName(name string) []string {
	parts := strings.Split(name, ">>>")
	if len(parts) == 4 {
		return []string{parts[1], parts[3]}
	}
	return nil
}

func updateProxyStats(memDB *sql.DB, apiData *api.ApiResponse, dbMutex *sync.Mutex, cfg *config.Config) {
	currentStats := extractProxyTraffic(apiData)
	if previousStats == "" {
		previousStats = strings.Join(currentStats, "\n")
		return
	}

	currentValues := make(map[string]int)
	previousValues := make(map[string]int)

	for _, line := range currentStats {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			currentValues[parts[0]+" "+parts[1]] = stringToInt(parts[2])
		} else {
			log.Printf("Ошибка: неверный формат строки статистики: %s", line)
		}
	}

	previousLines := strings.Split(previousStats, "\n")
	for _, line := range previousLines {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			previousValues[parts[0]+" "+parts[1]] = stringToInt(parts[2])
		}
	}

	uplinkValues := make(map[string]int)
	downlinkValues := make(map[string]int)
	sessUplinkValues := make(map[string]int)
	sessDownlinkValues := make(map[string]int)

	for key, current := range currentValues {
		previous, exists := previousValues[key]
		if !exists {
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

	var queries string
	for source := range uplinkValues {
		uplink := uplinkValues[source]
		downlink := downlinkValues[source]
		sessUplink := sessUplinkValues[source]
		sessDownlink := sessDownlinkValues[source]
		previousUplink, uplinkExists := previousValues[source+" uplink"]
		previousDownlink, downlinkExists := previousValues[source+" downlink"]

		if !uplinkExists {
			previousUplink = 0
		}
		if !downlinkExists {
			previousDownlink = 0
		}

		uplinkOnline := max(sessUplink-previousUplink, 0)
		downlinkOnline := max(sessDownlink-previousDownlink, 0)
		rate := (uplinkOnline + downlinkOnline) * 8 / cfg.V2rayStat.Monitor.TickerInterval

		queries += fmt.Sprintf(`
			INSERT INTO traffic_stats (source, rate, uplink, downlink, sess_uplink, sess_downlink)
			VALUES ('%s', %d, %d, %d, %d, %d)
			ON CONFLICT(source) DO UPDATE SET
				rate = %d,
				uplink = uplink + %d,
				downlink = downlink + %d,
				sess_uplink = %d,
				sess_downlink = %d;
		`, source, rate, uplink, downlink, sessUplink, sessDownlink,
			rate, uplink, downlink, sessUplink, sessDownlink)
	}
	//
	if queries != "" {
		dbMutex.Lock()
		_, err := memDB.Exec(queries)
		dbMutex.Unlock()
		if err != nil {
			log.Printf("Ошибка SQL в updateProxyStats: %v", err)
			return
		}
	}
	previousStats = strings.Join(currentStats, "\n")
}

func updateClientStats(memDB *sql.DB, apiData *api.ApiResponse, dbMutex *sync.Mutex, cfg *config.Config) {
	clientCurrentStats := extractUserTraffic(apiData)
	if clientPreviousStats == "" {
		clientPreviousStats = strings.Join(clientCurrentStats, "\n")
		return
	}

	clientCurrentValues := make(map[string]int)
	clientPreviousValues := make(map[string]int)

	for _, line := range clientCurrentStats {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			clientCurrentValues[parts[0]+" "+parts[1]] = stringToInt(parts[2])
		} else {
			log.Printf("Ошибка: неверный формат строки статистики: %s", line)
		}
	}

	previousLines := strings.Split(clientPreviousStats, "\n")
	for _, line := range previousLines {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			clientPreviousValues[parts[0]+" "+parts[1]] = stringToInt(parts[2])
		}
	}

	clientUplinkValues := make(map[string]int)
	clientDownlinkValues := make(map[string]int)
	clientSessUplinkValues := make(map[string]int)
	clientSessDownlinkValues := make(map[string]int)

	for key, current := range clientCurrentValues {
		previous, exists := clientPreviousValues[key]
		if !exists {
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
			continue
		}
		user := parts[0]
		direction := parts[1]

		switch direction {
		case "uplink":
			if _, exists := clientSessUplinkValues[user]; !exists {
				clientSessUplinkValues[user] = 0
				clientUplinkValues[user] = 0
			}
		case "downlink":
			if _, exists := clientSessDownlinkValues[user]; !exists {
				clientSessDownlinkValues[user] = 0
				clientDownlinkValues[user] = 0
			}
		}
	}

	currentTime := time.Now().In(timeLocation)
	var queries string
	isInactiveMutex.Lock()

	for user := range clientUplinkValues {
		uplink := clientUplinkValues[user]
		downlink := clientDownlinkValues[user]
		sessUplink := clientSessUplinkValues[user]
		sessDownlink := clientSessDownlinkValues[user]
		previousUplink, uplinkExists := clientPreviousValues[user+" uplink"]
		previousDownlink, downlinkExists := clientPreviousValues[user+" downlink"]

		if !uplinkExists {
			previousUplink = 0
		}
		if !downlinkExists {
			previousDownlink = 0
		}

		uplinkOnline := max(sessUplink-previousUplink, 0)
		downlinkOnline := max(sessDownlink-previousDownlink, 0)
		rate := (uplinkOnline + downlinkOnline) * 8 / cfg.V2rayStat.Monitor.TickerInterval

		lastSeen := ""
		if rate > cfg.V2rayStat.Monitor.OnlineRateThreshold*1000 {
			lastSeen = "online"
			isInactive[user] = false
		} else {
			if !isInactive[user] {
				lastSeen = currentTime.Truncate(time.Minute).Format("2006-01-02 15:04")
				isInactive[user] = true
			}
		}

		if lastSeen != "" {
			queries += fmt.Sprintf(`
				INSERT INTO clients_stats (user, last_seen, rate, uplink, downlink, sess_uplink, sess_downlink)
				VALUES ('%s', '%s', %d, %d, %d, %d, %d)
				ON CONFLICT(user) DO UPDATE SET
					last_seen = '%s',
					rate = %d,
					uplink = uplink + %d,
					downlink = downlink + %d,
					sess_uplink = %d,
					sess_downlink = %d;
			`, user, lastSeen, rate, uplink, downlink, sessUplink, sessDownlink,
				lastSeen, rate, uplink, downlink, sessUplink, sessDownlink)
		} else {
			queries += fmt.Sprintf(`
				INSERT INTO clients_stats (user, rate, uplink, downlink, sess_uplink, sess_downlink)
				VALUES ('%s', %d, %d, %d, %d, %d)
				ON CONFLICT(user) DO UPDATE SET
					rate = %d,
					uplink = uplink + %d,
					downlink = downlink + %d,
					sess_uplink = %d,
					sess_downlink = %d;
			`, user, rate, uplink, downlink, sessUplink, sessDownlink,
				rate, uplink, downlink, sessUplink, sessDownlink)
		}
	}

	isInactiveMutex.Unlock()

	if queries != "" {
		dbMutex.Lock()
		_, err := memDB.Exec(queries)
		dbMutex.Unlock()
		if err != nil {
			log.Printf("Ошибка SQL в updateClientStats: %v", err)
			return
		}
	}

	clientPreviousStats = strings.Join(clientCurrentStats, "\n")
}

func stringToInt(s string) int {
	result, err := strconv.Atoi(s)
	if err != nil {
		log.Printf("Error converting string '%s' to integer: %v", s, err)
		return 0
	}
	return result
}

func upsertDNSRecordsBatch(tx *sql.Tx, dnsStats map[string]map[string]int) error {
	for user, domains := range dnsStats {
		for domain, count := range domains {
			_, err := tx.Exec(`
                INSERT INTO dns_stats (user, domain, count) 
                VALUES (?, ?, ?)
                ON CONFLICT(user, domain) 
                DO UPDATE SET count = count + ?`, user, domain, count, count)
			if err != nil {
				log.Printf("Ошибка при пакетном обновлении dns_stats: %v", err)
				return fmt.Errorf("error during batch update of dns_stats: %v", err)
			}
		}
	}
	return nil
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

func readNewLines(memDB *sql.DB, dbMutex *sync.Mutex, file *os.File, offset *int64, cfg *config.Config) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	file.Seek(*offset, 0)
	scanner := bufio.NewScanner(file)

	tx, err := memDB.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции: %v", err)
		return
	}

	dnsStats := make(map[string]map[string]int)
	ipUpdates := make(map[string][]string)

	for scanner.Scan() {
		user, validIPs, ok := processLogLine(scanner.Text(), dnsStats, cfg)
		if ok {
			ipUpdates[user] = validIPs
			// log.Printf("DEBUG: Добавлено в ipUpdates: user=%s, validIPs=%v", user, validIPs)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Ошибка чтения файла: %v", err)
		tx.Rollback()
		return
	}

	// Обновление IP-адресов в базе
	for user, validIPs := range ipUpdates {
		// log.Printf("DEBUG: Вызов UpdateIPInDB для user=%s с validIPs=%v", user, validIPs)
		if err := db.UpdateIPInDB(tx, user, validIPs); err != nil {
			log.Printf("Error updating IP in database: %v", err)
			tx.Rollback()
			return
		}
	}

	// Обновление DNS-записей
	if err := upsertDNSRecordsBatch(tx, dnsStats); err != nil {
		tx.Rollback()
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Ошибка фиксации транзакции: %v", err)
		tx.Rollback()
		return
	}

	pos, err := file.Seek(0, 1)
	if err != nil {
		log.Printf("Ошибка получения позиции файла: %v", err)
		return
	}
	*offset = pos
}

// Запуск задачи мониторинга пользователей и логов
func monitorUsersAndLogs(ctx context.Context, memDB *sql.DB, dbMutex *sync.Mutex, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		accessLog, err := os.OpenFile(cfg.Core.AccessLog, os.O_RDONLY|os.O_CREATE, 0644)
		if err != nil {
			log.Printf("Ошибка открытия файла логов %s: %v", cfg.Core.AccessLog, err)
			return
		}
		defer accessLog.Close()

		var accessOffset int64
		accessLog.Seek(0, 2)
		accessOffset, err = accessLog.Seek(0, 1)
		if err != nil {
			log.Printf("Ошибка получения позиции файла логов: %v", err)
			return
		}

		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()

		dailyTicker := time.NewTicker(24 * time.Hour)
		defer dailyTicker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := db.AddUserToDB(memDB, dbMutex, cfg); err != nil {
					log.Printf("Ошибка добавления пользователей: %v", err)
				}
				if err := db.DelUserFromDB(memDB, dbMutex, cfg); err != nil {
					log.Printf("Ошибка удаления пользователей: %v", err)
				}

				apiData, err := api.GetApiResponse(cfg)
				if err != nil {
					log.Printf("Ошибка получения данных API: %v", err)
				} else {
					updateProxyStats(memDB, apiData, dbMutex, cfg)
					updateClientStats(memDB, apiData, dbMutex, cfg)
				}
				readNewLines(memDB, dbMutex, accessLog, &accessOffset, cfg)

			case <-dailyTicker.C:
				if err := accessLog.Close(); err != nil {
					log.Printf("Ошибка при закрытии файла логов %s: %v", cfg.Core.AccessLog, err)
				}
				accessLog, err = os.OpenFile(cfg.Core.AccessLog, os.O_RDONLY|os.O_CREATE|os.O_TRUNC, 0644)
				if err != nil {
					log.Printf("Ошибка при открытии файла логов %s после очистки: %v", cfg.Core.AccessLog, err)
					return
				}

				accessOffset = 0
				log.Printf("Файл логов %s успешно очищен", cfg.Core.AccessLog)

			case <-ctx.Done():
				return
			}
		}
	}()
}

// withServerHeader добавляет заголовок Server ко всем ответам
func withServerHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHeader := fmt.Sprintf("MuxCloud/%s (WebServer)", constant.Version)
		w.Header().Set("Server", serverHeader)
		w.Header().Set("X-Powered-By", "MuxCloud")
		next.ServeHTTP(w, r)
	})
}

func startAPIServer(ctx context.Context, memDB *sql.DB, dbMutex *sync.Mutex, cfg *config.Config, wg *sync.WaitGroup) {
	server := &http.Server{
		Addr:    cfg.V2rayStat.Address + ":" + cfg.V2rayStat.Port,
		Handler: withServerHeader(http.DefaultServeMux),
	}
	// Заглушка
	http.HandleFunc("/", api.Answer())

	// Эндпоинты только для чтения (без токена)
	http.HandleFunc("/api/v1/users", api.UsersHandler(memDB, dbMutex))
	http.HandleFunc("/api/v1/stats", api.StatsCustomHandler(memDB, dbMutex, cfg))
	http.HandleFunc("/api/v1/stats/base", api.StatsHandler(memDB, dbMutex, cfg))
	http.HandleFunc("/api/v1/dns_stats", api.DnsStatsHandler(memDB, dbMutex))

	// Эндпоинты, изменяющие данные (с проверкой токена)
	http.HandleFunc("/api/v1/add_user", api.TokenAuthMiddleware(cfg, api.AddUserHandler(cfg)))
	http.HandleFunc("/api/v1/bulk_add_users", api.TokenAuthMiddleware(cfg, api.BulkAddUsersHandler(cfg)))
	http.HandleFunc("/api/v1/delete_user", api.TokenAuthMiddleware(cfg, api.DeleteUserHandler(cfg)))
	http.HandleFunc("/api/v1/set_enabled", api.TokenAuthMiddleware(cfg, api.SetEnabledHandler(memDB, dbMutex, cfg)))
	http.HandleFunc("/api/v1/update_lim_ip", api.TokenAuthMiddleware(cfg, api.UpdateIPLimitHandler(memDB, dbMutex)))
	http.HandleFunc("/api/v1/adjust_date", api.TokenAuthMiddleware(cfg, api.AdjustDateOffsetHandler(memDB, dbMutex, cfg)))
	http.HandleFunc("/api/v1/update_renew", api.TokenAuthMiddleware(cfg, api.UpdateRenewHandler(memDB, dbMutex)))
	http.HandleFunc("/api/v1/delete_dns_stats", api.TokenAuthMiddleware(cfg, api.DeleteDNSStatsHandler(memDB, dbMutex)))
	http.HandleFunc("/api/v1/reset_traffic", api.TokenAuthMiddleware(cfg, api.ResetTrafficHandler()))
	http.HandleFunc("/api/v1/reset_clients_stats", api.TokenAuthMiddleware(cfg, api.ResetClientsStatsHandler(memDB, dbMutex)))
	http.HandleFunc("/api/v1/reset_traffic_stats", api.TokenAuthMiddleware(cfg, api.ResetTrafficStatsHandler(memDB, dbMutex)))

	go func() {
		// log.Printf("API server starting on %s:%s...", cfg.V2rayStat.Address, cfg.V2rayStat.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down server: %v", err)
	}
	// log.Println("API server stopped successfully")

	wg.Done()
}

func main() {
	// Load configuration
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}
	initTimezone(&cfg)

	// Инициализация базы данных
	var dbMutex sync.Mutex
	memDB, fileDB, err := db.InitDatabase(&cfg, &dbMutex)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer memDB.Close()
	defer fileDB.Close()

	isInactive, err = db.LoadIsInactiveFromLastSeen(memDB, &dbMutex)
	if err != nil {
		log.Fatalf("Ошибка загрузки начального статуса: %v", err)
	}

	// Setup context and signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start tasks
	var wg sync.WaitGroup
	wg.Add(1)
	go startAPIServer(ctx, memDB, &dbMutex, &cfg, &wg)
	monitorUsersAndLogs(ctx, memDB, &dbMutex, &cfg, &wg)
	db.MonitorSubscriptionsAndSync(ctx, memDB, fileDB, &dbMutex, &cfg, &wg)
	monitor.MonitorExcessIPs(ctx, memDB, &dbMutex, &cfg, &wg)
	monitor.MonitorBannedLog(ctx, &cfg, &wg)

	if cfg.Features["network"] {
		if err := stats.InitNetworkMonitoring(); err != nil {
			log.Printf("Failed to initialize network monitoring: %v", err)
		}
		stats.MonitorNetwork(ctx, &cfg, &wg)
	}

	if cfg.Features["telegram"] {
		stats.MonitorDailyReport(ctx, memDB, &cfg, &wg)
		stats.MonitorStats(ctx, &cfg, &wg)
	}

	log.Printf("Starting v2ray-stat application %s, with core: %s", constant.Version, cfg.V2rayStat.Type)

	// Wait for termination signal
	<-sigChan
	log.Println("Received termination signal, saving data")
	cancel()

	// Дождаться завершения всех горутин
	wg.Wait()
	log.Println("Program completed")
}
