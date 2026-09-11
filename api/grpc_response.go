package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"exodus-node/config"
	"exodus-node/constant"
	"exodus-node/sdk"

	"golang.org/x/sys/unix"
)

// Stat represents a single statistic entry.
type Stat struct {
	Name  string
	Value string
}

// ApiResponse contains the collected statistics.
type ApiResponse struct {
	Stat []Stat
}

// Service is a thin API facade over core SDK for node gRPC handlers.
type Service struct {
	cfg    *config.NodeConfig
	logger *config.Logger
	api    *sdk.API

	apiAddress string
	apiPort    int

	coreStateMu   sync.RWMutex
	coreOnline    bool
	lifecycleErr  string // root-cause error from last managed start attempt (SPAWN_ERROR etc.)
	lastLoggedErr string // dedup: avoid repeating the same WARN every 15s stats tick

	singboxVersion string
	nodeVersion    string
	cpuCount       int
	cpuModel       string
	totalRAMBytes  uint64

	networkMu            sync.Mutex
	defaultInterface     string
	previousNetworkStats map[string]networkCounter
}

type systemInfo struct {
	Arch              string   `json:"arch"`
	CPUs              int      `json:"cpus"`
	CPUModel          string   `json:"cpuModel"`
	MemoryTotal       uint64   `json:"memoryTotal"`
	Hostname          string   `json:"hostname"`
	Platform          string   `json:"platform"`
	Release           string   `json:"release"`
	Type              string   `json:"type"`
	Version           string   `json:"version"`
	NetworkInterfaces []string `json:"networkInterfaces"`
}

type systemInterfaceStats struct {
	Interface     string  `json:"interface"`
	RXBytesPerSec float64 `json:"rxBytesPerSec"`
	TXBytesPerSec float64 `json:"txBytesPerSec"`
	RXTotal       uint64  `json:"rxTotal"`
	TXTotal       uint64  `json:"txTotal"`
}

type systemStats struct {
	MemoryFree uint64                `json:"memoryFree"`
	MemoryUsed uint64                `json:"memoryUsed"`
	Uptime     float64               `json:"uptime"`
	LoadAvg    []float64             `json:"loadAvg"`
	Interface  *systemInterfaceStats `json:"interface"`
}

type networkCounter struct {
	rxBytes   uint64
	txBytes   uint64
	timestamp time.Time
}

func NewService(cfg *config.NodeConfig) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil node config")
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("nil node logger")
	}

	log := cfg.LoggerFor("StatsService")
	log.Debug("Initializing core API facade", "address", config.FixedCoreAPIAddress, "port", cfg.CoreAPIGRPCPort)
	coreAPI, err := sdk.New(sdk.Config{
		CoreType: config.FixedCoreType,
		Address:  config.FixedCoreAPIAddress,
		Port:     cfg.CoreAPIGRPCPort,
	})
	if err != nil {
		// The node process is the control plane and must remain available even when
		// the managed core is down. Invalid static SDK configuration is logged and
		// surfaced through degraded stats instead of killing the node at startup.
		log.Error("Core SDK initialization failed; continuing without fatal exit", "error", err)
	} else {
		cfg.LoggerFor("SingboxAPI").Info("[OK] Sing-box API initialized")
	}

	return &Service{
		cfg:                  cfg,
		logger:               log,
		api:                  coreAPI,
		apiAddress:           config.FixedCoreAPIAddress,
		apiPort:              cfg.CoreAPIGRPCPort,
		singboxVersion:       detectSingboxVersion(),
		nodeVersion:          constant.Version,
		cpuCount:             runtime.NumCPU(),
		cpuModel:             detectCPUModel(),
		totalRAMBytes:        detectTotalRAMBytes(),
		defaultInterface:     detectDefaultInterface(),
		previousNetworkStats: readNetworkCounters(),
	}, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.coreStateMu.Lock()
	api := s.api
	s.api = nil
	s.coreStateMu.Unlock()

	if api == nil {
		return nil
	}
	return api.Close()
}

