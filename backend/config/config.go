package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"v2ray-stat/logger"

	"gopkg.in/yaml.v3"
)

// Config holds the configuration settings for the backend.
type Config struct {
	Log          LogConfig       `yaml:"log"`
	V2rayStat    V2rayStatConfig `yaml:"v2ray-stat"`
	API          APIConfig       `yaml:"api"`
	Timezone     string          `yaml:"timezone"`
	Paths        PathsConfig     `yaml:"paths"`
	StatsColumns StatsColumns    `yaml:"stats_columns"`
	Logger       *logger.Logger
}

type LogConfig struct {
	LogLevel string `yaml:"loglevel"`
	LogMode  string `yaml:"logmode"`
}

// V2rayStatConfig holds v2ray-stat specific settings.
type V2rayStatConfig struct {
	Address string        `yaml:"address"`
	Port    string        `yaml:"port"`
	Monitor MonitorConfig `yaml:"monitor"`
	Nodes   []NodeConfig  `yaml:"nodes"`
}

// NodeConfig holds configuration for a single node.
type NodeConfig struct {
	Name       string      `yaml:"name"`
	URL        string      `yaml:"url"`
	MTLSConfig *MTLSConfig `yaml:"mtls"`
}

// MTLSConfig holds mTLS configuration for a node.
type MTLSConfig struct {
	Cert   string `yaml:"cert"`
	Key    string `yaml:"key"`
	CACert string `yaml:"ca_cert"`
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

// PathsConfig holds paths settings.
type PathsConfig struct {
	Database string `yaml:"database"`
}

// StatsColumns holds column configuration for stats display.
type StatsColumns struct {
	Server StatsSection `yaml:"server"`
	Client StatsSection `yaml:"client"`
}

// StatsSection holds columns and sort configuration for a section.
type StatsSection struct {
	Sort    string   `yaml:"sort"`
	Columns []string `yaml:"columns"`
}

var defaultConfig = Config{
	Log: LogConfig{
		LogLevel: "none",
		LogMode:  "inclusive",
	},
	V2rayStat: V2rayStatConfig{
		Address: "127.0.0.1",
		Port:    "9952",
		Monitor: MonitorConfig{
			TickerInterval:      10,
			OnlineRateThreshold: 0,
		},
		Nodes: []NodeConfig{},
	},
	API: APIConfig{
		APIToken: "",
	},
	Timezone: "",
	Paths: PathsConfig{
		Database: "/usr/local/etc/v2ray-stat/data.db",
	},
	StatsColumns: StatsColumns{
		Server: StatsSection{Sort: "source ASC", Columns: []string{}},
		Client: StatsSection{Sort: "last_seen DESC", Columns: []string{}},
	},
}

// LoadConfig reads configuration from the specified YAML file.
func LoadConfig(configFile string) (Config, error) {
	cfg := defaultConfig

	_, err := os.Stat(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Logger, _ = logger.NewLoggerWithValidation("warn", "inclusive", cfg.Timezone, os.Stderr)
			cfg.Logger.Warn("Configuration file not found, using default values", "file", configFile)
			return cfg, nil
		}
		return cfg, fmt.Errorf("error accessing configuration file %s: %v", configFile, err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return cfg, fmt.Errorf("error reading configuration file %s: %v", configFile, err)
	}
	cfg.Logger, _ = logger.NewLoggerWithValidation("debug", "inclusive", cfg.Timezone, os.Stderr)

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("error parsing YAML configuration from %s: %v", configFile, err)
	}

	cfg.Logger, err = logger.NewLoggerWithValidation(cfg.Log.LogLevel, cfg.Log.LogMode, cfg.Timezone, os.Stderr)
	if err != nil {
		return cfg, fmt.Errorf("failed to initialize logger: %v", err)
	}

	// Validate configuration
	if cfg.V2rayStat.Port != "" {
		portNum, err := strconv.Atoi(cfg.V2rayStat.Port)
		if err != nil || portNum < 1 || portNum > 65535 {
			cfg.Logger.Warn("Invalid v2ray-stat.port, using default", "port", cfg.V2rayStat.Port, "default", defaultConfig.V2rayStat.Port)
			cfg.V2rayStat.Port = defaultConfig.V2rayStat.Port
		}
	}

	if cfg.V2rayStat.Monitor.TickerInterval < 1 {
		cfg.Logger.Warn("Invalid v2ray-stat.monitor.ticker_interval, using default", "value", cfg.V2rayStat.Monitor.TickerInterval, "default", defaultConfig.V2rayStat.Monitor.TickerInterval)
		cfg.V2rayStat.Monitor.TickerInterval = defaultConfig.V2rayStat.Monitor.TickerInterval
	}

	if cfg.Timezone != "" {
		if _, err := time.LoadLocation(cfg.Timezone); err != nil {
			cfg.Logger.Warn("Invalid timezone value, using default", "timezone", cfg.Timezone)
			cfg.Timezone = defaultConfig.Timezone
		}
	}

	for i, node := range cfg.V2rayStat.Nodes {
		if node.URL == "" || node.Name == "" {
			cfg.Logger.Warn("Invalid node configuration, skipping", "index", i, "name", node.Name, "url", node.URL)
			cfg.V2rayStat.Nodes = append(cfg.V2rayStat.Nodes[:i], cfg.V2rayStat.Nodes[i+1:]...)
			continue
		}
		if node.MTLSConfig != nil {
			if node.MTLSConfig.Cert == "" || node.MTLSConfig.Key == "" || node.MTLSConfig.CACert == "" {
				cfg.Logger.Warn("Incomplete mTLS configuration for node, disabling mTLS", "name", node.Name)
				node.MTLSConfig = nil
			} else {
				for _, file := range []string{node.MTLSConfig.Cert, node.MTLSConfig.Key, node.MTLSConfig.CACert} {
					if _, err := os.Stat(file); os.IsNotExist(err) {
						cfg.Logger.Warn("mTLS certificate file not found for node, disabling mTLS", "name", node.Name, "file", file)
						node.MTLSConfig = nil
						break
					}
				}
			}
		}
	}

	cfg.Logger.Info("Configuration validated", "nodes_count", len(cfg.V2rayStat.Nodes))
	return cfg, nil
}
