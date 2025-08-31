package config

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"v2ray-stat/logger"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// Config holds the configuration settings for the backend.
type Config struct {
	Log          LogConfig          `yaml:"log"`
	V2RSSub      V2RSSubConfig      `yaml:"v2rs-sub"`
	Timezone     string             `yaml:"timezone"`
	Subscription SubscriptionConfig `yaml:"subscription"`
	Logger       *logger.Logger
}

type LogConfig struct {
	LogLevel string `yaml:"loglevel"`
	LogMode  string `yaml:"logmode"`
}

// V2RSSubConfig holds v2rs-sub specific settings.
type V2RSSubConfig struct {
	Address  string `yaml:"address"`
	Port     string `yaml:"port"`
	GrpcPort string `yaml:"grpc_port"`
}

type SubscriptionConfig struct {
	Defaults DefaultsConfig        `yaml:"defaults"`
	Groups   map[string]UserConfig `yaml:"groups"`
	Users    map[string]UserConfig `yaml:"users"`
}

type DefaultsConfig struct {
	Clients       []string          `yaml:"clients"`
	IncludeNodes  []string          `yaml:"nodes"`
	NodeTemplates map[string]string `yaml:"templates"`
	Headers       map[string]string `yaml:"headers"`
}

type UserConfig struct {
	Group         string            `yaml:"group"`
	Clients       []string          `yaml:"clients"`
	IncludeNodes  []string          `yaml:"nodes"`
	NodeTemplates map[string]string `yaml:"templates"`
	Headers       map[string]string `yaml:"headers"`
}

var (
	config Config
	mu     sync.RWMutex
)

var defaultConfig = Config{
	Log: LogConfig{
		LogLevel: "none",
		LogMode:  "inclusive",
	},
	V2RSSub: V2RSSubConfig{
		Address:  "127.0.0.1",
		Port:     "9954",
		GrpcPort: "9983",
	},
	Timezone: "",
	Subscription: SubscriptionConfig{
		Defaults: DefaultsConfig{
			Clients:       []string{},
			IncludeNodes:  []string{},
			NodeTemplates: map[string]string{},
			Headers:       map[string]string{},
		},
		Groups: map[string]UserConfig{},
		Users:  map[string]UserConfig{},
	},
}

