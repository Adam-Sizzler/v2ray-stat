package users

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/api"
	"v2ray-stat/node/proto"
)

// statsStore хранит предыдущие значения трафика для расчёта разницы.
type statsStore struct {
	previousProxyStats map[string]string // node_name -> stats
	previousUserStats  map[string]string // node_name -> stats
	mu                 sync.Mutex
}

// newStatsStore создаёт новый statsStore.
func newStatsStore() *statsStore {
	return &statsStore{
		previousProxyStats: make(map[string]string),
		previousUserStats:  make(map[string]string),
	}
}

// updateProxyStats обновляет статистику трафика для inbound-тегов в базе данных.
func updateProxyStats(manager *manager.DatabaseManager, nodeName string, apiData *api.ApiResponse, cfg *config.Config, store *statsStore) error {
	cfg.Logger.Debug("Starting proxy stats update", "node", nodeName)

	// Парсим текущие данные
	currentStats := extractProxyTraffic(apiData)
	store.mu.Lock()
	previousStats := store.previousProxyStats[nodeName]
	store.previousProxyStats[nodeName] = strings.Join(currentStats, "\n")
	store.mu.Unlock()

	if previousStats == "" {
		cfg.Logger.Debug("Initialized previous proxy stats", "node", nodeName, "count", len(currentStats))
		return nil
	}

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
	for _, line := range strings.Split(previousStats, "\n") {
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

			cfg.Logger.Debug("Updating proxy stats", "node", nodeName, "source", source, "uplink", uplink, "downlink", downlink)

			_, err := tx.Exec(`
				INSERT INTO traffic_stats (node_name, source, rate, uplink, downlink, sess_uplink, sess_downlink)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(node_name, source) DO UPDATE SET
					rate = ?,
					uplink = uplink + ?,
					downlink = downlink + ?,
					sess_uplink = ?,
					sess_downlink = ?`,
				nodeName, source, 0, uplink, downlink, sessUplink, sessDownlink,
				0, uplink, downlink, sessUplink, sessDownlink)
			if err != nil {
				return fmt.Errorf("update traffic_stats for %s: %w", source, err)
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
func updateUserStats(manager *manager.DatabaseManager, nodeName string, apiData *api.ApiResponse, cfg *config.Config, store *statsStore) error {
	cfg.Logger.Debug("Starting user stats update", "node", nodeName)

	// Парсим текущие данные
	currentStats := extractUserTraffic(apiData)
	store.mu.Lock()
	previousStats := store.previousUserStats[nodeName]
	store.previousUserStats[nodeName] = strings.Join(currentStats, "\n")
	store.mu.Unlock()

	if previousStats == "" {
		cfg.Logger.Debug("Initialized previous user stats", "node", nodeName, "count", len(currentStats))
		return nil
	}

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
	for _, line := range strings.Split(previousStats, "\n") {
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

	currentTime := time.Now().In(time.UTC)
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("start transaction for user stats: %w", err)
		}
		defer tx.Rollback()

		for user := range uplinkValues {
			// Проверяем существование пользователя в users_stats
			var exists bool
			err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM users_stats WHERE node_name = ? AND user = ?)", nodeName, user).Scan(&exists)
			if err != nil {
				return fmt.Errorf("check user %s existence: %w", user, err)
			}
			if !exists {
				cfg.Logger.Debug("Skipping stats for non-existent user", "node", nodeName, "user", user)
				continue
			}

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
			if rate > cfg.V2rayStat.Monitor.OnlineRateThreshold*1000 {
				lastSeen = "online"
				cfg.Logger.Debug("User is active", "node", nodeName, "user", user)
			} else {
				lastSeen = currentTime.Truncate(time.Minute).Format("2006-01-02 15:04")
				cfg.Logger.Debug("User transitioned to inactive state", "node", nodeName, "user", user, "last_seen", lastSeen)
			}

			cfg.Logger.Debug("Updating user stats", "node", nodeName, "user", user, "rate", rate, "uplink", uplink, "downlink", downlink)

			_, err = tx.Exec(`
				UPDATE users_stats SET
					last_seen = ?,
					rate = ?,
					uplink = uplink + ?,
					downlink = downlink + ?,
					sess_uplink = ?,
					sess_downlink = ?
				WHERE node_name = ? AND user = ?`,
				lastSeen, rate, uplink, downlink, sessUplink, sessDownlink, nodeName, user)
			if err != nil {
				return fmt.Errorf("update users_stats for %s: %w", user, err)
			}
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
func MonitorTrafficStats(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config, wg *sync.WaitGroup, store *statsStore) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				for _, nc := range nodeClients {
					grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()

					apiData, err := nc.Client.GetApiResponse(grpcCtx, &proto.GetApiResponseRequest{})
					if err != nil {
						cfg.Logger.Error("Failed to retrieve API data from node", "node", nc.Name, "error", err)
						continue
					}

					if err := updateProxyStats(manager, nc.Name, apiData, cfg, store); err != nil {
						cfg.Logger.Error("Failed to update proxy stats", "node", nc.Name, "error", err)
					}
					if err := updateUserStats(manager, nc.Name, apiData, cfg, store); err != nil {
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