func (s *Service) IsCoreOnline() bool {
	if s == nil {
		return false
	}
	s.coreStateMu.RLock()
	defer s.coreStateMu.RUnlock()
	return s.coreOnline
}

func (s *Service) MarkCoreOnline() {
	if s == nil {
		return
	}
	s.coreStateMu.Lock()
	s.coreOnline = true
	s.lifecycleErr = ""  // core recovered — forget the old start error
	s.lastLoggedErr = "" // reset log dedup
	s.coreStateMu.Unlock()
}

func (s *Service) MarkCoreOffline() {
	if s == nil {
		return
	}
	s.coreStateMu.Lock()
	s.coreOnline = false
	s.coreStateMu.Unlock()
}

// SetCoreError stores the root-cause error from a managed start/restart attempt
// (e.g. "start singbox: supervisor XML-RPC fault: SPAWN_ERROR: singbox").
// GetApiResponse prefers this over the transient "connection refused" errors
// produced on every stats tick while the core is down, so the panel always
// sees the original failure reason in core_error / last_status_message.
// Cleared automatically by MarkCoreOnline when the core recovers.
func (s *Service) SetCoreError(msg string) {
	if s == nil {
		return
	}
	s.coreStateMu.Lock()
	s.lifecycleErr = strings.TrimSpace(msg)
	s.coreStateMu.Unlock()
}

