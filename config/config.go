package config

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the configuration settings for the application.
type Config struct {
	V2rayStat        V2rayStatConfig        `yaml:"v2ray-stat"`
	Core             CoreConfig             `yaml:"core"`
	API              APIConfig              `yaml:"api"`
	Timezone         string                 `yaml:"timezone"`
	Features         map[string]bool        `yaml:"features"`
	Services         []string               `yaml:"services"`
	Telegram         TelegramConfig         `yaml:"telegram"`
	SystemMonitoring SystemMonitoringConfig `yaml:"system_monitoring"`
	Paths            PathsConfig            `yaml:"paths"`
	IpTtl            time.Duration          `yaml:"-"`
	StatsColumns     StatsColumns           `yaml:"stats_columns"`
}

// V2rayStatConfig holds v2ray-stat specific settings.
type V2rayStatConfig struct {
	Type    string        `yaml:"type"`
	Address string        `yaml:"address"`
	Port    string        `yaml:"port"`
	Monitor MonitorConfig `yaml:"monitor"`
}

// CoreConfig holds core-related settings.
type CoreConfig struct {
	Dir            string `yaml:"dir"`
	Config         string `yaml:"config"`
	AccessLog      string `yaml:"access_log"`
	AccessLogRegex string `yaml:"access_log_regex"`
}

// MonitorConfig holds monitoring-related settings.
type MonitorConfig struct {
	TickerInterval      int `yaml:"ticker_interval"`
	OnlineRateThreshold int `yaml:"online_rate_threshold"`
}

// APIConfig holds API-related settings.
type APIConfig struct {
	APIToken string `yaml:"api_token"`
}

// TelegramConfig holds Telegram notification settings.
type TelegramConfig struct {
	ChatID   string `yaml:"chat_id"`
	BotToken string `yaml:"bot_token"`
}

// SystemMonitoringConfig holds system monitoring settings.
type SystemMonitoringConfig struct {
	AverageInterval int          `yaml:"average_interval"`
	Memory          MemoryConfig `yaml:"memory"`
	Disk            DiskConfig   `yaml:"disk"`
}

// MemoryConfig holds memory monitoring settings.
type MemoryConfig struct {
	Threshold int `yaml:"threshold"`
}

// DiskConfig holds disk monitoring settings.
type DiskConfig struct {
	Threshold int `yaml:"threshold"`
}

// PathsConfig holds paths and logging settings.
type PathsConfig struct {
	Database     string `yaml:"database"`
	F2BLog       string `yaml:"f2b_log"`
	F2BBannedLog string `yaml:"f2b_banned_log"`
	AuthLua      string `yaml:"auth_lua"`
}

// StatsColumns holds column configuration for stats display.
type StatsColumns struct {
	Server StatsSection `yaml:"server"`
	Client StatsSection `yaml:"client"`
}

// StatsSection holds columns and sort configuration for a section.
type StatsSection struct {
	Sort      string   `yaml:"sort"`
	SortBy    string   // Parsed column name for sorting
	SortOrder string   // Parsed sort order (ASC or DESC)
	Columns   []string `yaml:"columns"`
}

var defaultConfig = Config{
	Core: CoreConfig{
		Dir:            "/usr/local/etc/xray/",
		Config:         "/usr/local/etc/xray/config.json",
		AccessLog:      "/usr/local/etc/xray/access.log",
		AccessLogRegex: `from tcp:([0-9\.]+).*?tcp:([\w\.\-]+):\d+.*?email: (\S+)`,
	},
	V2rayStat: V2rayStatConfig{
		Type: "xray",
		Port: "9952",
		Monitor: MonitorConfig{
			TickerInterval:      10,
			OnlineRateThreshold: 0,
		},
	},
	API: APIConfig{
		APIToken: "",
	},
	Timezone: "",
	Features: make(map[string]bool),
	Services: []string{"xray", "fail2ban-server"},
	Telegram: TelegramConfig{
		ChatID:   "",
		BotToken: "",
	},
	SystemMonitoring: SystemMonitoringConfig{
		AverageInterval: 120,
		Memory: MemoryConfig{
			Threshold: 0,
		},
		Disk: DiskConfig{
			Threshold: 0,
		},
	},
	Paths: PathsConfig{
		Database:     "/usr/local/etc/v2ray-stat/data.db",
		F2BLog:       "/var/log/v2ray-stat.log ",
		F2BBannedLog: "/var/log/v2ray-stat-banned.log",
		AuthLua:      "/etc/haproxy/.auth.lua",
	},
	StatsColumns: StatsColumns{
		Server: StatsSection{Sort: "source ASC", Columns: []string{}},
		Client: StatsSection{Sort: "user ASC", Columns: []string{}},
	},
}

