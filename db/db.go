package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"v2ray-stat/config"
	"v2ray-stat/db/manager"
	"v2ray-stat/telegram"
)

var (
	notifiedMutex      sync.Mutex
	notifiedUsers      = make(map[string]bool)
	renewNotifiedUsers = make(map[string]bool)
)

var (
	dateOffsetRegex = regexp.MustCompile(`^([+-]?)(\d+)(?::(\d+))?$`)
)

// extractUsersXrayServer retrieves Xray users from config files.
func extractUsersXrayServer(cfg *config.Config) []config.XrayClient {
	cfg.Logger.Debug("Extracting Xray users from config")
	clientMap := make(map[string]config.XrayClient)

	extractClients := func(inbounds []config.XrayInbound) {
		for _, inbound := range inbounds {
			for _, client := range inbound.Settings.Clients {
				cfg.Logger.Trace("Processing client", "email", client.Email)
				clientMap[client.Email] = client
			}
		}
	}

	data, err := os.ReadFile(cfg.Core.Config)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", cfg.Core.Config, "error", err)
	} else {
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			cfg.Logger.Error("Failed to parse JSON from config.json", "error", err)
		} else {
			cfg.Logger.Debug("Processing Xray inbounds from config.json")
			extractClients(cfgXray.Inbounds)
		}
	}

	disabledUsersPath := filepath.Join(cfg.Core.Dir, ".disabled_users")
	disabledData, err := os.ReadFile(disabledUsersPath)
	if err == nil {
		if len(disabledData) != 0 {
			var disabledCfg config.DisabledUsersConfigXray
			if err := json.Unmarshal(disabledData, &disabledCfg); err != nil {
				cfg.Logger.Error("Failed to parse JSON from .disabled_users", "path", disabledUsersPath, "error", err)
			} else {
				cfg.Logger.Debug("Processing Xray inbounds from .disabled_users")
				extractClients(disabledCfg.Inbounds)
			}
		} else {
			cfg.Logger.Warn("Empty .disabled_users file", "path", disabledUsersPath)
		}
	} else if !os.IsNotExist(err) {
		cfg.Logger.Error("Failed to read .disabled_users", "path", disabledUsersPath, "error", err)
	}

	var clients []config.XrayClient
	for _, client := range clientMap {
		clients = append(clients, client)
	}
	cfg.Logger.Debug("Extracted Xray users", "count", len(clients))
	return clients
}

// extractUsersSingboxServer retrieves Singbox users from config file.
func extractUsersSingboxServer(cfg *config.Config) []config.XrayClient {
	cfg.Logger.Debug("Extracting Singbox users from config")
	data, err := os.ReadFile(cfg.Core.Config)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json for Singbox", "path", cfg.Core.Config, "error", err)
		return nil
	}

	var cfgSingbox config.ConfigSingbox
	if err := json.Unmarshal(data, &cfgSingbox); err != nil {
		cfg.Logger.Error("Failed to parse JSON for Singbox", "error", err)
		return nil
	}

	var clients []config.XrayClient
	for _, inbound := range cfgSingbox.Inbounds {
		if inbound.Tag == "vless-in" || inbound.Tag == "trojan-in" {
			for _, user := range inbound.Users {
				cfg.Logger.Trace("Processing Singbox user", "name", user.Name, "tag", inbound.Tag)
				client := config.XrayClient{Email: user.Name}
				switch inbound.Type {
				case "vless":
					client.ID = user.UUID
				case "trojan":
					client.ID = user.Password
					client.Password = user.UUID
				}
				clients = append(clients, client)
			}
		}
	}
	cfg.Logger.Info("Extracted Singbox users", "count", len(clients))
	return clients
}