// logCoreStatsFailure logs a stats collection error. The first occurrence is
// always logged at WARN; subsequent identical errors are demoted to DEBUG to
// avoid flooding the log with "connection refused" every 15s stats tick while
// the core is stopped. The dedup counter resets when the core recovers
// (MarkCoreOnline clears lastLoggedErr).
func (s *Service) logCoreStatsFailure(message string, err error) {
	if s == nil || s.logger == nil {
		return
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	s.coreStateMu.Lock()
	firstOccurrence := s.lastLoggedErr != errStr
	if firstOccurrence {
		s.lastLoggedErr = errStr
	}
	s.coreStateMu.Unlock()

	if firstOccurrence {
		s.logger.Warn(message, "error", err)
	} else {
		s.logger.Debug(message, "error", err)
	}
}

func (s *Service) GetApiResponse(ctx context.Context) (*ApiResponse, error) {
	result := &ApiResponse{Stat: make([]Stat, 0, 32)}
	if s == nil {
		return result, fmt.Errorf("api service is nil")
	}

	coreStatus := "running"
	coreError := ""
	singboxUptimeSeconds := int64(0)

	if !s.IsCoreOnline() {
		s.logger.Debug("Core is not marked online; returning degraded stats", "status", "stopped")
		coreStatus = "stopped"
		s.coreStateMu.RLock()
		coreError = s.lifecycleErr
		s.coreStateMu.RUnlock()
	} else {
		api := s.getAPI()
		if api == nil || api.Stats == nil {
			coreStatus = "error"
			coreError = "core SDK is not initialized"
			s.logger.Warn("Core stats are unavailable", "error", coreError)
		} else {
			stats, err := api.Stats.QueryStats(ctx, sdk.QueryOptions{
				Patterns: []string{
					`^inbound>>>.*>>>traffic>>>(?:uplink|downlink)$`,
					`^outbound>>>.*>>>traffic>>>(?:uplink|downlink)$`,
					`^user>>>.*>>>traffic>>>(?:uplink|downlink)$`,
				},
				Regexp: true,
				Reset:  true,
			})
			if err != nil {
				coreStatus = "error"
				// Prefer the stored lifecycle error (e.g. "SPAWN_ERROR: singbox")
				// over the transient "connection refused" produced on every stats
				// tick while the core is down. The stored error is the root cause;
				// "connection refused" is just the symptom of it.
				s.coreStateMu.RLock()
				stored := s.lifecycleErr
				s.coreStateMu.RUnlock()
				if stored != "" {
					coreError = stored
				} else {
					coreError = fmt.Sprintf("query core stats: %v", err)
				}
				s.logCoreStatsFailure("Core stats query failed; returning degraded stats", err)
			} else {
				for _, item := range stats {
					result.Stat = append(result.Stat, Stat{
						Name:  item.Name,
						Value: strconv.FormatInt(item.Value, 10),
					})
				}
				if s.logger.Enabled(config.LogLevelTrace) {
					userBytes := make(map[string]int64)
					for _, item := range stats {
						if item.Value > 0 && strings.HasPrefix(item.Name, "user>>>") {
							rest := item.Name[len("user>>>"):]
							if idx := strings.Index(rest, ">>>traffic>>>"); idx >= 0 {
								username := rest[:idx]
								userBytes[username] += item.Value
							}
						}
					}
					for username, totalBytes := range userBytes {
						s.logger.Trace("Recorded user traffic delta", "user", username, "bytes", totalBytes)
					}
				}
			}

			if coreStatus == "running" {
				sysStats, sysErr := api.Stats.GetSysStats(ctx)
				if sysErr != nil {
					coreStatus = "error"
					s.coreStateMu.RLock()
					stored := s.lifecycleErr
					s.coreStateMu.RUnlock()
					if stored != "" {
						coreError = stored
					} else {
						coreError = fmt.Sprintf("query core sys stats: %v", sysErr)
					}
					s.logCoreStatsFailure("Core sys stats query failed; returning degraded stats", sysErr)
				} else if sysStats != nil {
					singboxUptimeSeconds = int64(sysStats.Uptime)
					s.MarkCoreOnline()
				}
			}
		}
	}

	// Runtime metadata consumed by backend node monitor. core_status/core_error are
	// transient runtime values; the panel maps them to is_connected/last_status_message
	// without adding dedicated Prisma columns.
	result.Stat = append(result.Stat,
		Stat{Name: "core_status", Value: coreStatus},
		Stat{Name: "core_error", Value: coreError},
		Stat{Name: "singbox_version", Value: s.singboxVersion},
		Stat{Name: "node_version", Value: s.nodeVersion},
		Stat{Name: "singbox_uptime", Value: strconv.FormatInt(singboxUptimeSeconds, 10)},
		Stat{Name: "cpu_count", Value: strconv.Itoa(s.cpuCount)},
		Stat{Name: "cpu_model", Value: s.cpuModel},
		Stat{Name: "total_ram", Value: formatIECBytes(s.totalRAMBytes)},
	)
	if infoJSON, statsJSON, err := s.collectSystemStatsJSON(); err != nil {
		s.logger.Warn("Failed to collect system stats", "error", err)
	} else {
		result.Stat = append(result.Stat,
			Stat{Name: "system_info", Value: infoJSON},
			Stat{Name: "system_stats", Value: statsJSON},
		)
	}

	return result, nil
}

func (s *Service) CheckCoreReady(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("api service is nil")
	}
	api := s.getAPI()
	if api == nil || api.Stats == nil {
		return fmt.Errorf("core SDK is not initialized")
	}
	_, err := api.Stats.GetSysStats(ctx)
	if err != nil {
		return fmt.Errorf("query core sys stats: %w", err)
	}
	return nil
}

