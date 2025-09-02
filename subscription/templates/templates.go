package templates

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
				newTemplates[client][file.Name()] = string(content)
				cfg.Logger.Trace("Loaded template", "client", client, "name", file.Name())
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

// WatchTemplates watches the templates/ directory for changes.
func WatchTemplates(ctx context.Context, cfg *config.Config, wg *sync.WaitGroup) {
	defer wg.Done()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cfg.Logger.Error("Failed to create templates watcher", "error", err)
		return
	}
	defer watcher.Close()
	cfg.Logger.Info("Started watching templates directories")

	clients := []string{"xray", "singbox", "mihomo", "happ"}
	for _, client := range clients {
		dir := filepath.Join("templates", client)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			cfg.Logger.Warn("Templates directory does not exist, skipping warch", "client", client, "dir", dir)
			continue
		}
		err := watcher.Add(dir)
		if err != nil {
			cfg.Logger.Error("Failed to add watch for templates directory", "client", client, "error", err)
			return
		}
		cfg.Logger.Debug("Added watch for templates directory", "client", client)
	}

	for {
		select {
		case <-ctx.Done():
			cfg.Logger.Debug("Stopping templates watcher due to context cancellation")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				cfg.Logger.Warn("Templates watcher closed unexpectedly")
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
				cfg.Logger.Warn("Templates watcher error channel closed")
				return
			}
			cfg.Logger.Error("Watcher error", "error", err)
		}
	}
}