// AddUserToDB adds users to the clients_stats database table.
func AddUserToDB(manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Starting to add users to database", "type", cfg.V2rayStat.Type)
	var clients []config.XrayClient
	switch cfg.V2rayStat.Type {
	case "xray":
		clients = extractUsersXrayServer(cfg)
	case "singbox":
		clients = extractUsersSingboxServer(cfg)
	}

	if len(clients) == 0 {
		cfg.Logger.Warn("No users found to add to database", "type", cfg.V2rayStat.Type)
		return nil
	}

	cfg.Logger.Debug("Found users to add", "count", len(clients))
	var addedUsers []string
	currentTime := time.Now().Format("2006-01-02-15")

	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Preparing insert statement for users")
		stmt, err := tx.Prepare("INSERT OR IGNORE INTO clients_stats(user, uuid, rate, enabled, created) VALUES (?, ?, ?, ?, ?)")
		if err != nil {
			cfg.Logger.Error("Failed to prepare insert statement", "error", err)
			return fmt.Errorf("failed to prepare statement: %v", err)
		}
		defer stmt.Close()

		for _, client := range clients {
			cfg.Logger.Trace("Processing user", "user", client.Email, "uuid", client.ID)
			result, err := stmt.Exec(client.Email, client.ID, "0", "true", currentTime)
			if err != nil {
				cfg.Logger.Error("Failed to insert user", "user", client.Email, "error", err)
				return fmt.Errorf("failed to insert client %s: %v", client.Email, err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to get RowsAffected for user", "user", client.Email, "error", err)
				return fmt.Errorf("failed to get RowsAffected for client %s: %v", client.Email, err)
			}
			if rowsAffected > 0 {
				cfg.Logger.Info("User added successfully", "user", client.Email)
				addedUsers = append(addedUsers, client.Email)
			} else {
				cfg.Logger.Trace("User already exists in database", "user", client.Email)
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
		cfg.Logger.Error("Error in AddUserToDB", "error", err)
		return err
	}

	if len(addedUsers) > 0 {
		cfg.Logger.Info("Users added to database", "users", strings.Join(addedUsers, ", "), "count", len(addedUsers))
	} else {
		cfg.Logger.Debug("No new users added, all already exist")
	}
	return nil
}

// DelUserFromDB removes users from the database if they are not in the config.
func DelUserFromDB(manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Starting to remove users from database", "type", cfg.V2rayStat.Type)
	var clients []config.XrayClient
	switch cfg.V2rayStat.Type {
	case "xray":
		clients = extractUsersXrayServer(cfg)
	case "singbox":
		clients = extractUsersSingboxServer(cfg)
	}

	cfg.Logger.Debug("Found users in config", "count", len(clients))
	var usersDB []string
	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Reading users from database")
		rows, err := db.Query("SELECT user FROM clients_stats")
		if err != nil {
			cfg.Logger.Error("Failed to query users", "error", err)
			return fmt.Errorf("failed to query users: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var user string
			if err := rows.Scan(&user); err != nil {
				cfg.Logger.Error("Failed to scan row", "error", err)
				return fmt.Errorf("failed to scan row: %v", err)
			}
			cfg.Logger.Trace("Read user from database", "user", user)
			usersDB = append(usersDB, user)
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Error iterating rows", "error", err)
			return fmt.Errorf("error iterating rows: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in DelUserFromDB while reading users", "error", err)
		return err
	}

	cfg.Logger.Debug("Read users from database", "count", len(usersDB))
	var deletedUsers []string
	for _, user := range usersDB {
		found := false
		for _, xrayUser := range clients {
			if user == xrayUser.Email {
				found = true
				break
			}
		}
		if !found {
			cfg.Logger.Debug("User marked for deletion", "user", user)
			deletedUsers = append(deletedUsers, user)
		}
	}

	if len(deletedUsers) == 0 {
		cfg.Logger.Debug("No users to delete from database")
		return nil
	}

	err = manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Preparing delete statement for users")
		stmt, err := tx.Prepare("DELETE FROM clients_stats WHERE user = ?")
		if err != nil {
			cfg.Logger.Error("Failed to prepare delete statement", "error", err)
			return fmt.Errorf("failed to prepare statement: %v", err)
		}
		defer stmt.Close()

		for _, user := range deletedUsers {
			cfg.Logger.Trace("Deleting user", "user", user)
			_, err := stmt.Exec(user)
			if err != nil {
				cfg.Logger.Error("Failed to delete user", "user", user, "error", err)
				return fmt.Errorf("failed to delete user %s: %v", user, err)
			}
			cfg.Logger.Debug("User deleted successfully", "user", user)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in DelUserFromDB while deleting users", "error", err)
		return err
	}

	cfg.Logger.Info("Users deleted from database", "users", strings.Join(deletedUsers, ", "), "count", len(deletedUsers))
	return nil
}

// UpdateIPInDB updates the IP list for a user in the database.
func UpdateIPInDB(manager *manager.DatabaseManager, user string, ipList []string, cfg *config.Config) error {
	cfg.Logger.Debug("Starting to update IPs for user", "user", user)
	if len(ipList) == 0 {
		cfg.Logger.Warn("IP list is empty for user", "user", user)
	}

	ipStr := strings.Join(ipList, ",")
	cfg.Logger.Trace("Formatted IP list", "user", user, "ips", ipStr)

	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Executing update IPs query", "user", user)
		_, err = tx.Exec("UPDATE clients_stats SET ips = ? WHERE user = ?", ipStr, user)
		if err != nil {
			cfg.Logger.Error("Failed to update IPs for user", "user", user, "error", err)
			return fmt.Errorf("failed to update IPs for user %s: %v", user, err)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in UpdateIPInDB for user", "user", user, "error", err)
		return err
	}

	cfg.Logger.Info("IPs updated successfully for user", "user", user, "ips", ipStr)
	return nil
}

// UpsertDNSRecordsBatch performs batch updates or inserts for DNS records.
func UpsertDNSRecordsBatch(manager *manager.DatabaseManager, dnsStats map[string]map[string]int, cfg *config.Config) error {
	cfg.Logger.Debug("Starting batch DNS records update", "records_count", len(dnsStats))
	if len(dnsStats) == 0 {
		cfg.Logger.Warn("No DNS records to update")
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
				cfg.Logger.Trace("Processing DNS record", "user", user, "domain", domain, "count", count)
				_, err := tx.Exec(`
					INSERT INTO dns_stats (user, domain, count) 
					VALUES (?, ?, ?)
					ON CONFLICT(user, domain) 
					DO UPDATE SET count = count + ?`,
					user, domain, count, count)
				if err != nil {
					cfg.Logger.Error("Failed to update DNS record", "user", user, "domain", domain, "error", err)
					return fmt.Errorf("failed to update dns_stats for user %s and domain %s: %v", user, domain, err)
				}
				cfg.Logger.Debug("DNS record updated successfully", "user", user, "domain", domain, "count", count)
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
		cfg.Logger.Error("Error in UpsertDNSRecordsBatch", "error", err)
		return err
	}

	cfg.Logger.Debug("DNS records updated successfully", "records_count", len(dnsStats))
	return nil
}

// UpdateEnabledInDB updates the enabled status for a user in the database.
func UpdateEnabledInDB(manager *manager.DatabaseManager, cfg *config.Config, user string, enabled bool) error {
	cfg.Logger.Debug("Starting to update enabled status", "user", user, "enabled", enabled)
	if user == "" {
		cfg.Logger.Warn("Empty user value")
		return fmt.Errorf("empty user value")
	}

	enabledStr := "false"
	if enabled {
		enabledStr = "true"
	}
	cfg.Logger.Trace("Converted enabled status", "enabled", enabled, "enabledStr", enabledStr)

	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "user", user, "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Executing update enabled query", "user", user, "enabledStr", enabledStr)
		result, err := tx.Exec("UPDATE clients_stats SET enabled = ? WHERE user = ?", enabledStr, user)
		if err != nil {
			cfg.Logger.Error("Failed to update enabled status", "user", user, "error", err)
			return fmt.Errorf("failed to update enabled status for user %s: %v", user, err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			cfg.Logger.Error("Failed to check affected rows", "user", user, "error", err)
			return fmt.Errorf("failed to check affected rows: %v", err)
		}
		if rowsAffected == 0 {
			cfg.Logger.Warn("User not found", "user", user)
			return fmt.Errorf("user %s not found", user)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in UpdateEnabledInDB", "user", user, "error", err)
		return err
	}

	cfg.Logger.Info("Enabled status updated", "user", user, "enabled", enabledStr)
	return nil
}

// formatDate formats the subscription end date with timezone.
func formatDate(subEnd string, cfg *config.Config) string {
	cfg.Logger.Debug("Formatting date", "subEnd", subEnd)
	t, err := time.ParseInLocation("2006-01-02-15", subEnd, time.Local)
	if err != nil {
		cfg.Logger.Error("Failed to parse date", "subEnd", subEnd, "error", err)
		return subEnd
	}

	_, offsetSeconds := t.Zone()
	offsetHours := offsetSeconds / 3600
	cfg.Logger.Trace("Formatted date with timezone", "date", t.Format("2006.01.02 15:04"), "offset", offsetHours)
	return fmt.Sprintf("%s UTC%+d", t.Format("2006.01.02 15:04"), offsetHours)
}

// parseAndAdjustDate adjusts a date based on the provided offset.
func parseAndAdjustDate(offset string, baseDate time.Time, cfg *config.Config) (time.Time, error) {
	cfg.Logger.Debug("Parsing and adjusting date", "offset", offset, "baseDate", baseDate.Format("2006-01-02-15"))
	matches := dateOffsetRegex.FindStringSubmatch(offset)
	if matches == nil {
		cfg.Logger.Error("Invalid date offset format", "offset", offset)
		return time.Time{}, fmt.Errorf("invalid format: %s", offset)
	}

	sign := matches[1]
	daysStr := matches[2]
	hoursStr := matches[3]

	days, _ := strconv.Atoi(daysStr)
	hours := 0
	if hoursStr != "" {
		hours, _ = strconv.Atoi(hoursStr)
	}

	if sign == "-" {
		days = -days
		hours = -hours
	}

	newDate := baseDate.AddDate(0, 0, days).Add(time.Duration(hours) * time.Hour)
	cfg.Logger.Trace("Adjusted date", "newDate", newDate.Format("2006-01-02-15"))
	return newDate, nil
}

// AdjustDateOffset updates the subscription end date for a user in the database.
func AdjustDateOffset(manager *manager.DatabaseManager, cfg *config.Config, user, offset string, baseDate time.Time) error {
	cfg.Logger.Debug("Starting to update subscription date", "user", user, "offset", offset)
	offset = strings.TrimSpace(offset)
	cfg.Logger.Trace("Processed offset", "offset", offset)

	if offset == "" {
		cfg.Logger.Warn("Empty offset value for user", "user", user)
		return fmt.Errorf("empty offset value")
	}

	if offset == "0" {
		err := manager.ExecuteHighPriority(func(db *sql.DB) error {
			tx, err := db.Begin()
			if err != nil {
				cfg.Logger.Error("Failed to start transaction", "user", user, "error", err)
				return fmt.Errorf("failed to start transaction: %v", err)
			}
			defer tx.Rollback()

			cfg.Logger.Debug("Executing reset sub_end query", "user", user)
			result, err := tx.Exec("UPDATE clients_stats SET sub_end = '' WHERE user = ?", user)
			if err != nil {
				cfg.Logger.Error("Failed to update database", "user", user, "error", err)
				return fmt.Errorf("failed to update database: %v", err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				cfg.Logger.Error("Failed to check affected rows", "user", user, "error", err)
				return fmt.Errorf("failed to check affected rows: %v", err)
			}
			if rowsAffected == 0 {
				cfg.Logger.Warn("User not found", "user", user)
				return fmt.Errorf("user %s not found", user)
			}

			cfg.Logger.Debug("Committing transaction")
			if err := tx.Commit(); err != nil {
				cfg.Logger.Error("Failed to commit transaction", "error", err)
				return fmt.Errorf("failed to commit transaction: %v", err)
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in AdjustDateOffset while resetting subscription", "user", user, "error", err)
			return err
		}

		cfg.Logger.Info("Set unlimited subscription time for user", "user", user)
		return nil
	}

	newDate, err := parseAndAdjustDate(offset, baseDate, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to parse date offset", "user", user, "offset", offset, "error", err)
		return fmt.Errorf("invalid offset format: %v", err)
	}

	err = manager.ExecuteHighPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "user", user, "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Executing update sub_end query", "user", user, "new_date", newDate.Format("2006-01-02-15"))
		result, err := tx.Exec("UPDATE clients_stats SET sub_end = ? WHERE user = ?", newDate.Format("2006-01-02-15"), user)
		if err != nil {
			cfg.Logger.Error("Failed to update database", "user", user, "error", err)
			return fmt.Errorf("failed to update database: %v", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			cfg.Logger.Error("Failed to check affected rows", "user", user, "error", err)
			return fmt.Errorf("failed to check affected rows: %v", err)
		}
		if rowsAffected == 0 {
			cfg.Logger.Warn("User not found", "user", user)
			return fmt.Errorf("user %s not found", user)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in AdjustDateOffset while updating subscription", "user", user, "error", err)
		return err
	}

	cfg.Logger.Info("Subscription date updated", "user", user, "base_date", baseDate.Format("2006-01-02-15"), "new_date", newDate.Format("2006-01-02-15"), "offset", offset)
	return nil
}

// Subscription represents subscription data from clients_stats table.
type Subscription struct {
	User    string
	SubEnd  string
	UUID    string
	Enabled string
	Renew   int
}

// CheckExpiredSubscriptions checks for expired subscriptions and updates user statuses.
func CheckExpiredSubscriptions(manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Starting to check expired subscriptions")
	var subscriptions []Subscription
	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Reading subscriptions from clients_stats")
		rows, err := db.Query("SELECT user, sub_end, uuid, enabled, renew FROM clients_stats WHERE sub_end IS NOT NULL")
		if err != nil {
			cfg.Logger.Error("Failed to query database", "error", err)
			return fmt.Errorf("failed to query database: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var s Subscription
			if err := rows.Scan(&s.User, &s.SubEnd, &s.UUID, &s.Enabled, &s.Renew); err != nil {
				cfg.Logger.Error("Failed to scan row", "error", err)
				continue
			}
			cfg.Logger.Trace("Read subscription", "user", s.User, "sub_end", s.SubEnd, "enabled", s.Enabled, "renew", s.Renew)
			subscriptions = append(subscriptions, s)
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Error iterating rows", "error", err)
			return fmt.Errorf("error iterating rows: %v", err)
		}

		if len(subscriptions) == 0 {
			cfg.Logger.Warn("No subscriptions found in clients_stats")
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in CheckExpiredSubscriptions while reading subscriptions", "error", err)
		return err
	}

	cfg.Logger.Info("Read subscriptions", "count", len(subscriptions))
	canSendNotifications := cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != ""
	if !canSendNotifications {
		cfg.Logger.Warn("Telegram notifications not configured")
	}

	for _, s := range subscriptions {
		if s.SubEnd != "" {
			cfg.Logger.Trace("Processing subscription", "user", s.User, "sub_end", s.SubEnd)
			subEnd, err := time.Parse("2006-01-02-15", s.SubEnd)
			if err != nil {
				cfg.Logger.Error("Failed to parse date", "user", s.User, "sub_end", s.SubEnd, "error", err)
				continue
			}

			if subEnd.Before(time.Now()) {
				cfg.Logger.Debug("Subscription expired", "user", s.User, "sub_end", s.SubEnd)
				if canSendNotifications && !notifiedUsers[s.User] {
					formattedDate := formatDate(s.SubEnd, cfg)
					message := fmt.Sprintf(
						"❌ Subscription expired\n\n"+
							"Client:   *%s*\n"+
							"End date:   *%s*",
						s.User, formattedDate)
					cfg.Logger.Trace("Sending expiration notification", "user", s.User)
					if err := telegram.SendNotification(cfg, message); err == nil {
						notifiedMutex.Lock()
						notifiedUsers[s.User] = true
						notifiedMutex.Unlock()
						cfg.Logger.Info("Expiration notification sent", "user", s.User)
					} else {
						cfg.Logger.Error("Failed to send notification", "user", s.User, "error", err)
					}
				}

				if s.Renew >= 1 {
					offset := fmt.Sprintf("%d", s.Renew)
					cfg.Logger.Debug("Renewing subscription", "user", s.User, "offset", offset)
					err = AdjustDateOffset(manager, cfg, s.User, offset, time.Now())
					if err != nil {
						cfg.Logger.Error("Failed to renew subscription", "user", s.User, "error", err)
						continue
					}
					cfg.Logger.Info("Subscription auto-renewed", "user", s.User, "renew_days", s.Renew)

					if canSendNotifications {
						notifiedMutex.Lock()
						message := fmt.Sprintf(
							"✅ Subscription renewed\n\n"+
								"Client:   *%s*\n"+
								"Renewed for:   *%d days*",
							s.User, s.Renew)
						if err := telegram.SendNotification(cfg, message); err == nil {
							renewNotifiedUsers[s.User] = true
							cfg.Logger.Info("Renewal notification sent", "user", s.User)
						} else {
							cfg.Logger.Error("Failed to send renewal notification", "user", s.User, "error", err)
						}
						notifiedMutex.Unlock()
					}

					notifiedMutex.Lock()
					notifiedUsers[s.User] = false
					renewNotifiedUsers[s.User] = false
					notifiedMutex.Unlock()

					if s.Enabled == "false" {
						cfg.Logger.Debug("Enabling user", "user", s.User)
						err = ToggleUserEnabled(manager, cfg, s.User, true)
						if err != nil {
							cfg.Logger.Error("Failed to enable user", "user", s.User, "error", err)
							continue
						}
						err = UpdateEnabledInDB(manager, cfg, s.User, true)
						if err != nil {
							cfg.Logger.Error("Failed to update enabled status", "user", s.User, "error", err)
							continue
						}
						cfg.Logger.Info("User enabled after renewal", "user", s.User)
					}
				} else if s.Enabled == "true" {
					cfg.Logger.Debug("Disabling user", "user", s.User)
					err = ToggleUserEnabled(manager, cfg, s.User, false)
					if err != nil {
						cfg.Logger.Error("Failed to disable user", "user", s.User, "error", err)
						continue
					}
					err = UpdateEnabledInDB(manager, cfg, s.User, false)
					if err != nil {
						cfg.Logger.Error("Failed to update enabled status", "user", s.User, "error", err)
						continue
					}
					cfg.Logger.Info("User disabled", "user", s.User)
				}
			} else {
				if s.Enabled == "false" {
					cfg.Logger.Debug("Enabling user with active subscription", "user", s.User)
					err = ToggleUserEnabled(manager, cfg, s.User, true)
					if err != nil {
						cfg.Logger.Error("Failed to enable user", "user", s.User, "error", err)
						continue
					}
					err = UpdateEnabledInDB(manager, cfg, s.User, true)
					if err != nil {
						cfg.Logger.Error("Failed to update enabled status", "user", s.User, "error", err)
						continue
					}
					cfg.Logger.Info("Subscription active, user enabled", "user", s.User, "sub_end", s.SubEnd)
				}
			}
		}
	}

	cfg.Logger.Debug("Finished checking expired subscriptions")
	return nil
}

// CleanInvalidTrafficTags removes non-existent tags from traffic_stats table.
func CleanInvalidTrafficTags(manager *manager.DatabaseManager, cfg *config.Config) error {
	cfg.Logger.Debug("Starting to clean invalid traffic tags")
	var trafficSources []string
	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Reading tags from traffic_stats")
		rows, err := db.Query("SELECT source FROM traffic_stats")
		if err != nil {
			cfg.Logger.Error("Failed to query traffic_stats tags", "error", err)
			return fmt.Errorf("failed to query traffic_stats tags: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var source string
			if err := rows.Scan(&source); err != nil {
				cfg.Logger.Error("Failed to scan row from traffic_stats", "error", err)
				return fmt.Errorf("failed to scan row from traffic_stats: %v", err)
			}
			cfg.Logger.Trace("Read tag", "source", source)
			trafficSources = append(trafficSources, source)
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Error iterating rows", "error", err)
			return fmt.Errorf("error iterating rows: %v", err)
		}

		if len(trafficSources) == 0 {
			cfg.Logger.Warn("No tags found in traffic_stats")
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in CleanInvalidTrafficTags while reading tags", "error", err)
		return err
	}

	cfg.Logger.Info("Read tags from database", "count", len(trafficSources))
	data, err := os.ReadFile(cfg.Core.Config)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", cfg.Core.Config, "error", err)
		return fmt.Errorf("failed to read config.json: %v", err)
	}

	if len(data) == 0 {
		cfg.Logger.Warn("Config file is empty", "path", cfg.Core.Config)
		return nil
	}

	cfg.Logger.Debug("Reading configuration", "path", cfg.Core.Config)
	validTags := make(map[string]bool)
	switch cfg.V2rayStat.Type {
	case "xray":
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			cfg.Logger.Error("Failed to parse JSON for Xray", "error", err)
			return fmt.Errorf("failed to parse JSON for Xray: %v", err)
		}
		for _, inbound := range cfgXray.Inbounds {
			cfg.Logger.Trace("Processing inbound tag", "tag", inbound.Tag)
			validTags[inbound.Tag] = true
		}
		for _, outbound := range cfgXray.Outbounds {
			if tag, ok := outbound["tag"].(string); ok {
				cfg.Logger.Trace("Processing outbound tag", "tag", tag)
				validTags[tag] = true
			}
		}
	case "singbox":
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			cfg.Logger.Error("Failed to parse JSON for Singbox", "error", err)
			return fmt.Errorf("failed to parse JSON for Singbox: %v", err)
		}
		for _, inbound := range cfgSingbox.Inbounds {
			cfg.Logger.Trace("Processing inbound tag", "tag", inbound.Tag)
			validTags[inbound.Tag] = true
		}
		for _, outbound := range cfgSingbox.Outbounds {
			if tag, ok := outbound["tag"].(string); ok {
				cfg.Logger.Trace("Processing outbound tag", "tag", tag)
				validTags[tag] = true
			}
		}
	}

	cfg.Logger.Info("Found valid tags", "count", len(validTags))
	var invalidTags []string
	for _, source := range trafficSources {
		if !validTags[source] {
			cfg.Logger.Debug("Found invalid tag", "source", source)
			invalidTags = append(invalidTags, source)
		}
	}

	if len(invalidTags) == 0 {
		cfg.Logger.Debug("No invalid tags found for deletion")
		return nil
	}

	err = manager.ExecuteLowPriority(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			cfg.Logger.Error("Failed to start transaction", "error", err)
			return fmt.Errorf("failed to start transaction: %v", err)
		}
		defer tx.Rollback()

		cfg.Logger.Debug("Preparing delete statement for invalid tags")
		stmt, err := tx.Prepare("DELETE FROM traffic_stats WHERE source = ?")
		if err != nil {
			cfg.Logger.Error("Failed to prepare delete statement", "error", err)
			return fmt.Errorf("failed to prepare statement: %v", err)
		}
		defer stmt.Close()

		for _, source := range invalidTags {
			cfg.Logger.Trace("Deleting tag", "source", source)
			_, err := stmt.Exec(source)
			if err != nil {
				cfg.Logger.Error("Failed to delete tag", "source", source, "error", err)
				return fmt.Errorf("failed to delete tag %s: %v", source, err)
			}
			cfg.Logger.Debug("Tag deleted successfully", "source", source)
		}

		cfg.Logger.Debug("Committing transaction")
		if err := tx.Commit(); err != nil {
			cfg.Logger.Error("Failed to commit transaction", "error", err)
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in CleanInvalidTrafficTags while deleting tags", "error", err)
		return err
	}

	cfg.Logger.Info("Invalid tags deleted from traffic_stats", "tags", strings.Join(invalidTags, ", "), "count", len(invalidTags))
	return nil
}

// ToggleUserEnabled toggles the enabled status of a user in the config files.
func ToggleUserEnabled(manager *manager.DatabaseManager, cfg *config.Config, userIdentifier string, enabled bool) error {
	cfg.Logger.Debug("Toggling user enabled status", "user", userIdentifier, "enabled", enabled)
	mainConfigPath := cfg.Core.Config
	disabledUsersPath := filepath.Join(cfg.Core.Dir, ".disabled_users")

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	cfg.Logger.Trace("User status", "user", userIdentifier, "status", status)

	switch cfg.V2rayStat.Type {
	case "xray":
		mainConfigData, err := os.ReadFile(mainConfigPath)
		if err != nil {
			cfg.Logger.Error("Failed to read Xray main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error reading Xray main config: %v", err)
		}
		var mainConfig config.ConfigXray
		if err := json.Unmarshal(mainConfigData, &mainConfig); err != nil {
			cfg.Logger.Error("Failed to parse Xray main config", "error", err)
			return fmt.Errorf("error parsing Xray main config: %v", err)
		}

		var disabledConfig config.DisabledUsersConfigXray
		disabledConfigData, err := os.ReadFile(disabledUsersPath)
		if err != nil {
			if os.IsNotExist(err) {
				cfg.Logger.Warn("Disabled users file does not exist", "path", disabledUsersPath)
				disabledConfig = config.DisabledUsersConfigXray{Inbounds: []config.XrayInbound{}}
			} else {
				cfg.Logger.Error("Failed to read Xray disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error reading Xray disabled users file: %v", err)
			}
		} else if len(disabledConfigData) == 0 {
			cfg.Logger.Warn("Empty disabled users file", "path", disabledUsersPath)
			disabledConfig = config.DisabledUsersConfigXray{Inbounds: []config.XrayInbound{}}
		} else {
			if err := json.Unmarshal(disabledConfigData, &disabledConfig); err != nil {
				cfg.Logger.Error("Failed to parse Xray disabled users file", "error", err)
				return fmt.Errorf("error parsing Xray disabled users file: %v", err)
			}
		}

		sourceInbounds := mainConfig.Inbounds
		targetInbounds := disabledConfig.Inbounds
		if enabled {
			sourceInbounds = disabledConfig.Inbounds
			targetInbounds = mainConfig.Inbounds
		}

		userMap := make(map[string]config.XrayClient)
		found := false
		for i, inbound := range sourceInbounds {
			if inbound.Protocol == "vless" || inbound.Protocol == "trojan" {
				newClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients))
				clientMap := make(map[string]bool)
				for _, client := range inbound.Settings.Clients {
					cfg.Logger.Trace("Processing client in inbound", "tag", inbound.Tag, "email", client.Email)
					if client.Email == userIdentifier {
						if !clientMap[client.Email] {
							userMap[inbound.Tag] = client
							clientMap[client.Email] = true
							found = true
						}
					} else {
						if !clientMap[client.Email] {
							newClients = append(newClients, client)
							clientMap[client.Email] = true
						}
					}
				}
				sourceInbounds[i].Settings.Clients = newClients
			}
		}

		if !found {
			cfg.Logger.Error("User not found in inbounds with vless or trojan protocols", "user", userIdentifier)
			return fmt.Errorf("user %s not found in inbounds with vless or trojan protocols", userIdentifier)
		}

		for _, inbound := range targetInbounds {
			if inbound.Protocol == "vless" || inbound.Protocol == "trojan" {
				for _, client := range inbound.Settings.Clients {
					if client.Email == userIdentifier {
						cfg.Logger.Error("User already exists in target Xray config", "user", userIdentifier, "tag", inbound.Tag)
						return fmt.Errorf("user %s already exists in target Xray config with tag %s", userIdentifier, inbound.Tag)
					}
				}
			}
		}

		for i, inbound := range targetInbounds {
			if inbound.Protocol == "vless" || inbound.Protocol == "trojan" {
				if client, exists := userMap[inbound.Tag]; exists {
					clientMap := make(map[string]bool)
					newClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients)+1)
					for _, c := range inbound.Settings.Clients {
						if !clientMap[c.Email] {
							newClients = append(newClients, c)
							clientMap[c.Email] = true
						}
					}
					if !clientMap[userIdentifier] {
						newClients = append(newClients, client)
						cfg.Logger.Info("User set to status in inbound", "user", userIdentifier, "status", status, "tag", inbound.Tag)
					}
					targetInbounds[i].Settings.Clients = newClients
				}
			}
		}

		for _, mainInbound := range mainConfig.Inbounds {
			if (mainInbound.Protocol == "vless" || mainInbound.Protocol == "trojan") && !hasInboundXray(targetInbounds, mainInbound.Tag) {
				if client, exists := userMap[mainInbound.Tag]; exists {
					newInbound := mainInbound
					newInbound.Settings.Clients = []config.XrayClient{client}
					targetInbounds = append(targetInbounds, newInbound)
					cfg.Logger.Info("Created new inbound for user", "tag", newInbound.Tag, "user", userIdentifier)
				}
			}
		}

		if enabled {
			mainConfig.Inbounds = targetInbounds
			disabledConfig.Inbounds = sourceInbounds
		} else {
			mainConfig.Inbounds = sourceInbounds
			disabledConfig.Inbounds = targetInbounds
		}

		mainConfigData, err = json.MarshalIndent(mainConfig, "", "  ")
		if err != nil {
			cfg.Logger.Error("Failed to serialize Xray main config", "error", err)
			return fmt.Errorf("error serializing Xray main config: %v", err)
		}
		if err := os.WriteFile(mainConfigPath, mainConfigData, 0644); err != nil {
			cfg.Logger.Error("Failed to write Xray main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error writing Xray main config: %v", err)
		}

		if len(disabledConfig.Inbounds) > 0 {
			disabledConfigData, err = json.MarshalIndent(disabledConfig, "", "  ")
			if err != nil {
				cfg.Logger.Error("Failed to serialize Xray disabled users file", "error", err)
				return fmt.Errorf("error serializing Xray disabled users file: %v", err)
			}
			if err := os.WriteFile(disabledUsersPath, disabledConfigData, 0644); err != nil {
				cfg.Logger.Error("Failed to write Xray disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error writing Xray disabled users file: %v", err)
			}
		} else {
			if err := os.Remove(disabledUsersPath); err != nil && !os.IsNotExist(err) {
				cfg.Logger.Error("Failed to remove empty .disabled_users for Xray", "error", err)
			}
		}

	case "singbox":
		mainConfigData, err := os.ReadFile(mainConfigPath)
		if err != nil {
			cfg.Logger.Error("Failed to read Singbox main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error reading Singbox main config: %v", err)
		}
		var mainConfig config.ConfigSingbox
		if err := json.Unmarshal(mainConfigData, &mainConfig); err != nil {
			cfg.Logger.Error("Failed to parse Singbox main config", "error", err)
			return fmt.Errorf("error parsing Singbox main config: %v", err)
		}

		var disabledConfig config.DisabledUsersConfigSingbox
		disabledConfigData, err := os.ReadFile(disabledUsersPath)
		if err != nil {
			if os.IsNotExist(err) {
				cfg.Logger.Warn("Disabled users file does not exist", "path", disabledUsersPath)
				disabledConfig = config.DisabledUsersConfigSingbox{Inbounds: []config.SingboxInbound{}}
			} else {
				cfg.Logger.Error("Failed to read Singbox disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error reading Singbox disabled users file: %v", err)
			}
		} else if len(disabledConfigData) == 0 {
			cfg.Logger.Warn("Empty disabled users file", "path", disabledUsersPath)
			disabledConfig = config.DisabledUsersConfigSingbox{Inbounds: []config.SingboxInbound{}}
		} else {
			if err := json.Unmarshal(disabledConfigData, &disabledConfig); err != nil {
				cfg.Logger.Error("Failed to parse Singbox disabled users file", "error", err)
				return fmt.Errorf("error parsing Singbox disabled users file: %v", err)
			}
		}

		sourceInbounds := mainConfig.Inbounds
		targetInbounds := disabledConfig.Inbounds
		if enabled {
			sourceInbounds = disabledConfig.Inbounds
			targetInbounds = mainConfig.Inbounds
		}

		userMap := make(map[string]config.SingboxClient)
		found := false
		for i, inbound := range sourceInbounds {
			if inbound.Type == "vless" || inbound.Type == "trojan" {
				newUsers := make([]config.SingboxClient, 0, len(inbound.Users))
				userNameMap := make(map[string]bool)
				for _, user := range inbound.Users {
					cfg.Logger.Trace("Processing user in inbound", "tag", inbound.Tag, "name", user.Name)
					if user.Name == userIdentifier {
						if !userNameMap[user.Name] {
							userMap[inbound.Tag] = user
							userNameMap[user.Name] = true
							found = true
						}
					} else {
						if !userNameMap[user.Name] {
							newUsers = append(newUsers, user)
							userNameMap[user.Name] = true
						}
					}
				}
				sourceInbounds[i].Users = newUsers
			}
		}

		if !found {
			cfg.Logger.Error("User not found in inbounds with vless or trojan protocols for Singbox", "user", userIdentifier)
			return fmt.Errorf("user %s not found in inbounds with vless or trojan protocols for Singbox", userIdentifier)
		}

		for _, inbound := range targetInbounds {
			if inbound.Type == "vless" || inbound.Type == "trojan" {
				for _, user := range inbound.Users {
					if user.Name == userIdentifier {
						cfg.Logger.Error("User already exists in target Singbox config", "user", userIdentifier, "tag", inbound.Tag)
						return fmt.Errorf("user %s already exists in target Singbox config with tag %s", userIdentifier, inbound.Tag)
					}
				}
			}
		}

		for i, inbound := range targetInbounds {
			if inbound.Type == "vless" || inbound.Type == "trojan" {
				if user, exists := userMap[inbound.Tag]; exists {
					userNameMap := make(map[string]bool)
					newUsers := make([]config.SingboxClient, 0, len(inbound.Users)+1)
					for _, u := range inbound.Users {
						if !userNameMap[u.Name] {
							newUsers = append(newUsers, u)
							userNameMap[u.Name] = true
						}
					}
					if !userNameMap[userIdentifier] {
						newUsers = append(newUsers, user)
						cfg.Logger.Info("User set to status in inbound", "user", userIdentifier, "status", status, "tag", inbound.Tag)
					}
					targetInbounds[i].Users = newUsers
				}
			}
		}

		for _, mainInbound := range mainConfig.Inbounds {
			if (mainInbound.Type == "vless" || mainInbound.Type == "trojan") && !hasInboundSingbox(targetInbounds, mainInbound.Tag) {
				if user, exists := userMap[mainInbound.Tag]; exists {
					newInbound := mainInbound
					newInbound.Users = []config.SingboxClient{user}
					targetInbounds = append(targetInbounds, newInbound)
					cfg.Logger.Info("Created new inbound for user", "tag", newInbound.Tag, "user", userIdentifier)
				}
			}
		}

		if enabled {
			mainConfig.Inbounds = targetInbounds
			disabledConfig.Inbounds = sourceInbounds
		} else {
			mainConfig.Inbounds = sourceInbounds
			disabledConfig.Inbounds = targetInbounds
		}

		mainConfigData, err = json.MarshalIndent(mainConfig, "", "  ")
		if err != nil {
			cfg.Logger.Error("Failed to serialize Singbox main config", "error", err)
			return fmt.Errorf("error serializing Singbox main config: %v", err)
		}
		if err := os.WriteFile(mainConfigPath, mainConfigData, 0644); err != nil {
			cfg.Logger.Error("Failed to write Singbox main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error writing Singbox main config: %v", err)
		}

		if len(disabledConfig.Inbounds) > 0 {
			disabledConfigData, err = json.MarshalIndent(disabledConfig, "", "  ")
			if err != nil {
				cfg.Logger.Error("Failed to serialize Singbox disabled users file", "error", err)
				return fmt.Errorf("error serializing Singbox disabled users file: %v", err)
			}
			if err := os.WriteFile(disabledUsersPath, disabledConfigData, 0644); err != nil {
				cfg.Logger.Error("Failed to write Singbox disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error writing Singbox disabled users file: %v", err)
			}
		} else {
			if err := os.Remove(disabledUsersPath); err != nil && !os.IsNotExist(err) {
				cfg.Logger.Error("Failed to remove empty .disabled_users for Singbox", "error", err)
			}
		}
	}

	cfg.Logger.Info("User enabled status toggled", "user", userIdentifier, "enabled", enabled)
	return nil
}

// hasInboundXray checks if an inbound with the given tag exists for Xray.
func hasInboundXray(inbounds []config.XrayInbound, tag string) bool {
	for _, inbound := range inbounds {
		if inbound.Tag == tag {
			return true
		}
	}
	return false
}

// hasInboundSingbox checks if an inbound with the given tag exists for Singbox.
func hasInboundSingbox(inbounds []config.SingboxInbound, tag string) bool {
	for _, inbound := range inbounds {
		if inbound.Tag == tag {
			return true
		}
	}
	return false
}

// LoadIsInactiveFromLastSeen loads user inactivity status from last_seen column.
func LoadIsInactiveFromLastSeen(manager *manager.DatabaseManager, cfg *config.Config) (map[string]bool, error) {
	cfg.Logger.Debug("Loading user inactivity status from last_seen")
	isInactive := make(map[string]bool)
	err := manager.ExecuteHighPriority(func(db *sql.DB) error {
		rows, err := db.Query("SELECT user, last_seen FROM clients_stats")
		if err != nil {
			cfg.Logger.Error("Failed to query users", "error", err)
			return fmt.Errorf("failed to query users: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var user, lastSeen string
			if err := rows.Scan(&user, &lastSeen); err != nil {
				cfg.Logger.Warn("Failed to scan row", "user", user, "error", err)
				continue
			}
			cfg.Logger.Trace("Processing user", "user", user, "last_seen", lastSeen)
			isInactive[user] = lastSeen != "online"
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Error iterating rows", "error", err)
			return fmt.Errorf("error iterating rows: %v", err)
		}
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Error in LoadIsInactiveFromLastSeen", "error", err)
		return nil, err
	}
	cfg.Logger.Debug("Loaded user activity data", "count", len(isInactive))
	return isInactive, nil
}

// InitDB initializes the database table structure.
func InitDB(db *sql.DB, dbType string, cfg *config.Config) error {
	cfg.Logger.Debug("Starting database initialization", "dbType", dbType)
	var tableCount int
	err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='clients_stats'").Scan(&tableCount)
	if err != nil {
		cfg.Logger.Error("Failed to check table existence", "dbType", dbType, "error", err)
		return fmt.Errorf("failed to check table existence for database %s: %v", dbType, err)
	}
	if tableCount > 0 {
		cfg.Logger.Debug("Tables already exist, initialization not required", "dbType", dbType)
		return nil
	}

	sqlStmt := `
        PRAGMA cache_size = 2000;
        PRAGMA journal_mode = WAL;
        PRAGMA synchronous = NORMAL;
        PRAGMA temp_store = MEMORY;
        PRAGMA busy_timeout = 5000;

        CREATE TABLE IF NOT EXISTS clients_stats (
            user TEXT PRIMARY KEY,
            uuid TEXT,
            last_seen TEXT DEFAULT '',
            rate INTEGER DEFAULT 0,
            enabled TEXT,
            sub_end TEXT DEFAULT '',
            renew INTEGER DEFAULT 0,
            lim_ip INTEGER DEFAULT 0,
            ips TEXT DEFAULT '',
            uplink INTEGER DEFAULT 0,
            downlink INTEGER DEFAULT 0,
            sess_uplink INTEGER DEFAULT 0,
            sess_downlink INTEGER DEFAULT 0,
            created TEXT
        );

        CREATE TABLE IF NOT EXISTS traffic_stats (
            source TEXT PRIMARY KEY,
            rate INTEGER DEFAULT 0,
            uplink INTEGER DEFAULT 0,
            downlink INTEGER DEFAULT 0,
            sess_uplink INTEGER DEFAULT 0,
            sess_downlink INTEGER DEFAULT 0
        );

        CREATE TABLE IF NOT EXISTS dns_stats (
            user TEXT NOT NULL,
            count INTEGER DEFAULT 1,
            domain TEXT NOT NULL,
            PRIMARY KEY (user, domain)
        );

        CREATE INDEX IF NOT EXISTS idx_clients_stats_user ON clients_stats(user);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_rate ON clients_stats(rate);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_enabled ON clients_stats(enabled);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_sub_end ON clients_stats(sub_end);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_renew ON clients_stats(renew);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_sess_uplink ON clients_stats(sess_uplink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_sess_downlink ON clients_stats(sess_downlink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_uplink ON clients_stats(uplink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_downlink ON clients_stats(downlink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_lim_ip ON clients_stats(lim_ip);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_ips ON clients_stats(ips);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_last_seen ON clients_stats(last_seen);
    `
	cfg.Logger.Debug("Executing SQL script to create tables and indexes", "dbType", dbType)
	if _, err = db.Exec(sqlStmt); err != nil {
		cfg.Logger.Error("Failed to execute SQL script", "dbType", dbType, "error", err)
		return fmt.Errorf("failed to execute SQL script for database %s: %v", dbType, err)
	}

	cfg.Logger.Debug("Successfully created PRAGMA settings, tables, and indexes", "dbType", dbType)
	return nil
}

// InitDatabase initializes in-memory and file databases.
func InitDatabase(cfg *config.Config) (memDB, fileDB *sql.DB, err error) {
	cfg.Logger.Debug("Creating in-memory database")
	memDB, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		cfg.Logger.Error("Failed to create in-memory database", "error", err)
		return nil, nil, fmt.Errorf("failed to create in-memory database: %v", err)
	}
	memDB.SetMaxOpenConns(1)
	memDB.SetMaxIdleConns(1)

	cfg.Logger.Debug("Initializing in-memory database")
	if err = InitDB(memDB, "in-memory", cfg); err != nil {
		cfg.Logger.Error("Failed to initialize in-memory database", "error", err)
		memDB.Close()
		return nil, nil, fmt.Errorf("failed to initialize in-memory database: %v", err)
	}

	cfg.Logger.Debug("Opening file database", "path", cfg.Paths.Database)
	fileDB, err = sql.Open("sqlite3", cfg.Paths.Database)
	if err != nil {
		cfg.Logger.Error("Failed to open file database", "path", cfg.Paths.Database, "error", err)
		memDB.Close()
		return nil, nil, fmt.Errorf("failed to open file database: %v", err)
	}
	fileDB.SetMaxOpenConns(1)
	fileDB.SetMaxIdleConns(1)

	fileExists := true
	if _, err := os.Stat(cfg.Paths.Database); os.IsNotExist(err) {
		cfg.Logger.Warn("File database does not exist, will create new file database", "path", cfg.Paths.Database)
		fileExists = false
	} else if err != nil {
		cfg.Logger.Error("Failed to check file database", "path", cfg.Paths.Database, "error", err)
		memDB.Close()
		fileDB.Close()
		return nil, nil, fmt.Errorf("error checking file database: %v", err)
	}

	if fileExists {
		var tableCount int
		err = fileDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='clients_stats'").Scan(&tableCount)
		if err == nil && tableCount > 0 {
			cfg.Logger.Debug("Synchronizing file to memory database")
			tempManager, err := manager.NewDatabaseManager(fileDB, context.Background(), 1, 50, 100, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to create temporary DatabaseManager", "error", err)
				memDB.Close()
				fileDB.Close()
				return nil, nil, fmt.Errorf("failed to create temporary DatabaseManager: %v", err)
			}
			syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err = tempManager.SyncDBWithContext(syncCtx, memDB, "file to memory"); err != nil {
				cfg.Logger.Error("Failed to synchronize database (file to memory)", "error", err)
			} else {
				cfg.Logger.Info("Database synchronized successfully (file to memory)")
			}
			syncCancel()
			tempManager.Close()
		}
	}

	cfg.Logger.Debug("Initializing file database")
	if err = InitDB(fileDB, "file", cfg); err != nil {
		cfg.Logger.Error("Failed to initialize file database", "error", err)
		memDB.Close()
		fileDB.Close()
		return nil, nil, fmt.Errorf("failed to initialize file database: %v", err)
	}

	cfg.Logger.Info("Database initialization completed", "in-memory", true, "file", true)
	return memDB, fileDB, nil
}

// MonitorSubscriptionsAndSync runs periodic subscription checks and database synchronization.
func MonitorSubscriptionsAndSync(ctx context.Context, manager *manager.DatabaseManager, fileDB *sql.DB, cfg *config.Config, wg *sync.WaitGroup) {
	cfg.Logger.Debug("Starting subscription and sync monitoring")
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cfg.Logger.Debug("Running periodic subscription and sync check")
				if err := CleanInvalidTrafficTags(manager, cfg); err != nil {
					cfg.Logger.Error("Failed to clean invalid tags", "error", err)
				}
				if err := CheckExpiredSubscriptions(manager, cfg); err != nil {
					cfg.Logger.Error("Failed to check subscriptions", "error", err)
				}
				syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := manager.SyncDBWithContext(syncCtx, fileDB, "memory to file"); err != nil {
					cfg.Logger.Error("Failed to synchronize database (memory to file)", "error", err)
				} else {
					cfg.Logger.Info("Database synchronized successfully (memory to file)")
				}
				syncCancel()
			case <-ctx.Done():
				cfg.Logger.Debug("Stopped subscription and sync monitoring")
				return
			}
		}
	}()
}