// LoadConfig reads configuration from the specified YAML file and returns a Config struct.
func LoadConfig(configFile string) (Config, error) {
	cfg := defaultConfig

	// Read YAML file
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Configuration file %s not found, using default values", configFile)
			return cfg, nil
		}
		return cfg, fmt.Errorf("error reading configuration file: %v", err)
	}

	// Unmarshal YAML into Config struct
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("error parsing YAML configuration: %v", err)
	}

	// Validate and adjust configuration
	if cfg.V2rayStat.Type != "xray" && cfg.V2rayStat.Type != "singbox" {
		log.Printf("Invalid v2ray-stat.type '%s', using default: %s", cfg.V2rayStat.Type, defaultConfig.V2rayStat.Type)
		cfg.V2rayStat.Type = defaultConfig.V2rayStat.Type
	}

	if cfg.V2rayStat.Port != "" {
		portNum, err := strconv.Atoi(cfg.V2rayStat.Port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return cfg, fmt.Errorf("invalid v2ray-stat.port: %s", cfg.V2rayStat.Port)
		}
	}

	if cfg.Core.AccessLogRegex != "" {
		if _, err := regexp.Compile(cfg.Core.AccessLogRegex); err != nil {
			log.Printf("Invalid core.access_log_regex '%s', using default: %s", cfg.Core.AccessLogRegex, defaultConfig.Core.AccessLogRegex)
			cfg.Core.AccessLogRegex = defaultConfig.Core.AccessLogRegex
		}
	}

	if cfg.SystemMonitoring.AverageInterval < 10 {
		log.Printf("Invalid system_monitoring.average_interval value %d, using default: %d", cfg.SystemMonitoring.AverageInterval, defaultConfig.SystemMonitoring.AverageInterval)
		cfg.SystemMonitoring.AverageInterval = defaultConfig.SystemMonitoring.AverageInterval
	}

	if cfg.SystemMonitoring.Memory.Threshold < 0 || cfg.SystemMonitoring.Memory.Threshold > 100 {
		log.Printf("Invalid system_monitoring.memory.threshold value %d, using default: %d", cfg.SystemMonitoring.Memory.Threshold, defaultConfig.SystemMonitoring.Memory.Threshold)
		cfg.SystemMonitoring.Memory.Threshold = defaultConfig.SystemMonitoring.Memory.Threshold
	}

	if cfg.SystemMonitoring.Disk.Threshold < 0 || cfg.SystemMonitoring.Disk.Threshold > 100 {
		log.Printf("Invalid system_monitoring.disk.threshold value %d, using default: %d", cfg.SystemMonitoring.Disk.Threshold, defaultConfig.SystemMonitoring.Disk.Threshold)
		cfg.SystemMonitoring.Disk.Threshold = defaultConfig.SystemMonitoring.Disk.Threshold
	}

	if cfg.V2rayStat.Monitor.TickerInterval < 1 {
		log.Printf("Invalid v2ray-stat.monitor.ticker_interval value %d, using default: %d", cfg.V2rayStat.Monitor.TickerInterval, defaultConfig.V2rayStat.Monitor.TickerInterval)
		cfg.V2rayStat.Monitor.TickerInterval = defaultConfig.V2rayStat.Monitor.TickerInterval
	}

	if cfg.V2rayStat.Monitor.OnlineRateThreshold < 0 {
		log.Printf("Invalid v2ray-stat.monitor.online_rate_threshold value %d, using default: %d", cfg.V2rayStat.Monitor.OnlineRateThreshold, defaultConfig.V2rayStat.Monitor.OnlineRateThreshold)
		cfg.V2rayStat.Monitor.OnlineRateThreshold = defaultConfig.V2rayStat.Monitor.OnlineRateThreshold
	}

	if cfg.Timezone != "" {
		if _, err := time.LoadLocation(cfg.Timezone); err != nil {
			log.Printf("Invalid timezone value '%s', using default (empty)", cfg.Timezone)
			cfg.Timezone = defaultConfig.Timezone
		}
	}

	// Ensure Features map is initialized
	if cfg.Features == nil {
		cfg.Features = make(map[string]bool)
	}

	if cfg.StatsColumns.Server.Columns == nil {
		cfg.StatsColumns.Server.Columns = []string{}
	}
	if cfg.StatsColumns.Client.Columns == nil {
		cfg.StatsColumns.Client.Columns = []string{}
	}

	// Validate columns
	validServerColumns := []string{"source", "rate", "uplink", "downlink", "sess_uplink", "sess_downlink"}
	validClientColumns := []string{"user", "uuid", "last_seen", "rate", "uplink", "downlink", "sess_uplink", "sess_downlink", "enabled", "sub_end", "renew", "lim_ip", "ips", "created"}

	var filteredServer []string
	for _, col := range cfg.StatsColumns.Server.Columns {
		if contains(validServerColumns, col) {
			filteredServer = append(filteredServer, col)
		} else {
			log.Printf("Invalid custom server column `%s`, ignoring", col)
		}
	}
	cfg.StatsColumns.Server.Columns = filteredServer

	var filteredClient []string
	for _, col := range cfg.StatsColumns.Client.Columns {
		if contains(validClientColumns, col) {
			filteredClient = append(filteredClient, col)
		} else {
			log.Printf("Invalid custom client column `%s`, ignoring", col)
		}
	}
	cfg.StatsColumns.Client.Columns = filteredClient

	// Validate sort configuration
	validateSort := func(section string, sortStr string, validColumns []string) (string, string) {
		if sortStr == "" {
			if section == "Server" {
				return "source", "ASC"
			}
			return "user", "ASC"
		}
		parts := strings.Fields(sortStr)
		if len(parts) != 2 {
			log.Printf("Invalid sort format for %s: '%s', using default", section, sortStr)
			if section == "Server" {
				return "source", "ASC"
			}
			return "user", "ASC"
		}
		column, order := parts[0], strings.ToUpper(parts[1])
		if !contains(validColumns, column) {
			log.Printf("Invalid sort column for %s: '%s', using default", section, column)
			if section == "Server" {
				return "source", "ASC"
			}
			return "user", "ASC"
		}
		if order != "ASC" && order != "DESC" {
			log.Printf("Invalid sort order for %s: '%s', using ASC", section, order)
			order = "ASC"
		}
		return column, order
	}

	cfg.StatsColumns.Server.SortBy, cfg.StatsColumns.Server.SortOrder = validateSort("Server", cfg.StatsColumns.Server.Sort, validServerColumns)
	cfg.StatsColumns.Client.SortBy, cfg.StatsColumns.Client.SortOrder = validateSort("Client", cfg.StatsColumns.Client.Sort, validClientColumns)

	return cfg, nil
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}
