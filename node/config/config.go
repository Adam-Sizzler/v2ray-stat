package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"v2ray-stat/logger"

	"gopkg.in/yaml.v3"
)

// NodeConfig holds the configuration settings for the node.
type NodeConfig struct {
	Log       LogConfig       `yaml:"log"`
	V2rayStat V2rayStatConfig `yaml:"v2rs-node"`
	Timezone  string          `yaml:"timezone"`
	Features  map[string]bool `yaml:"features"`
	Core      CoreConfig      `yaml:"core"`
	Paths     PathsConfig     `yaml:"paths"`
	Logger    *logger.Logger
}

type LogConfig struct {
	LogLevel string `yaml:"loglevel"`
	LogMode  string `yaml:"logmode"`
}

type V2rayStatConfig struct {
	Type       string      `yaml:"type"`
	Address    string      `yaml:"address"`
	Port       string      `yaml:"port"`
	MTLSConfig *MTLSConfig `yaml:"mtls"`
}

type CoreConfig struct {
	Dir            string `yaml:"dir"`
	Config         string `yaml:"config"`
	AccessLog      string `yaml:"access_log"`
	AccessLogRegex string `yaml:"access_log_regex"`
}

// PathsConfig holds paths and logging settings.
type PathsConfig struct {
	F2BLog       string `yaml:"f2b_log"`
	F2BBannedLog string `yaml:"f2b_banned_log"`
	AuthLua      string `yaml:"auth_lua"`
}

type MTLSConfig struct {
	Cert   string `yaml:"cert"`
	Key    string `yaml:"key"`
	CACert string `yaml:"ca_cert"`
}

var defaultConfig = NodeConfig{
	Log: LogConfig{
		LogLevel: "trace",
		LogMode:  "inclusive",
	},
	V2rayStat: V2rayStatConfig{
		Type:    "xray",
		Address: "127.0.0.1",
		Port:    "10000",
	},
	Timezone: "UTC",
	Features: make(map[string]bool),
	Core: CoreConfig{
		Dir:            "/usr/local/etc/xray/",
		Config:         "/usr/local/etc/xray/config.json",
		AccessLog:      "/usr/local/etc/xray/access.log",
		AccessLogRegex: `from (?:tcp|udp):([\d\.]+):\d+ accepted (?:tcp|udp):([\w\.\-]+):\d+ \[[^\]]+\] email: (\S+)`,
	},
	Paths: PathsConfig{
		F2BLog:       "/var/log/v2ray-stat.log",
		F2BBannedLog: "/var/log/v2ray-stat-banned.log",
		AuthLua:      "/etc/haproxy/.auth.lua",
	},
}

// LoadNodeConfig reads configuration from the specified YAML file and returns a NodeConfig struct.
func LoadNodeConfig(configFile string) (NodeConfig, error) {
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
	cfg.Logger, _ = logger.NewLoggerWithValidation("info", "inclusive", cfg.Timezone, os.Stderr)
	cfg.Logger.Debug("Read configuration file", "file", configFile, "size", len(data))

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("error parsing YAML configuration from %s: %v", configFile, err)
	}
	cfg.Logger.Debug("Parsed YAML configuration", "address", cfg.V2rayStat.Address, "port", cfg.V2rayStat.Port)

	cfg.Logger, err = logger.NewLoggerWithValidation(cfg.Log.LogLevel, cfg.Log.LogMode, cfg.Timezone, os.Stderr)
	if err != nil {
		return cfg, fmt.Errorf("failed to initialize logger: %v", err)
	}

	if cfg.V2rayStat.Type != "xray" && cfg.V2rayStat.Type != "singbox" {
		cfg.Logger.Warn("Invalid v2ray-stat.type, using default", "type", cfg.V2rayStat.Type, "default", defaultConfig.V2rayStat.Type)
		cfg.V2rayStat.Type = defaultConfig.V2rayStat.Type
	}

	if cfg.V2rayStat.Port != "" {
		portNum, err := strconv.Atoi(cfg.V2rayStat.Port)
		if err != nil || portNum < 1 || portNum > 65535 {
			cfg.Logger.Warn("Invalid v2ray-stat.port, using default", "port", cfg.V2rayStat.Port, "default", defaultConfig.V2rayStat.Port)
			cfg.V2rayStat.Port = defaultConfig.V2rayStat.Port
		}
	}

	if cfg.Core.AccessLogRegex != "" {
		if _, err := regexp.Compile(cfg.Core.AccessLogRegex); err != nil {
			cfg.Logger.Warn("Invalid core.access_log_regex, using default", "regex", cfg.Core.AccessLogRegex, "default", defaultConfig.Core.AccessLogRegex)
			cfg.Core.AccessLogRegex = defaultConfig.Core.AccessLogRegex
		}
	}

	if cfg.Core.Config == "" {
		cfg.Logger.Warn("Core config path is empty, using default", "default", defaultConfig.Core.Config)
		cfg.Core.Config = defaultConfig.Core.Config
	} else if _, err := os.Stat(cfg.Core.Config); os.IsNotExist(err) {
		cfg.Logger.Warn("Core config file not found, using default", "file", cfg.Core.Config, "default", defaultConfig.Core.Config)
		cfg.Core.Config = defaultConfig.Core.Config
	}

	if cfg.V2rayStat.MTLSConfig != nil {
		if cfg.V2rayStat.Address != "127.0.0.1" && cfg.V2rayStat.Address != "0.0.0.0" && cfg.V2rayStat.Address != "localhost" {
			if cfg.V2rayStat.MTLSConfig.Cert == "" || cfg.V2rayStat.MTLSConfig.Key == "" || cfg.V2rayStat.MTLSConfig.CACert == "" {
				cfg.Logger.Error("Incomplete mTLS configuration for non-localhost address", "address", cfg.V2rayStat.Address)
				return cfg, fmt.Errorf("incomplete mTLS configuration for non-localhost address")
			}
			for _, file := range []string{cfg.V2rayStat.MTLSConfig.Cert, cfg.V2rayStat.MTLSConfig.Key, cfg.V2rayStat.MTLSConfig.CACert} {
				if _, err := os.Stat(file); os.IsNotExist(err) {
					cfg.Logger.Error("mTLS certificate file not found for non-localhost address", "file", file, "address", cfg.V2rayStat.Address)
					return cfg, fmt.Errorf("mTLS certificate file not found: %s", file)
				}
			}
		}
	}

	if cfg.Timezone != "" {
		if _, err := time.LoadLocation(cfg.Timezone); err != nil {
			cfg.Logger.Warn("Invalid timezone value, using default", "timezone", cfg.Timezone)
			cfg.Timezone = defaultConfig.Timezone
		}
	}

	// Ensure Features map is initialized
	if cfg.Features == nil {
		cfg.Features = make(map[string]bool)
	}

	cfg.Logger.Info("Node configuration validated", "address", cfg.V2rayStat.Address, "port", cfg.V2rayStat.Port, "mtls_enabled", cfg.V2rayStat.MTLSConfig != nil)
	return cfg, nil
}
