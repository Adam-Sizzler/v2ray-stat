package users

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/common"
	"v2ray-stat/node/api"
	"v2ray-stat/node/proto"
)

var (
	userActivityTimestamps = make(map[string]map[string]time.Time) // node_name -> user -> timestamp
	userActivityMutex      sync.Mutex
	previousStats          = make(map[string]string) // node_name -> stats
	clientPreviousStats    = make(map[string]string) // node_name -> stats
	isInactive             = make(map[string]bool)   // node_name:user -> status
	isInactiveMutex        sync.Mutex
)

// LoadIsInactiveFromLastSeen загружает статус неактивности пользователей из last_seen.
func LoadIsInactiveFromLastSeen(manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Loading user inactivity status from last_seen")
	isInactiveLocal := make(map[string]bool)
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		rows, err := db.Query("SELECT node_name, user, last_seen FROM user_traffic")
		if err != nil {
			cfg.Logger.Error("Failed to query users", "error", err)
			return fmt.Errorf("failed to query users: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var nodeName, user, lastSeen string
			if err := rows.Scan(&nodeName, &user, &lastSeen); err != nil {
				cfg.Logger.Warn("Failed to scan row", "node", nodeName, "user", user, "error", err)
				continue
			}
			cfg.Logger.Trace("Processing user", "node", nodeName, "user", user, "last_seen", lastSeen)
			isInactiveLocal[nodeName+":"+user] = lastSeen != "online"
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Error iterating rows", "error", err)
			return fmt.Errorf("error iterating rows: %w", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in LoadIsInactiveFromLastSeen", "error", err)
		return err
	}
	cfg.Logger.Debug("Loaded user activity data", "count", len(isInactiveLocal))

	isInactiveMutex.Lock()
	maps.Copy(isInactive, isInactiveLocal)
	isInactiveMutex.Unlock()

	return nil
}

// convertProtoToApiResponse преобразует proto.GetApiResponseResponse в api.ApiResponse.
func convertProtoToApiResponse(protoData *proto.GetApiResponseResponse) *api.ApiResponse {
	apiData := &api.ApiResponse{}
	for _, s := range protoData.Stats {
		apiData.Stat = append(apiData.Stat, api.Stat{
			Name:  s.Name,
			Value: s.Value,
		})
	}
	return apiData
}

// updateProxyStats обновляет статистику трафика для inbound-тегов в базе данных.
func updateProxyStats(manager *manager.DatabaseManager, nodeName string, apiData *api.ApiResponse, cfg *config.Config) error {
	cfg.Logger.Debug("Starting proxy stats update", "node", nodeName)

	currentStats := extractProxyTraffic(apiData)
	userActivityMutex.Lock()
	if userActivityTimestamps[nodeName] == nil {
		userActivityTimestamps[nodeName] = make(map[string]time.Time)
	}
	userActivityMutex.Unlock()

	previousStatsMutex := sync.Mutex{}
	previousStatsMutex.Lock()
	if previousStats[nodeName] == "" {
		previousStats[nodeName] = strings.Join(currentStats, "\n")
		previousStatsMutex.Unlock()
		cfg.Logger.Debug("Initialized previous proxy stats", "node", nodeName, "count", len(currentStats))
		return nil
	}
	previous := previousStats[nodeName]
	previousStats[nodeName] = strings.Join(currentStats, "\n")
	previousStatsMutex.Unlock()

	currentValues := make(map[string]int)
	previousValues := make(map[string]int)

	// Парсим текущие данные
	for _, line := range currentStats {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Parsing current proxy stats", "node", nodeName, "line", line)
			currentValues[parts[0]+" "+parts[1]] = stringToInt(cfg, parts[2])
		} else {
			cfg.Logger.Warn("Invalid proxy stats line format", "node", nodeName, "line", line)
		}
	}

	// Парсим предыдущие данные
	for line := range strings.SplitSeq(previous, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Parsing previous proxy stats", "node", nodeName, "line", line)
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
			cfg.Logger.Warn("Missing previous proxy data", "node", nodeName, "key", key)
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
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("start transaction for proxy stats: %w", err)
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
				cfg.Logger.Warn("Missing previous uplink data", "node", nodeName, "source", source)
				previousUplink = 0
			}
			if !downlinkExists {
				cfg.Logger.Warn("Missing previous downlink data", "node", nodeName, "source", source)
				previousDownlink = 0
			}

			uplinkOnline := max(sessUplink-previousUplink, 0)
			downlinkOnline := max(sessDownlink-previousDownlink, 0)
			rate := (uplinkOnline + downlinkOnline) * 8 / cfg.V2rayStat.Monitor.TickerInterval

			cfg.Logger.Debug("Updating proxy stats", "node", nodeName, "source", source, "rate", rate, "uplink", uplink, "downlink", downlink)

			_, err := tx.Exec(`
				INSERT INTO bound_traffic (node_name, source, rate, uplink, downlink, sess_uplink, sess_downlink)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(node_name, source) DO UPDATE SET
					rate = ?,
					uplink = uplink + ?,
					downlink = downlink + ?,
					sess_uplink = ?,
					sess_downlink = ?`,
				nodeName, source, rate, uplink, downlink, sessUplink, sessDownlink,
				rate, uplink, downlink, sessUplink, sessDownlink)
			if err != nil {
				return fmt.Errorf("update bound_traffic for %s: %w", source, err)
			}
		}

		return tx.Commit()
	})
	if err != nil {
		cfg.Logger.Error("Failed to update proxy stats", "node", nodeName, "error", err)
		return err
	}

	cfg.Logger.Debug("Finished proxy stats update", "node", nodeName, "entries", len(currentStats))
	return nil
}

