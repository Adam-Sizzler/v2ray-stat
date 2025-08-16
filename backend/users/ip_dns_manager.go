package users

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// IPStore хранит IP-адреса и их временные метки с мьютексом для потокобезопасности.
type IPStore struct {
	timestamps map[string]map[string]time.Time // user -> ip -> timestamp
	mutex      sync.RWMutex
}

// NewIPStore создаёт новый экземпляр IPStore.
func NewIPStore() *IPStore {
	return &IPStore{
		timestamps: make(map[string]map[string]time.Time),
	}
}

// AddIPs добавляет/обновляет IP для пользователя с меткой времени.
func (s *IPStore) AddIPs(user string, ips []string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.timestamps[user] == nil {
		s.timestamps[user] = make(map[string]time.Time)
	}
	for _, ip := range ips {
		s.timestamps[user][ip] = time.Now()
	}
}

// CollectAndCleanup возвращает актуальные IP для обновления в БД и чистит старые.
func (s *IPStore) CollectAndCleanup(ttl time.Duration) map[string][]string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	ipUpdates := make(map[string][]string)
	now := time.Now()

	for user, ipMap := range s.timestamps {
		validIPs := []string{}
		for ip, timestamp := range ipMap {
			if now.Sub(timestamp) <= ttl {
				validIPs = append(validIPs, ip)
			} else {
				delete(ipMap, ip) // удаляем устаревший
			}
		}
		if len(validIPs) > 0 {
			ipUpdates[user] = validIPs
		}
		if len(ipMap) == 0 {
			delete(s.timestamps, user) // чистим пустых
		}
	}

	return ipUpdates
}

// UpdateIPsBatch обновляет IP-адреса для нескольких пользователей одной транзакцией.
func UpdateIPsBatch(manager *manager.DatabaseManager, ipUpdates map[string][]string, cfg *config.Config) error {
	cfg.Logger.Debug("Starting batch IP updates", "users_count", len(ipUpdates))
	if len(ipUpdates) == 0 {
		cfg.Logger.Warn("No IP updates to process")
		return nil
	}

	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		for user, ipList := range ipUpdates {
			ipStr := strings.Join(ipList, ",")
			cfg.Logger.Trace("Formatted IP list", "user", user, "ips", ipStr)
			_, err := tx.Exec(`
                INSERT INTO user_data (user, ips)
                VALUES (?, ?)
                ON CONFLICT(user) DO UPDATE SET ips = ?`,
				user, ipStr, ipStr)
			if err != nil {
				cfg.Logger.Error("Failed to update IPs for user", "user", user, "error", err)
				return fmt.Errorf("failed to update IPs for user %s: %v", user, err)
			}
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in UpdateIPsBatch", "error", err)
		return err
	}

	cfg.Logger.Debug("IP updates completed successfully", "users_count", len(ipUpdates))
	return nil
}

// UpsertDNSRecordsBatch выполняет пакетное обновление или вставку DNS-записей.
func UpsertDNSRecordsBatch(manager *manager.DatabaseManager, dnsStats map[string]map[string]int, nodeName string, cfg *config.Config) error {
	cfg.Logger.Debug("Starting batch DNS records update", "node_name", nodeName, "records_count", len(dnsStats))
	if len(dnsStats) == 0 {
		cfg.Logger.Warn("No DNS records to update", "node_name", nodeName)
		return nil
	}

	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		for user, domains := range dnsStats {
			for domain, count := range domains {
				_, err := tx.Exec(`
                    INSERT INTO user_dns (node_name, user, domain, count)
                    VALUES (?, ?, ?, ?)
                    ON CONFLICT(node_name, user, domain)
                    DO UPDATE SET count = count + ?`,
					nodeName, user, domain, count, count)
				if err != nil {
					cfg.Logger.Error("Failed to update DNS record", "node_name", nodeName, "user", user, "domain", domain, "error", err)
					return fmt.Errorf("failed to update user_dns for %s:%s:%s: %v", nodeName, user, domain, err)
				}
				cfg.Logger.Debug("DNS record updated successfully", "node_name", nodeName, "user", user, "domain", domain, "count", count)
			}
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in UpsertDNSRecordsBatch", "node_name", nodeName, "error", err)
		return err
	}

	cfg.Logger.Debug("DNS records updated successfully", "node_name", nodeName, "records_count", len(dnsStats))
	return nil
}

// ProcessLogData обрабатывает данные логов с нод, обновляя IP-адреса и DNS-статистику.
func ProcessLogData(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, store *IPStore, cfg *config.Config) error {
	for _, nc := range nodeClients {
		grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		response, err := nc.Client.GetLogData(grpcCtx, &proto.GetLogDataRequest{})
		if err != nil {
			cfg.Logger.Error("Failed to retrieve log data from node", "node_name", nc.NodeName, "error", err)
			continue
		}

		// Собираем DNS-статистику
		dnsStats := make(map[string]map[string]int)
		for user, data := range response.UserLogData {
			store.AddIPs(user, data.ValidIps)
			dnsStats[user] = make(map[string]int)
			for domain, count := range data.DnsStats {
				dnsStats[user][domain] = int(count)
			}
		}

		if len(dnsStats) > 0 {
			if err := UpsertDNSRecordsBatch(manager, dnsStats, nc.NodeName, cfg); err != nil {
				cfg.Logger.Error("Failed to update user_dns", "node_name", nc.NodeName, "error", err)
				continue
			}
		} else {
			cfg.Logger.Debug("No DNS records to update from node", "node_name", nc.NodeName)
		}
	}

	ipUpdates := store.CollectAndCleanup(66 * time.Second)
	if len(ipUpdates) > 0 {
		if err := UpdateIPsBatch(manager, ipUpdates, cfg); err != nil {
			cfg.Logger.Error("Failed to update IPs in database", "error", err)
			return err
		}
	}

	return nil
}

// MonitorLogData периодически запрашивает данные логов с нод через gRPC.
func MonitorLogData(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Создаём IPStore для хранения состояния между тиками
		store := NewIPStore()

		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := ProcessLogData(ctx, manager, nodeClients, store, cfg); err != nil {
					cfg.Logger.Error("Failed to process log data", "error", err)
				}
			case <-ctx.Done():
				cfg.Logger.Debug("Log data monitoring stopped")
				return
			}
		}
	}()
}
