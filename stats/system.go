package stats

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"v2ray-stat/config"
	"v2ray-stat/db/manager"
	"v2ray-stat/telegram"
	"v2ray-stat/util"
)

var (
	serviceStatuses   = make(map[string]bool)
	isFirstCheck      = true
	statusMutex       sync.Mutex
	diskMutex         sync.Mutex
	memoryMutex       sync.Mutex
	diskExceeded      bool
	memoryExceeded    bool
	diskPercentages   []float64
	memoryPercentages []float64
)

// getCoreVersion retrieves the version of the core binary (xray or sing-box).
func getCoreVersion(cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving core version", "type", cfg.V2rayStat.Type)
	var binaryName string
	switch cfg.V2rayStat.Type {
	case "xray":
		binaryName = "xray"
	case "singbox":
		binaryName = "sing-box"
	}

	binaryPath := filepath.Join(cfg.Core.Dir, binaryName)
	cmd := exec.Command(binaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		cfg.Logger.Error("Failed to retrieve core version", "type", cfg.V2rayStat.Type, "error", err)
		return "unknown"
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if cfg.V2rayStat.Type == "xray" && len(parts) >= 2 {
			cfg.Logger.Debug("Core version retrieved", "type", cfg.V2rayStat.Type, "version", parts[1])
			return parts[1]
		} else if cfg.V2rayStat.Type == "singbox" && len(parts) >= 3 {
			cfg.Logger.Debug("Core version retrieved", "type", cfg.V2rayStat.Type, "version", parts[2])
			return parts[2]
		}
	}
	cfg.Logger.Error("Invalid version output", "type", cfg.V2rayStat.Type)
	return "unknown"
}

// getIPAddresses returns the system's IPv4 and IPv6 addresses.
func getIPAddresses(cfg *config.Config) (ipv4, ipv6 string) {
	cfg.Logger.Debug("Retrieving IP addresses")
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		cfg.Logger.Error("Failed to retrieve IP addresses", "error", err)
		return "unknown", "unknown"
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			cfg.Logger.Trace("Processing IP address", "address", addr.String())
			if ipNet.IP.To4() != nil {
				ipv4 = ipNet.IP.String()
			} else if ipNet.IP.To16() != nil {
				ipv6 = ipNet.IP.String()
			}
		}
	}

	if ipv4 == "" {
		cfg.Logger.Trace("No IPv4 address found")
		ipv4 = "none"
	}
	if ipv6 == "" {
		cfg.Logger.Trace("No IPv6 address found")
		ipv6 = "none"
	}
	cfg.Logger.Debug("IP addresses retrieved", "ipv4", ipv4, "ipv6", ipv6)
	return ipv4, ipv6
}

// GetUptime returns the system uptime.
func GetUptime(cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving system uptime")
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/uptime", "error", err)
		return "unknown"
	}
	var uptimeSeconds float64
	fmt.Sscanf(string(data), "%f", &uptimeSeconds)

	days := int(uptimeSeconds / (24 * 3600))
	hours := int(uptimeSeconds/3600) % 24
	cfg.Logger.Trace("System uptime retrieved", "days", days, "hours", hours)
	return fmt.Sprintf("%d days %02d hours", days, hours)
}

// GetLoadAverage returns the system load average.
func GetLoadAverage(cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving system load average")
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/loadavg", "error", err)
		return "unknown"
	}
	var load1, load5, load15 float64
	fmt.Sscanf(string(data), "%f %f %f", &load1, &load5, &load15)
	cfg.Logger.Trace("System load average retrieved", "load1", load1, "load5", load5, "load15", load15)
	return fmt.Sprintf("%.2f, %.2f, %.2f", load1, load5, load15)
}