// updateUserStats обновляет статистику трафика пользователей в базе данных.
func updateUserStats(manager *manager.DatabaseManager, nodeName string, apiData *api.ApiResponse, cfg *config.Config) error {
	cfg.Logger.Debug("Starting user stats update", "node", nodeName)

	currentStats := extractUserTraffic(apiData)
	userActivityMutex.Lock()
	if userActivityTimestamps[nodeName] == nil {
		userActivityTimestamps[nodeName] = make(map[string]time.Time)
	}
	userActivityMutex.Unlock()

	previousStatsMutex := sync.Mutex{}
	previousStatsMutex.Lock()
	if clientPreviousStats[nodeName] == "" {
		clientPreviousStats[nodeName] = strings.Join(currentStats, "\n")
		previousStatsMutex.Unlock()
		cfg.Logger.Debug("Initialized previous user stats", "node", nodeName, "count", len(currentStats))
		return nil
	}
	previous := clientPreviousStats[nodeName]
	clientPreviousStats[nodeName] = strings.Join(currentStats, "\n")
	previousStatsMutex.Unlock()

	currentValues := make(map[string]int)
	previousValues := make(map[string]int)

	// Парсим текущие данные
	for _, line := range currentStats {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Parsing current user stats", "node", nodeName, "line", line)
			currentValues[parts[0]+" "+parts[1]] = stringToInt(cfg, parts[2])
		} else {
			cfg.Logger.Warn("Invalid user stats line format", "node", nodeName, "line", line)
		}
	}

	// Парсим предыдущие данные
	for _, line := range strings.Split(previous, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 3 {
			cfg.Logger.Trace("Parsing previous user stats", "node", nodeName, "line", line)
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
			cfg.Logger.Warn("Missing previous user data", "node", nodeName, "key", key)
			previous = 0
		}
		diff := max(current-previous, 0)
		parts := strings.Fields(key)
		user := parts[0]
		direction := parts[1]

		switch direction {
		case "uplink":
			uplinkValues[user] = diff
			sessUplinkValues[user] = current
		case "downlink":
			downlinkValues[user] = diff
			sessDownlinkValues[user] = current
		}
	}

	for key := range previousValues {
		parts := strings.Fields(key)
		if len(parts) != 2 {
			cfg.Logger.Warn("Invalid key format in previous stats", "node", nodeName, "key", key)
			continue
		}
		user := parts[0]
		direction := parts[1]

		switch direction {
		case "uplink":
			if _, exists := sessUplinkValues[user]; !exists {
				cfg.Logger.Debug("Setting zero values for uplink", "node", nodeName, "user", user)
				sessUplinkValues[user] = 0
				uplinkValues[user] = 0
			}
		case "downlink":
			if _, exists := sessDownlinkValues[user]; !exists {
				cfg.Logger.Debug("Setting zero values for downlink", "node", nodeName, "user", user)
				sessDownlinkValues[user] = 0
				downlinkValues[user] = 0
			}
		}
	}

	currentTime := time.Now().In(common.TimeLocation)
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("start transaction for user stats: %w", err)
		}
		defer tx.Rollback()

		isInactiveMutex.Lock()
		defer isInactiveMutex.Unlock()

		for user := range uplinkValues {
			uplink := uplinkValues[user]
			downlink := downlinkValues[user]
			sessUplink := sessUplinkValues[user]
			sessDownlink := sessDownlinkValues[user]
			previousUplink, uplinkExists := previousValues[user+" uplink"]
			previousDownlink, downlinkExists := previousValues[user+" downlink"]

			if !uplinkExists {
				cfg.Logger.Warn("Missing previous uplink data", "node", nodeName, "user", user)
				previousUplink = 0
			}
			if !downlinkExists {
				cfg.Logger.Warn("Missing previous downlink data", "node", nodeName, "user", user)
				previousDownlink = 0
			}

			uplinkOnline := max(sessUplink-previousUplink, 0)
			downlinkOnline := max(sessDownlink-previousDownlink, 0)
			rate := (uplinkOnline + downlinkOnline) * 8 / cfg.V2rayStat.Monitor.TickerInterval

			var lastSeen string
			userKey := nodeName + ":" + user
			if rate > cfg.V2rayStat.Monitor.OnlineRateThreshold*1000 {
				lastSeen = "online"
				isInactive[userKey] = false
				cfg.Logger.Debug("User is active", "node", nodeName, "user", user)
			} else {
				if !isInactive[userKey] {
					lastSeen = currentTime.Truncate(time.Minute).Format("2006-01-02 15:04")
					isInactive[userKey] = true
					cfg.Logger.Debug("User transitioned to inactive state", "node", nodeName, "user", user, "last_seen", lastSeen)
				}
			}

			cfg.Logger.Debug("Updating user stats", "node", nodeName, "user", user, "rate", rate, "uplink", uplink, "downlink", downlink)

			if lastSeen != "" {
				_, err := tx.Exec(`
					INSERT INTO user_traffic (node_name, user, last_seen, rate, uplink, downlink, sess_uplink, sess_downlink)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(node_name, user) DO UPDATE SET
						last_seen = ?,
						rate = ?,
						uplink = uplink + ?,
						downlink = downlink + ?,
						sess_uplink = ?,
						sess_downlink = ?`,
					nodeName, user, lastSeen, rate, uplink, downlink, sessUplink, sessDownlink,
					lastSeen, rate, uplink, downlink, sessUplink, sessDownlink)
				if err != nil {
					return fmt.Errorf("update user_traffic for %s: %w", user, err)
				}
			} else {
				_, err := tx.Exec(`
					INSERT INTO user_traffic (node_name, user, rate, uplink, downlink, sess_uplink, sess_downlink)
					VALUES (?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(node_name, user) DO UPDATE SET
						rate = ?,
						uplink = uplink + ?,
						downlink = downlink + ?,
						sess_uplink = ?,
						sess_downlink = ?`,
					nodeName, user, rate, uplink, downlink, sessUplink, sessDownlink,
					rate, uplink, downlink, sessUplink, sessDownlink)
				if err != nil {
					return fmt.Errorf("update user_traffic for %s: %w", user, err)
				}
			}

			userActivityMutex.Lock()
			userActivityTimestamps[nodeName][user] = currentTime
			userActivityMutex.Unlock()
		}

		return tx.Commit()
	})
	if err != nil {
		cfg.Logger.Error("Failed to update user stats", "node", nodeName, "error", err)
		return err
	}

	cfg.Logger.Debug("Finished user stats update", "node", nodeName, "entries", len(currentStats))
	return nil
}