// LoadConfig reads configuration from the specified YAML file.
func LoadConfig(configFile string) (Config, error) {
	cfg := defaultConfig
	cfg.Logger, _ = logger.NewLoggerWithValidation("debug", "inclusive", cfg.Timezone, os.Stderr)
	cfg.Logger.Trace("Attempting to load config file", "file", configFile)

	_, err := os.Stat(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Logger, _ = logger.NewLoggerWithValidation("warn", "inclusive", cfg.Timezone, os.Stderr)
			cfg.Logger.Warn("Configuration file not found, using default values", "file", configFile)
			mu.Lock()
			config = cfg
			mu.Unlock()
			return cfg, nil
		}
		cfg.Logger.Error("Error accessing configuration file", "file", configFile, "error", err)
		return cfg, fmt.Errorf("error accessing configuration file %s: %v", configFile, err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		cfg.Logger.Error("Error reading configuration file", "file", configFile, "error", err)
		return cfg, fmt.Errorf("error reading configuration file %s: %v", configFile, err)
	}
	cfg.Logger.Debug("Read config file successfully", "file", configFile)

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		cfg.Logger.Error("Error parsing YAML configuration", "file", configFile, "error", err)
		return cfg, fmt.Errorf("error parsing YAML configuration from %s: %v", configFile, err)
	}

	cfg.Logger, err = logger.NewLoggerWithValidation(cfg.Log.LogLevel, cfg.Log.LogMode, cfg.Timezone, os.Stderr)
	if err != nil {
		cfg.Logger.Error("Failed to initialize logger", "error", err)
		return cfg, fmt.Errorf("failed to initialize logger: %v", err)
	}
	cfg.Logger.Info("Logger initialized with level and mode", "level", cfg.Log.LogLevel, "mode", cfg.Log.LogMode)

	// Validate configuration
	if cfg.V2RSSub.Port != "" {
		portNum, err := strconv.Atoi(cfg.V2RSSub.Port)
		if err != nil || portNum < 1 || portNum > 65535 {
			cfg.Logger.Warn("Invalid v2rs-sub.port, using default", "port", cfg.V2RSSub.Port, "default", defaultConfig.V2RSSub.Port)
			cfg.V2RSSub.Port = defaultConfig.V2RSSub.Port
		}
	}

	if cfg.V2RSSub.GrpcPort != "" {
		portNum, err := strconv.Atoi(cfg.V2RSSub.GrpcPort)
		if err != nil || portNum < 1 || portNum > 65535 {
			cfg.Logger.Warn("Invalid grpc_port, using default", "port", cfg.V2RSSub.GrpcPort, "default", defaultConfig.V2RSSub.GrpcPort)
			cfg.V2RSSub.GrpcPort = defaultConfig.V2RSSub.GrpcPort
		}
	}

	if cfg.Timezone != "" {
		if _, err := time.LoadLocation(cfg.Timezone); err != nil {
			cfg.Logger.Warn("Invalid timezone value, using default", "timezone", cfg.Timezone)
			cfg.Timezone = defaultConfig.Timezone
		}
	}

	// Validate defaults
	if len(cfg.Subscription.Defaults.Clients) == 0 {
		cfg.Logger.Warn("defaults.clients is empty, using empty list")
	}
	if len(cfg.Subscription.Defaults.IncludeNodes) == 0 {
		cfg.Logger.Warn("defaults.nodes is empty, using empty list")
	}
	if len(cfg.Subscription.Defaults.NodeTemplates) == 0 {
		cfg.Logger.Warn("defaults.templates is empty, using empty map")
	}
	for _, node := range cfg.Subscription.Defaults.IncludeNodes {
		if _, ok := cfg.Subscription.Defaults.NodeTemplates[node]; !ok {
			cfg.Logger.Warn("defaults: no template specified for node", "node", node)
		}
	}

	// Validate groups
	for groupName, group := range cfg.Subscription.Groups {
		if len(group.Clients) == 0 {
			cfg.Logger.Warn("group clients is empty", "group", groupName)
		}
		if len(group.IncludeNodes) == 0 {
			cfg.Logger.Warn("group nodes is empty", "group", groupName)
		}
		if len(group.NodeTemplates) == 0 {
			cfg.Logger.Warn("group templates is empty", "group", groupName)
		}
		for _, node := range group.IncludeNodes {
			if _, ok := group.NodeTemplates[node]; !ok {
				cfg.Logger.Warn("group: no template specified for node", "group", groupName, "node", node)
			}
		}
	}

	// Validate users
	for userName, user := range cfg.Subscription.Users {
		if user.Group != "" {
			if _, ok := cfg.Subscription.Groups[user.Group]; !ok {
				cfg.Logger.Warn("user: group not found", "user", userName, "group", user.Group)
			}
		}
		// If user has nodes, ensure templates exist
		if len(user.IncludeNodes) > 0 {
			for _, node := range user.IncludeNodes {
				if _, ok := user.NodeTemplates[node]; !ok {
					if user.Group != "" {
						if group, ok := cfg.Subscription.Groups[user.Group]; ok {
							if _, ok := group.NodeTemplates[node]; !ok {
								cfg.Logger.Warn("user: no template specified for node in group or user config", "user", userName, "node", node, "group", user.Group)
							}
						}
					} else if _, ok := cfg.Subscription.Defaults.NodeTemplates[node]; !ok {
						cfg.Logger.Warn("user: no template specified for node in user config or defaults", "user", userName, "node", node)
					}
				}
			}
		}
	}

	cfg.Logger.Debug("Configuration validated")
	cfg.Logger.Info("Configuration loaded successfully", "file", configFile)

	// Update global config
	mu.Lock()
	config = cfg
	mu.Unlock()

	return cfg, nil
}

func GetConfig() Config {
	mu.RLock()
	defer mu.RUnlock()
	return config
}

// WatchConfig watches the config file for changes and reloads it.
func WatchConfig(ctx context.Context, cfg *Config, wg *sync.WaitGroup) {
	defer wg.Done()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Trace("Starting to watch config file", "file", "config.yaml")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cfg.Logger.Error("Failed to create config watcher", "error", err)
		return
	}
	defer watcher.Close()

	err = watcher.Add("config.yaml")
	if err != nil {
		cfg.Logger.Error("Failed to add watch for config.yaml", "error", err)
		return
	}
	cfg.Logger.Debug("Added watch for config.yaml")

	for {
		select {
		case <-ctx.Done():
			cfg.Logger.Debug("Stopping config watcher due to context cancellation")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				cfg.Logger.Warn("Config watcher closed unexpectedly")
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				cfg.Logger.Info("config.yaml changed, reloading...")
				if newCfg, err := LoadConfig("config.yaml"); err != nil {
					cfg.Logger.Error("Failed to reload config", "error", err)
				} else {
					mu.Lock()
					config = newCfg
					mu.Unlock()
					cfg.Logger.Info("Config reloaded successfully")
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				cfg.Logger.Warn("Config watcher error channel closed")
				return
			}
			cfg.Logger.Error("Watcher error", "error", err)
		}
	}
}
