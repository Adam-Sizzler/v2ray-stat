package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"v2ray-stat/subscription/config"

	"github.com/fsnotify/fsnotify"
)

var (
	templates = map[string]map[string]string{
		"xray":    {},
		"singbox": {},
		"mihomo":  {},
		"happ":    {},
	}
	mu sync.RWMutex
)

// LoadTemplates loads all templates from the templates/ directory into memory.
func LoadTemplates(cfg *config.Config) error {
	cfg.Logger.Trace("Starting to load templates from directories")
	newTemplates := map[string]map[string]string{
		"xray":    {},
		"singbox": {},
		"mihomo":  {},
		"happ":    {},
	}
	clients := []string{"xray", "singbox", "mihomo", "happ"}
	for _, client := range clients {
		dir := filepath.Join("templates", client)
		files, err := os.ReadDir(dir)
		if err != nil {
			cfg.Logger.Error("Failed to read templates directory", "client", client, "error", err)
			return fmt.Errorf("failed to read templates/%s: %w", client, err)
		}
		cfg.Logger.Debug("Reading templates for client", "client", client, "file_count", len(files))
		for _, file := range files {
			if !file.IsDir() {
				path := filepath.Join(dir, file.Name())
				content, err := os.ReadFile(path)
				if err != nil {
					cfg.Logger.Error("Failed to read template file", "path", path, "error", err)
					return fmt.Errorf("failed to read %s: %w", path, err)
				}
				templateName := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
				newTemplates[client][templateName] = string(content)
				cfg.Logger.Trace("Loaded template", "client", client, "name", templateName)
			}
		}
	}

	mu.Lock()
	templates = newTemplates
	mu.Unlock()
	cfg.Logger.Debug("All templates loaded", "total_clients", len(clients))
	return nil
}

// GetTemplates returns the loaded templates.
func GetTemplates() map[string]map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	return templates
}

// WatchTemplates starts watching the templates/ directory for changes.
func WatchTemplates(cfg *config.Config) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cfg.Logger.Error("Failed to create templates watcher", "error", err)
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	cfg.Logger.Info("Started watching templates directories")

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					cfg.Logger.Info("Templates directory changed, reloading...")
					if err := LoadTemplates(cfg); err != nil {
						cfg.Logger.Error("Failed to reload templates", "error", err)
					} else {
						cfg.Logger.Info("Templates reloaded successfully")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				cfg.Logger.Error("Watcher error", "error", err)
			}
		}
	}()

	clients := []string{"xray", "singbox", "mihomo", "happ"}
	for _, client := range clients {
		dir := filepath.Join("templates", client)
		err := watcher.Add(dir)
		if err != nil {
			cfg.Logger.Error("Failed to add watch for templates directory", "client", client, "error", err)
			watcher.Close()
			return fmt.Errorf("failed to watch templates/%s: %w", client, err)
		}
		cfg.Logger.Debug("Added watch for templates directory", "client", client)
	}

	return nil
}
