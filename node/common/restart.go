package common

import (
	"fmt"
	"os/exec"
	"v2ray-stat/node/config"
)

// RestartService restarts the specified service using systemctl.
func RestartService(serviceName string, cfg *config.NodeConfig) error {
	cfg.Logger.Debug("Restarting service", "service", serviceName)
	cmd := exec.Command("systemctl", "restart", serviceName)
	if err := cmd.Run(); err != nil {
		cfg.Logger.Error("Error while restarting service", "service", serviceName, "error", err)
		return fmt.Errorf("failed to restart service %s: %v", serviceName, err)
	}
	cfg.Logger.Info("Service restarted successfully", "service", serviceName)
	return nil
}