func (s *Service) collectSystemStatsJSON() (string, string, error) {
	info := systemInfo{
		Arch:              runtime.GOARCH,
		CPUs:              s.cpuCount,
		CPUModel:          s.cpuModel,
		MemoryTotal:       s.totalRAMBytes,
		Hostname:          detectHostname(),
		Platform:          runtime.GOOS,
		Release:           detectKernelRelease(),
		Type:              detectOSType(),
		Version:           detectOSVersion(),
		NetworkInterfaces: detectNetworkInterfaces(),
	}

	memoryFree := detectAvailableRAMBytes()
	stats := systemStats{
		MemoryFree: memoryFree,
		MemoryUsed: saturatingSub(s.totalRAMBytes, memoryFree),
		Uptime:     detectSystemUptime(),
		LoadAvg:    detectLoadAverage(),
		Interface:  s.collectInterfaceStats(),
	}

	infoBytes, err := json.Marshal(info)
	if err != nil {
		return "", "", err
	}
	statsBytes, err := json.Marshal(stats)
	if err != nil {
		return "", "", err
	}
	return string(infoBytes), string(statsBytes), nil
}

func detectSingboxVersion() string {
	out, err := exec.Command("/usr/local/bin/sing-box", "version").Output()
	if err != nil {
		return "unknown"
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	// Example: "sing-box version 1.13.x"
	if idx := strings.LastIndex(line, " "); idx > 0 && idx+1 < len(line) {
		return strings.TrimSpace(line[idx+1:])
	}
	return line
}

func detectCPUModel() string {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

func detectTotalRAMBytes() uint64 {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				break
			}
			kib, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr != nil {
				break
			}
			return kib * 1024
		}
	}
	return 0
}

func detectAvailableRAMBytes() uint64 {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var available, free uint64
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kib, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					available = kib * 1024
				}
			}
		}
		if strings.HasPrefix(line, "MemFree:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kib, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					free = kib * 1024
				}
			}
		}
	}
	if available == 0 {
		return free
	}
	return available
}

var (
	cachedHostname = sync.OnceValue(func() string {
		hostname, err := os.Hostname()
		if err != nil || strings.TrimSpace(hostname) == "" {
			return "unknown"
		}
		return hostname
	})

	cachedKernelRelease = sync.OnceValue(func() string {
		var uts unix.Utsname
		if err := unix.Uname(&uts); err == nil {
			buf := make([]byte, 0, 65)
			for _, c := range uts.Release {
				if c == 0 {
					break
				}
				buf = append(buf, byte(c))
			}
			if len(buf) > 0 {
				return string(buf)
			}
		}
		out, err := exec.Command("uname", "-r").Output()
		if err == nil {
			if release := strings.TrimSpace(string(out)); release != "" {
				return release
			}
		}
		return "unknown"
	})

	cachedOSVersion = sync.OnceValue(func() string {
		if content, err := os.ReadFile("/proc/version"); err == nil {
			if version := strings.TrimSpace(string(content)); version != "" {
				return version
			}
		}
		return cachedKernelRelease()
	})
)

func detectHostname() string {
	return cachedHostname()
}

func detectKernelRelease() string {
	return cachedKernelRelease()
}

func detectOSType() string {
	if runtime.GOOS == "linux" {
		return "Linux"
	}
	return runtime.GOOS
}

func detectOSVersion() string {
	return cachedOSVersion()
}

func detectSystemUptime() float64 {
	content, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return uptime
}

func detectLoadAverage() []float64 {
	content, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return []float64{0, 0, 0}
	}
	fields := strings.Fields(string(content))
	load := []float64{0, 0, 0}
	for i := 0; i < len(load) && i < len(fields); i++ {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err == nil {
			load[i] = value
		}
	}
	return load
}

func detectNetworkInterfaces() []string {
	seen := make(map[string]struct{})
	interfaces := make([]string, 0)

	appendInterface := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		interfaces = append(interfaces, name)
	}

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			appendInterface(iface.Name)
		}
	}

	for _, iface := range readNetworkInterfaceOrder() {
		appendInterface(iface)
	}

	return interfaces
}

func readNetworkInterfaceOrder() []string {
	content, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return nil
	}
	interfaces := make([]string, 0)
	for _, line := range strings.Split(string(content), "\n")[2:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface != "" {
			interfaces = append(interfaces, iface)
		}
	}
	return interfaces
}