// GetMemoryUsage returns memory usage information without sending notifications.
func GetMemoryUsage(cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving memory usage")
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/meminfo", "error", err)
		return "unknown"
	}

	var memTotal, memAvailable uint64
	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			cfg.Logger.Trace("Skipping empty or invalid line in /proc/meminfo")
			continue
		}
		if fields[0] == "MemTotal:" {
			memTotal, _ = strconv.ParseUint(fields[1], 10, 64)
			cfg.Logger.Trace("Parsed MemTotal", "value", memTotal)
		}
		if fields[0] == "MemAvailable:" {
			memAvailable, _ = strconv.ParseUint(fields[1], 10, 64)
			cfg.Logger.Trace("Parsed MemAvailable", "value", memAvailable)
		}
	}

	if memTotal == 0 {
		cfg.Logger.Error("Invalid memory data: MemTotal is zero")
		return "unknown"
	}

	usedMem := memTotal - memAvailable
	cfg.Logger.Trace("Memory usage retrieved", "used_mb", float64(usedMem)/1024, "total_mb", float64(memTotal)/1024)
	return fmt.Sprintf("%.2f MB used / %.2f MB total", float64(usedMem)/1024, float64(memTotal)/1024)
}

// getConnectionCounts returns the number of TCP and UDP connections.
func getConnectionCounts(cfg *config.Config) (tcpCount, udpCount int) {
	cfg.Logger.Debug("Retrieving connection counts")
	tcpData, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/net/tcp", "error", err)
	} else {
		tcpLines := strings.Split(string(tcpData), "\n")
		tcpCount = len(tcpLines) - 1 // Subtract header line
		cfg.Logger.Trace("TCP connections counted", "count", tcpCount)
	}

	udpData, err := os.ReadFile("/proc/net/udp")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/net/udp", "error", err)
	} else {
		udpLines := strings.Split(string(udpData), "\n")
		udpCount = len(udpLines) - 1 // Subtract header line
		cfg.Logger.Trace("UDP connections counted", "count", udpCount)
	}
	return tcpCount, udpCount
}

// LoadTrafficStats retrieves traffic statistics from the database.
func LoadTrafficStats(manager *manager.DatabaseManager, cfg *config.Config) (totalTraffic, uplinkTraffic, downlinkTraffic string, err error) {
	cfg.Logger.Debug("Retrieving traffic stats")
	var uplink, downlink uint64
	err = manager.ExecuteLowPriority(func(db *sql.DB) error {
		err := db.QueryRow("SELECT uplink, downlink FROM traffic_stats WHERE source = 'direct'").Scan(&uplink, &downlink)
		if err != nil {
			if err == sql.ErrNoRows {
				cfg.Logger.Warn("Traffic data not found, setting default values", "source", "direct")
				uplink, downlink = 0, 0
				return nil
			}
			cfg.Logger.Error("Failed to query traffic_stats table", "error", err)
			return fmt.Errorf("failed to query database: %v", err)
		}
		cfg.Logger.Trace("Traffic data retrieved", "uplink", uplink, "downlink", downlink)
		return nil
	})
	if err != nil {
		cfg.Logger.Error("Failed to retrieve traffic stats", "error", err)
		uplink, downlink = 0, 0
	}

	totalTraffic = util.FormatData(float64(uplink+downlink), "byte")
	uplinkTraffic = util.FormatData(float64(uplink), "byte")
	downlinkTraffic = util.FormatData(float64(downlink), "byte")
	cfg.Logger.Debug("Traffic stats formatted", "total", totalTraffic, "uplink", uplinkTraffic, "downlink", downlinkTraffic)
	return totalTraffic, uplinkTraffic, downlinkTraffic, nil
}

// isServiceRunning checks if a service is running by checking /proc.
func isServiceRunning(svc string, cfg *config.Config) bool {
	cfg.Logger.Debug("Checking service status", "service", svc)
	procDir, err := os.Open("/proc")
	if err != nil {
		cfg.Logger.Error("Failed to open /proc for service", "service", svc, "error", err)
		return false
	}
	defer procDir.Close()

	entries, err := procDir.Readdirnames(-1)
	if err != nil {
		cfg.Logger.Error("Failed to read /proc for service", "service", svc, "error", err)
		return false
	}

	for _, entry := range entries {
		if _, err := strconv.Atoi(entry); err != nil {
			cfg.Logger.Trace("Skipping non-numeric entry", "entry", entry)
			continue
		}
		commPath := filepath.Join("/proc", entry, "comm")
		commData, err := os.ReadFile(commPath)
		if err != nil {
			cfg.Logger.Error("Failed to read comm file for service", "path", commPath, "service", svc, "error", err)
			continue
		}
		if strings.TrimSpace(string(commData)) == svc {
			cfg.Logger.Trace("Service is running", "service", svc)
			return true
		}
	}
	cfg.Logger.Info("Service is not running", "service", svc)
	return false
}

