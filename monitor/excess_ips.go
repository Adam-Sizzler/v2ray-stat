package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"v2ray-stat/config"
	"v2ray-stat/db/manager"
)

// logExcessIPs logs excess IP addresses to a file.
func logExcessIPs(manager *manager.DatabaseManager, logFile *os.File, cfg *config.Config) error {
	// Log start of excess IP logging
	cfg.Logger.Debug("Starting excess IP logging")

	currentTime := time.Now().Format("2006/01/02 15:04:05")

	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		// Query clients_stats table
		cfg.Logger.Debug("Reading data from clients_stats table")
		rows, err := db.Query("SELECT user, lim_ip, ips FROM clients_stats")
		if err != nil {
			cfg.Logger.Error("Failed to query clients_stats table", "error", err)
			return fmt.Errorf("failed to query database: %v", err)
		}
		defer rows.Close()

		var processedUsers int
		for rows.Next() {
			var user string
			var ipLimit sql.NullInt32
			var ipAddresses sql.NullString

			// Scan row data
			if err := rows.Scan(&user, &ipLimit, &ipAddresses); err != nil {
				cfg.Logger.Error("Failed to read row for user", "user", user, "error", err)
				return fmt.Errorf("failed to read row: %v", err)
			}
			processedUsers++

			// Log user processing details
			cfg.Logger.Trace("Processing user", "user", user, "ip_limit", ipLimit.Int32, "ips", ipAddresses.String)

			// Skip if IP limit is not set or zero
			if !ipLimit.Valid || ipLimit.Int32 == 0 {
				cfg.Logger.Debug("IP limit not set or zero", "user", user)
				continue
			}

			// Skip if no IP addresses are available
			if !ipAddresses.Valid || ipAddresses.String == "" {
				cfg.Logger.Warn("No IP addresses found for user", "user", user)
				continue
			}

			// Parse and filter IP addresses
			ipAddressesStr := strings.Trim(ipAddresses.String, "[]")
			ipList := strings.Split(ipAddressesStr, ",")

			filteredIPList := make([]string, 0, len(ipList))
			for _, ip := range ipList {
				ip = strings.TrimSpace(ip)
				if ip != "" {
					filteredIPList = append(filteredIPList, ip)
				}
			}

			// Log excess IPs if the limit is exceeded
			if len(filteredIPList) > int(ipLimit.Int32) {
				excessIPs := filteredIPList[ipLimit.Int32:]
				for _, ip := range excessIPs {
					logData := fmt.Sprintf("%s [LIMIT_IP] User = %s || SRC = %s\n", currentTime, user, ip)
					cfg.Logger.Trace("Writing excess IP to log", "user", user, "ip", ip)
					if _, err := logFile.WriteString(logData); err != nil {
						cfg.Logger.Error("Failed to write to log for user", "user", user, "ip", ip, "error", err)
						return fmt.Errorf("failed to write to log file: %v", err)
					}
					cfg.Logger.Debug("Excess IP logged", "user", user, "ip", ip)
				}
			}
		}

		// Check for errors during row processing
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Failed to process result rows", "error", err)
			return fmt.Errorf("failed to process rows: %v", err)
		}

		// Log if no users were found or summarize processed users
		if processedUsers == 0 {
			cfg.Logger.Warn("No users found in clients_stats table")
		} else {
			cfg.Logger.Debug("Completed excess IP logging", "processed_users", processedUsers)
		}

		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in logExcessIPs", "error", err)
		return err
	}

	return nil
}

// MonitorExcessIPs starts a routine to monitor excess IP addresses.
func MonitorExcessIPs(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Log start of excess IP monitoring
		cfg.Logger.Debug("Starting excess IP monitoring")

		// Open log file for appending
		logFile, err := os.OpenFile(cfg.Paths.F2BLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			cfg.Logger.Error("Failed to open log file", "path", cfg.Paths.F2BLog, "error", err)
			return
		}
		defer logFile.Close()

		// Log successful file opening
		cfg.Logger.Debug("Log file opened successfully", "path", cfg.Paths.F2BLog)

		// Start periodic monitoring
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Log start of periodic check
				cfg.Logger.Debug("Starting periodic excess IP check")
				if err := logExcessIPs(manager, logFile, cfg); err != nil {
					cfg.Logger.Error("Failed to log excess IPs", "error", err)
				}
			case <-ctx.Done():
				// Log monitoring termination
				cfg.Logger.Debug("Stopped monitoring excess IPs")
				return
			}
		}
	}()
}