// extractProxyTraffic фильтрует и форматирует статистику трафика для inbound-тегов.
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

// extractUserTraffic фильтрует и форматирует статистику трафика пользователей.
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

// splitAndCleanName разделяет имя статистики и возвращает компоненты.
func splitAndCleanName(name string) []string {
	parts := strings.Split(name, ">>>")
	if len(parts) == 4 {
		return []string{parts[1], parts[3]}
	}
	return nil
}

// stringToInt конвертирует строку в целое число.
func stringToInt(cfg *config.Config, s string) int {
	result, err := strconv.Atoi(s)
	if err != nil {
		cfg.Logger.Warn("Failed to convert string to integer", "string", s, "error", err)
		return 0
	}
	return result
}

// max возвращает максимум из двух чисел.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// MonitorTrafficStats периодически собирает статистику трафика с нод.
func MonitorTrafficStats(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Загружаем начальный статус неактивности
		if err := LoadIsInactiveFromLastSeen(manager, cfg); err != nil {
			cfg.Logger.Error("Failed to load initial user inactivity status", "error", err)
		}

		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				for _, nc := range nodeClients {
					grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()

					protoData, err := nc.Client.GetApiResponse(grpcCtx, &proto.GetApiResponseRequest{})
					if err != nil {
						cfg.Logger.Error("Failed to retrieve API data from node", "node", nc.Name, "error", err)
						continue
					}

					apiData := convertProtoToApiResponse(protoData)
					if err := updateProxyStats(manager, nc.Name, apiData, cfg); err != nil {
						cfg.Logger.Error("Failed to update proxy stats", "node", nc.Name, "error", err)
					}
					if err := updateUserStats(manager, nc.Name, apiData, cfg); err != nil {
						cfg.Logger.Error("Failed to update user stats", "node", nc.Name, "error", err)
					}
				}
			case <-ctx.Done():
				cfg.Logger.Debug("Traffic monitoring stopped")
				return
			}
		}
	}()
}