// CheckServiceStatus checks service statuses and sends notifications if changed.
func CheckServiceStatus(cfg *config.Config) {
	cfg.Logger.Debug("Checking service statuses")
	statusMutex.Lock()
	defer statusMutex.Unlock()

	var changed []string
	var statusLines []string

	for _, svc := range cfg.Services {
		running := isServiceRunning(svc, cfg)
		prev, seen := serviceStatuses[svc]

		if !isFirstCheck && seen && prev != running {
			cfg.Logger.Info("Service status changed", "service", svc, "running", running)
			changed = append(changed, svc)
		}

		serviceStatuses[svc] = running
		state := "▼"
		if running {
			state = "▲"
		}
		statusLines = append(statusLines, fmt.Sprintf("%s %s", state, svc))
	}

	if !isFirstCheck && len(changed) > 0 {
		message := fmt.Sprintf("⚠️ Service Status Update:\n%s", strings.Join(statusLines, "\n"))
		if err := telegram.SendNotification(cfg, message); err != nil {
			cfg.Logger.Error("Failed to send service status notification", "error", err)
		} else {
			cfg.Logger.Info("Service status notification sent successfully")
		}
	}

	if isFirstCheck {
		cfg.Logger.Debug("First service status check completed")
		isFirstCheck = false
	}
}

// CheckMemoryUsage checks memory usage and sends notifications if thresholds are exceeded.
func CheckMemoryUsage(cfg *config.Config) {
	cfg.Logger.Debug("Checking memory usage")
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		cfg.Logger.Error("Failed to read /proc/meminfo", "error", err)
		return
	}

	var memTotal, memAvailable uint64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			cfg.Logger.Trace("Skipping empty or invalid line in /proc/meminfo")
			continue
		}
		if fields[0] == "MemTotal:" {
			memTotal, _ = strconv.ParseUint(fields[1], 10, 64)
			cfg.Logger.Trace("Parsed MemTotal", "value", memTotal)
		}
		if fields[0] == "MemAvailable:" {
			memAvailable, _ = strconv.ParseUint(fields[1], 10, 64)
			cfg.Logger.Trace("Parsed MemAvailable", "value", memAvailable)
		}
	}

	if memTotal == 0 {
		cfg.Logger.Error("Invalid memory data: MemTotal is zero")
		return
	}

	usedMem := memTotal - memAvailable
	percentage := float64(usedMem) / float64(memTotal) * 100

	memoryMutex.Lock()
	defer memoryMutex.Unlock()

	const tickInterval = 10
	measurements := max((cfg.SystemMonitoring.AverageInterval+tickInterval-1)/tickInterval, 1)
	memoryPercentages = append(memoryPercentages, percentage)
	if len(memoryPercentages) > measurements {
		memoryPercentages = memoryPercentages[1:]
	}

	if len(memoryPercentages) == measurements {
		var sum float64
		for _, p := range memoryPercentages {
			sum += p
		}
		average := sum / float64(len(memoryPercentages))
		cfg.Logger.Debug("Calculated average memory usage", "average", average)

		if average > float64(cfg.SystemMonitoring.Memory.Threshold) && !memoryExceeded {
			message := fmt.Sprintf("🚨 ALERT: Average memory usage over *%d* seconds exceeded *%d%%*! (Current: *%.2f%%*)", cfg.SystemMonitoring.AverageInterval, cfg.SystemMonitoring.Memory.Threshold, average)
			if err := telegram.SendNotification(cfg, message); err != nil {
				cfg.Logger.Error("Failed to send memory usage notification", "error", err)
			} else {
				cfg.Logger.Info("Memory usage notification sent successfully")
				memoryExceeded = true
			}
		} else if average <= float64(cfg.SystemMonitoring.Memory.Threshold) && memoryExceeded {
			message := fmt.Sprintf("✅ Average memory usage over *%d* seconds dropped below *%d%%*. (Current: *%.2f%%*)", cfg.SystemMonitoring.AverageInterval, cfg.SystemMonitoring.Memory.Threshold, average)
			if err := telegram.SendNotification(cfg, message); err != nil {
				cfg.Logger.Error("Failed to send memory usage notification", "error", err)
			} else {
				cfg.Logger.Info("Memory usage notification sent successfully")
				memoryExceeded = false
			}
		}
	}
}

