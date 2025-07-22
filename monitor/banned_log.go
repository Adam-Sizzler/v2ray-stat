package monitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"v2ray-stat/config"
	"v2ray-stat/telegram"
)

var (
	bannedLogRegex = regexp.MustCompile(`(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})\s+(BAN|UNBAN)\s+\[User\] = (\S+)\s+\[IP\] = (\S+)(?:\s+banned for (\d+) seconds\.)?`)
)

// MonitorBanned reads new entries from the banned log file and sends notifications to Telegram.
func MonitorBanned(bannedLog *os.File, bannedOffset *int64, cfg *config.Config) error {
	// Seek to the last read position in the banned log
	_, err := bannedLog.Seek(*bannedOffset, 0)
	if err != nil {
		cfg.Logger.Error("Failed to seek to banned log position", "error", err)
		return fmt.Errorf("failed to seek to banned log position: %v", err)
	}

	scanner := bufio.NewScanner(bannedLog)
	for scanner.Scan() {
		line := scanner.Text()
		matches := bannedLogRegex.FindStringSubmatch(line)
		if len(matches) < 5 {
			cfg.Logger.Warn("Invalid line in banned log", "line", line)
			continue
		}

		// Extract log details
		timestamp := matches[1]
		action := matches[2]
		user := matches[3]
		ip := matches[4]
		banDuration := "unknown"
		if len(matches) == 6 && matches[5] != "" {
			banDuration = matches[5] + " seconds"
		}

		// Format notification message based on action
		var message string
		if action == "BAN" {
			message = fmt.Sprintf("🚫 IP Banned\n\n"+
				" Client:   *%s*\n"+
				" IP:   *%s*\n"+
				" Time:   *%s*\n"+
				" Duration:   *%s*", user, ip, timestamp, banDuration)
		} else {
			message = fmt.Sprintf("✅ IP Unbanned\n\n"+
				" Client:   *%s*\n"+
				" IP:   *%s*\n"+
				" Time:   *%s*", user, ip, timestamp)
		}

		// Send Telegram notification if configured
		if cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
			if err := telegram.SendNotification(cfg, message); err != nil {
				cfg.Logger.Error("Failed to send ban notification", "error", err)
			} else {
				cfg.Logger.Info("Ban notification sent successfully", "user", user, "ip", ip, "action", action)
			}
		}
	}

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		cfg.Logger.Error("Failed to read banned log", "error", err)
		return fmt.Errorf("failed to read banned log: %v", err)
	}

	// Update the offset to the current position
	pos, err := bannedLog.Seek(0, 1)
	if err != nil {
		cfg.Logger.Error("Failed to retrieve banned log position", "error", err)
		return fmt.Errorf("failed to retrieve banned log position: %v", err)
	}
	*bannedOffset = pos

	return nil
}

// MonitorBannedLog periodically monitors the banned log file for new entries.
func MonitorBannedLog(ctx context.Context, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Open the banned log file
		bannedLog, err := os.OpenFile(cfg.Paths.F2BBannedLog, os.O_RDONLY|os.O_CREATE, 0644)
		if err != nil {
			cfg.Logger.Error("Failed to open banned log file", "path", cfg.Paths.F2BBannedLog, "error", err)
			return
		}
		defer bannedLog.Close()

		// Seek to the end of the file to start monitoring new entries
		var bannedOffset int64
		_, err = bannedLog.Seek(0, 2)
		if err != nil {
			cfg.Logger.Error("Failed to seek to end of banned log file", "error", err)
			return
		}
		bannedOffset, err = bannedLog.Seek(0, 1)
		if err != nil {
			cfg.Logger.Error("Failed to retrieve initial banned log position", "error", err)
			return
		}

		// Start periodic monitoring
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := MonitorBanned(bannedLog, &bannedOffset, cfg); err != nil {
					cfg.Logger.Error("Failed to monitor banned log", "error", err)
				}
			case <-ctx.Done():
				cfg.Logger.Debug("Stopped monitoring banned log due to context cancellation")
				return
			}
		}
	}()
}
