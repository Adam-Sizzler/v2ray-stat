package users

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// IPStore stores IP addresses and their timestamps with mutex for thread safety.
type IPStore struct {
	timestamps map[string]map[string]time.Time // user -> ip -> timestamp
	mutex      sync.RWMutex
}

// NewIPStore creates a new IPStore instance.
func NewIPStore() *IPStore {
	return &IPStore{
		timestamps: make(map[string]map[string]time.Time),
	}
}

// AddIPs adds/updates IPs for a user with timestamps.
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

// CollectAndCleanup returns valid IPs for database update and cleans up old ones.
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
				delete(ipMap, ip) // Remove outdated IPs
			}
		}
		if len(validIPs) > 0 {
			ipUpdates[user] = validIPs
		}
		if len(ipMap) == 0 {
			delete(s.timestamps, user) // Clean up empty users
		}
	}

	return ipUpdates
}

// UpdateIPsBatch updates IP addresses for multiple users in a single transaction.
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

// UpsertDNSRecordsBatch performs batch updates or inserts for DNS records.
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