// GetDiskUsage returns disk usage information without sending notifications.
func GetDiskUsage(cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving disk usage")
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		cfg.Logger.Error("Failed to get disk usage", "error", err)
		return "unknown"
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := total - free

	if total == 0 {
		cfg.Logger.Error("Invalid disk data: total size is zero")
		return "unknown"
	}

	cfg.Logger.Info("Disk usage retrieved", "used_gb", float64(used)/(1024*1024*1024), "total_gb", float64(total)/(1024*1024*1024))
	return fmt.Sprintf("%.2f GB used / %.2f GB total", float64(used)/(1024*1024*1024), float64(total)/(1024*1024*1024))
}

// CheckDiskUsage checks disk usage and sends notifications if thresholds are exceeded.
func CheckDiskUsage(cfg *config.Config) {
	cfg.Logger.Debug("Checking disk usage")
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		cfg.Logger.Error("Failed to get disk usage", "error", err)
		return
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := total - free

	if total == 0 {
		cfg.Logger.Error("Invalid disk data: total size is zero")
		return
	}

	percentage := float64(used) / float64(total) * 100

	diskMutex.Lock()
	defer diskMutex.Unlock()

	const tickInterval = 10
	measurements := max((cfg.SystemMonitoring.AverageInterval+tickInterval-1)/tickInterval, 1)
	diskPercentages = append(diskPercentages, percentage)
	if len(diskPercentages) > measurements {
		diskPercentages = diskPercentages[1:]
	}

	if len(diskPercentages) == measurements {
		var sum float64
		for _, p := range diskPercentages {
			sum += p
		}
		average := sum / float64(len(diskPercentages))
		cfg.Logger.Debug("Calculated average disk usage", "average", average)

		if average > float64(cfg.SystemMonitoring.Disk.Threshold) && !diskExceeded {
			message := fmt.Sprintf("🚨 ALERT: Average disk usage over *%d* seconds exceeded *%d%%*! (Current: *%.2f%%*)", cfg.SystemMonitoring.AverageInterval, cfg.SystemMonitoring.Disk.Threshold, average)
			if err := telegram.SendNotification(cfg, message); err != nil {
				cfg.Logger.Error("Failed to send disk usage notification", "error", err)
			} else {
				cfg.Logger.Info("Disk usage notification sent successfully")
				diskExceeded = true
			}
		} else if average <= float64(cfg.SystemMonitoring.Disk.Threshold) && diskExceeded {
			message := fmt.Sprintf("✅ Average disk usage over *%d* seconds dropped below *%d%%*. (Current: *%.2f%%*)", cfg.SystemMonitoring.AverageInterval, cfg.SystemMonitoring.Disk.Threshold, average)
			if err := telegram.SendNotification(cfg, message); err != nil {
				cfg.Logger.Error("Failed to send disk usage notification", "error", err)
			} else {
				cfg.Logger.Info("Disk usage notification sent successfully")
				diskExceeded = false
			}
		}
	}
}

// GetStatus returns the status of specified services without sending notifications.
func GetStatus(cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving service statuses")
	var status strings.Builder

	statusMutex.Lock()
	defer statusMutex.Unlock()

	for _, svc := range cfg.Services {
		isRunning := isServiceRunning(svc, cfg)
		serviceStatuses[svc] = isRunning

		state := "▼"
		if isRunning {
			state = "▲"
		}
		fmt.Fprintf(&status, "%s %s ", state, svc)
	}

	result := strings.TrimSpace(status.String())
	cfg.Logger.Trace("Service statuses retrieved", "status", result)
	return result
}

// MonitorStats runs periodic checks for service, disk, and memory usage.
func MonitorStats(ctx context.Context, cfg *config.Config, wg *sync.WaitGroup) {
	cfg.Logger.Debug("Starting stats monitoring")
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cfg.Logger.Debug("Running periodic stats check")
				CheckServiceStatus(cfg)
				CheckDiskUsage(cfg)
				CheckMemoryUsage(cfg)
			case <-ctx.Done():
				cfg.Logger.Debug("Stopped stats monitoring")
				return
			}
		}
	}()
}
