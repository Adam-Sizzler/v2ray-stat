package stats

import (
	"context"
	"fmt"
	"sync"
	"time"

	"v2ray-stat/config"
	"v2ray-stat/constant"
	"v2ray-stat/db/manager"
	"v2ray-stat/telegram"
)

// SendDailyReport sends a daily Telegram notification with system and network stats.
func SendDailyReport(manager *manager.DatabaseManager, cfg *config.Config) {
	if cfg.Telegram.BotToken == "" || cfg.Telegram.ChatID == "" {
		cfg.Logger.Error("Failed to send daily report: missing TelegramBotToken or TelegramChatID")
		return
	}

	cfg.Logger.Debug("Starting daily report generation")

	coreVersion := getCoreVersion(cfg)
	ipv4, ipv6 := getIPAddresses(cfg)
	uptime := GetUptime(cfg)
	loadAverage := GetLoadAverage(cfg)
	memoryUsage := GetMemoryUsage(cfg)
	tcpCount, udpCount := getConnectionCounts(cfg)
	totalTraffic, uplinkTraffic, downlinkTraffic, err := LoadTrafficStats(manager, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to load traffic stats", "error", err)
		totalTraffic, uplinkTraffic, downlinkTraffic = "0 bytes", "0 bytes", "0 bytes"
	}

	serviceStatus := GetStatus(cfg)
	if serviceStatus == "" {
		cfg.Logger.Warn("No services configured")
		serviceStatus = "no services configured"
	} else {
		cfg.Logger.Info("Service status retrieved", "status", serviceStatus)
	}

	message := fmt.Sprintf(
		"⚙️ v2ray-stat version: %s\n"+
			"📡 %s version: %s\n"+
			"🌐 IPv4: %s\n"+
			"🌐 IPv6: %s\n"+
			"⏳ Uptime: %s\n"+
			"📈 System Load: %s\n"+
			"📋 RAM: %s\n"+
			"🔹 TCP: %d\n"+
			"🔸 UDP: %d\n"+
			"🚦 Traffic: %s (↑%s,↓%s)\n"+
			"ℹ️ Status: %s",
		constant.Version, cfg.V2rayStat.Type, coreVersion, ipv4, ipv6, uptime, loadAverage, memoryUsage, tcpCount, udpCount, totalTraffic, uplinkTraffic, downlinkTraffic, serviceStatus,
	)

	if err := telegram.SendNotification(cfg, message); err != nil {
		cfg.Logger.Error("Failed to send daily report to Telegram", "error", err)
	} else {
		cfg.Logger.Info("Daily report sent successfully to Telegram")
	}
}

// MonitorDailyReport schedules the daily report to run every 24 hours.
func MonitorDailyReport(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		cfg.Logger.Debug("Starting daily report monitoring")
		SendDailyReport(manager, cfg)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cfg.Logger.Debug("Running daily report")
				SendDailyReport(manager, cfg)
			case <-ctx.Done():
				cfg.Logger.Debug("Stopped daily report monitoring")
				return
			}
		}
	}()
}
