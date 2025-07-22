package stats

import (
	"fmt"
	"net"

	"v2ray-stat/config"
)

var trafficMonitor *TrafficMonitor

// setTrafficMonitor sets the global traffic monitor instance.
func setTrafficMonitor(tm *TrafficMonitor) {
	trafficMonitor = tm
}

// GetTrafficMonitor returns the global traffic monitor instance.
func GetTrafficMonitor() *TrafficMonitor {
	return trafficMonitor
}

// GetDefaultInterface returns the second active network interface.
func GetDefaultInterface(cfg *config.Config) (string, error) {
	cfg.Logger.Debug("Searching for default network interface")
	interfaces, err := net.Interfaces()
	if err != nil {
		cfg.Logger.Error("Failed to get network interfaces", "error", err)
		return "", fmt.Errorf("failed to get network interfaces: %v", err)
	}

	count := 0
	for _, i := range interfaces {
		if i.Flags&net.FlagUp == 0 {
			continue
		}

		count++
		if count == 2 {
			cfg.Logger.Debug("Found second active interface", "interface", i.Name)
			return i.Name, nil
		}
	}

	cfg.Logger.Error("Second active interface not found")
	return "", fmt.Errorf("second active interface not found")
}

// InitNetworkMonitoring initializes network traffic monitoring.
func InitNetworkMonitoring(cfg *config.Config) error {
	cfg.Logger.Debug("Initializing network monitoring")
	// Get default network interface
	iface, err := GetDefaultInterface(cfg)
	if err != nil {
		cfg.Logger.Error("Failed to determine default network interface", "error", err)
		return fmt.Errorf("failed to determine default network interface: %v", err)
	}

	// Initialize traffic monitor
	monitor, err := NewTrafficMonitor(cfg, iface)
	if err != nil {
		cfg.Logger.Error("Failed to initialize traffic monitor", "interface", iface, "error", err)
		return fmt.Errorf("failed to initialize traffic monitor for interface %s: %v", iface, err)
	}

	// Save monitor for later use
	setTrafficMonitor(monitor)
	return nil
}
