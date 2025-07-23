package stats

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"v2ray-stat/config"
)

// NetworkStats holds network interface statistics.
type NetworkStats struct {
	RxBytes, TxBytes     uint64
	RxPackets, TxPackets uint64
}

// TrafficMonitor monitors network traffic for a specified interface.
type TrafficMonitor struct {
	Iface           string
	mu              sync.RWMutex
	rxSpeed         float64
	txSpeed         float64
	rxPacketsPerSec float64
	txPacketsPerSec float64
	totalRxBytes    uint64
	totalTxBytes    uint64
	initialRxBytes  uint64
	initialTxBytes  uint64
	lastStats       NetworkStats
	lastUpdate      time.Time
	isFirstUpdate   bool
}

// NewTrafficMonitor creates a new TrafficMonitor with initial statistics.
func NewTrafficMonitor(cfg *config.Config, iface string) (*TrafficMonitor, error) {
	cfg.Logger.Debug("Initializing traffic monitor", "interface", iface)
	initialStats, err := readNetworkStats(iface, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to initialize traffic monitor", "error", err)
		return nil, fmt.Errorf("failed to initialize traffic monitor: %v", err)
	}

	monitor := &TrafficMonitor{
		Iface:          iface,
		initialRxBytes: initialStats.RxBytes,
		initialTxBytes: initialStats.TxBytes,
		lastStats:      initialStats,
		isFirstUpdate:  true,
	}
	cfg.Logger.Debug("Traffic monitor initialized", "interface", iface)
	return monitor, nil
}

// UpdateStats updates network traffic statistics for one iteration.
func (tm *TrafficMonitor) UpdateStats(cfg *config.Config) error {
	cfg.Logger.Debug("Updating network stats", "interface", tm.Iface)
	stats, err := readNetworkStats(tm.Iface, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to update network stats", "error", err)
		return fmt.Errorf("failed to update network stats: %v", err)
	}

	currentTime := time.Now()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isFirstUpdate {
		deltaTime := currentTime.Sub(tm.lastUpdate).Seconds()
		if deltaTime > 0 {
			tm.rxSpeed = float64((stats.RxBytes-tm.lastStats.RxBytes)*8) / deltaTime
			tm.txSpeed = float64((stats.TxBytes-tm.lastStats.TxBytes)*8) / deltaTime
			tm.rxPacketsPerSec = float64(stats.RxPackets-tm.lastStats.RxPackets) / deltaTime
			tm.txPacketsPerSec = float64(stats.TxPackets-tm.lastStats.TxPackets) / deltaTime
			tm.totalRxBytes = stats.RxBytes - tm.initialRxBytes
			tm.totalTxBytes = stats.TxBytes - tm.initialTxBytes
			cfg.Logger.Debug("Network stats updated", "interface", tm.Iface, "rx_speed", tm.rxSpeed, "tx_speed", tm.txSpeed)
		}
	} else {
		tm.totalRxBytes = 0
		tm.totalTxBytes = 0
		tm.isFirstUpdate = false
		cfg.Logger.Debug("Initial network stats set", "interface", tm.Iface)
	}

	tm.lastStats = stats
	tm.lastUpdate = currentTime
	cfg.Logger.Trace("Network stats update completed", "interface", tm.Iface)
	return nil
}

// ResetTraffic resets accumulated traffic by updating initial values.
func (tm *TrafficMonitor) ResetTraffic(cfg *config.Config) error {
	cfg.Logger.Debug("Resetting traffic stats", "interface", tm.Iface)
	stats, err := readNetworkStats(tm.Iface, cfg)
	if err != nil {
		cfg.Logger.Error("Failed to reset traffic", "error", err)
		return fmt.Errorf("failed to reset traffic: %v", err)
	}

	tm.mu.Lock()
	tm.initialRxBytes = stats.RxBytes
	tm.initialTxBytes = stats.TxBytes
	tm.totalRxBytes = 0
	tm.totalTxBytes = 0
	tm.mu.Unlock()

	cfg.Logger.Debug("Traffic stats reset", "interface", tm.Iface)
	return nil
}

// GetStats returns current network statistics.
func (tm *TrafficMonitor) GetStats() (rxSpeed, txSpeed, rxPacketsPerSec, txPacketsPerSec float64, totalRxBytes, totalTxBytes uint64) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.rxSpeed, tm.txSpeed, tm.rxPacketsPerSec, tm.txPacketsPerSec, tm.totalRxBytes, tm.totalTxBytes
}

// readNetworkStats reads network statistics from /proc/net/dev for the specified interface.
func readNetworkStats(iface string, cfg *config.Config) (NetworkStats, error) {
	cfg.Logger.Debug("Reading network stats from /proc/net/dev", "interface", iface)
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/net/dev", "error", err)
		return NetworkStats{}, fmt.Errorf("failed to read /proc/net/dev: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, iface+":") {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				cfg.Logger.Error("Invalid data format for interface", "interface", iface)
				return NetworkStats{}, fmt.Errorf("invalid data format for interface %s", iface)
			}

			var stats NetworkStats
			_, err := fmt.Sscanf(fields[1], "%d", &stats.RxBytes)
			if err != nil {
				cfg.Logger.Error("Failed to parse rx bytes", "interface", iface, "error", err)
				return NetworkStats{}, fmt.Errorf("failed to parse rx bytes: %v", err)
			}
			_, err = fmt.Sscanf(fields[2], "%d", &stats.RxPackets)
			if err != nil {
				cfg.Logger.Error("Failed to parse rx packets", "interface", iface, "error", err)
				return NetworkStats{}, fmt.Errorf("failed to parse rx packets: %v", err)
			}
			_, err = fmt.Sscanf(fields[9], "%d", &stats.TxBytes)
			if err != nil {
				cfg.Logger.Error("Failed to parse tx bytes", "interface", iface, "error", err)
				return NetworkStats{}, fmt.Errorf("failed to parse tx bytes: %v", err)
			}
			_, err = fmt.Sscanf(fields[10], "%d", &stats.TxPackets)
			if err != nil {
				cfg.Logger.Error("Failed to parse tx packets", "interface", iface, "error", err)
				return NetworkStats{}, fmt.Errorf("failed to parse tx packets: %v", err)
			}
			cfg.Logger.Debug("Network stats read successfully", "interface", iface)
			return stats, nil
		}
	}
	cfg.Logger.Error("Interface not found in /proc/net/dev", "interface", iface)
	return NetworkStats{}, fmt.Errorf("interface %s not found", iface)
}

// MonitorNetwork runs periodic network traffic monitoring.
func MonitorNetwork(ctx context.Context, cfg *config.Config, wg *sync.WaitGroup) {
	cfg.Logger.Debug("Starting network monitoring")
	trafficMonitor := GetTrafficMonitor()
	if trafficMonitor == nil {
		cfg.Logger.Warn("Network monitoring not initialized, skipping routine")
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cfg.Logger.Debug("Running network stats update", "interface", trafficMonitor.Iface)
				if err := trafficMonitor.UpdateStats(cfg); err != nil {
					cfg.Logger.Error("Failed to update network stats", "interface", trafficMonitor.Iface, "error", err)
				} else {
					cfg.Logger.Debug("Network stats updated successfully", "interface", trafficMonitor.Iface)
				}
			case <-ctx.Done():
				cfg.Logger.Debug("Stopped network monitoring")
				return
			}
		}
	}()
}