func detectDefaultInterface() string {
	content, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "00000000" {
			return strings.TrimSpace(fields[0])
		}
	}
	return ""
}

func readNetworkCounters() map[string]networkCounter {
	content, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return map[string]networkCounter{}
	}
	now := time.Now()
	counters := make(map[string]networkCounter)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines[2:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 10 {
			continue
		}
		iface := strings.TrimSuffix(parts[0], ":")
		rxBytes, rxErr := strconv.ParseUint(parts[1], 10, 64)
		txBytes, txErr := strconv.ParseUint(parts[9], 10, 64)
		if iface == "" || rxErr != nil || txErr != nil {
			continue
		}
		counters[iface] = networkCounter{
			rxBytes:   rxBytes,
			txBytes:   txBytes,
			timestamp: now,
		}
	}
	return counters
}

func (s *Service) collectInterfaceStats() *systemInterfaceStats {
	current := readNetworkCounters()
	if len(current) == 0 {
		return nil
	}

	s.networkMu.Lock()
	defer s.networkMu.Unlock()

	if s.defaultInterface == "" {
		s.defaultInterface = detectDefaultInterface()
	}
	if s.defaultInterface == "" {
		for _, iface := range readNetworkInterfaceOrder() {
			if iface != "lo" {
				s.defaultInterface = iface
				break
			}
		}
	}
	if s.defaultInterface == "" {
		return nil
	}

	counter, ok := current[s.defaultInterface]
	if !ok {
		return nil
	}

	result := &systemInterfaceStats{
		Interface: s.defaultInterface,
		RXTotal:   counter.rxBytes,
		TXTotal:   counter.txBytes,
	}
	if previous, ok := s.previousNetworkStats[s.defaultInterface]; ok {
		elapsed := counter.timestamp.Sub(previous.timestamp).Seconds()
		if elapsed > 0 {
			result.RXBytesPerSec = float64(saturatingSub(counter.rxBytes, previous.rxBytes)) / elapsed
			result.TXBytesPerSec = float64(saturatingSub(counter.txBytes, previous.txBytes)) / elapsed
		}
	}
	s.previousNetworkStats = current

	return result
}

func saturatingSub(current uint64, previous uint64) uint64 {
	if current <= previous {
		return 0
	}
	return current - previous
}

func formatIECBytes(bytes uint64) string {
	if bytes == 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	size := float64(bytes)
	idx := 0
	for size >= 1024 && idx < len(units)-1 {
		size /= 1024
		idx++
	}
	if idx == 0 {
		return fmt.Sprintf("%.0f %s", size, units[idx])
	}
	value := math.Round(size*100) / 100
	return fmt.Sprintf("%.2f %s", value, units[idx])
}

func (s *Service) getAPI() *sdk.API {
	if s == nil {
		return nil
	}
	s.coreStateMu.RLock()
	defer s.coreStateMu.RUnlock()
	return s.api
}

func (s *Service) UpdateAPIClient(address string, port int) error {
	if s == nil {
		return nil
	}
	s.coreStateMu.RLock()
	currentAddress := s.apiAddress
	currentPort := s.apiPort
	s.coreStateMu.RUnlock()

	// If already initialized and configuration matches, skip recreation
	if currentAddress == address && currentPort == port {
		return nil
	}

	s.logger.Info("Updating core API client config", "address", address, "port", port)
	coreAPI, err := sdk.New(sdk.Config{
		CoreType: config.FixedCoreType,
		Address:  address,
		Port:     port,
	})
	if err != nil {
		return fmt.Errorf("failed to create core SDK: %w", err)
	}

	s.coreStateMu.Lock()
	oldAPI := s.api
	s.api = coreAPI
	s.apiAddress = address
	s.apiPort = port
	s.coreStateMu.Unlock()

	if oldAPI != nil {
		_ = oldAPI.Close()
	}
	return nil
}
