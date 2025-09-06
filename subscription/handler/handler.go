package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"v2ray-stat/subscription/api"
	"v2ray-stat/subscription/config"
	"v2ray-stat/subscription/templates"
)

var clientFormats = map[string]struct {
	Format string
	Header map[string]string
}{
	"xray": {
		Format: "json",
		Header: map[string]string{"Content-Type": "application/json"},
	},
	"singbox": {
		Format: "json",
		Header: map[string]string{"Content-Type": "application/json"},
	},
	"mihomo": {
		Format: "yaml",
		Header: map[string]string{"Content-Type": "text/yaml"},
	},
}

// SubscriptionHandler handles /api/v1/sub?client=<client>&user=<user>&mode=<mode>.
func SubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	client := r.URL.Query().Get("client")
	user := r.URL.Query().Get("user")
	if client == "" {
		client = "xray"
	}
	if mode == "" {
		mode = "advanced" // Default to JSON/YAML templates
	}
	cfg := config.GetConfig()
	cfg.Logger.Debug("Received subscription request", "client", client, "user", user, "mode", mode)
	if user == "" {
		cfg.Logger.Warn("Missing user parameter in request")
		http.Error(w, "missing user parameter", http.StatusBadRequest)
		return
	}

	if _, ok := clientFormats[client]; !ok {
		cfg.Logger.Warn("Invalid client requested", "client", client)
		http.Error(w, "invalid client", http.StatusBadRequest)
		return
	}

	if mode != "base" && mode != "advanced" {
		cfg.Logger.Warn("Invalid mode requested", "mode", mode)
		http.Error(w, "invalid mode, must be 'base' or 'advanced'", http.StatusBadRequest)
		return
	}

	userConfig, ok := cfg.Subscription.UserMap[user]
	if !ok {
		cfg.Logger.Debug("User not found in UserMap, applying defaults", "user", user)
		userConfig = config.UserConfig{
			Clients:       slices.Clone(cfg.Subscription.Defaults.Clients),
			IncludeNodes:  slices.Clone(cfg.Subscription.Defaults.IncludeNodes),
			NodeTemplates: make(map[string]map[string]string),
			Headers:       make(map[string]string),
		}

		for mode, templates := range cfg.Subscription.Defaults.NodeTemplates {
			userConfig.NodeTemplates[mode] = make(map[string]string)
			maps.Copy(userConfig.NodeTemplates[mode], templates)
		}

		maps.Copy(userConfig.Headers, cfg.Subscription.Defaults.Headers)
	}
	cfg.Logger.Trace("User config before merging", "user", user, "config", fmt.Sprintf("%+v", userConfig))

	// Apply group settings if specified
	if userConfig.Group != "" {
		if groupConfig, ok := cfg.Subscription.Groups[userConfig.Group]; ok {
			cfg.Logger.Debug("Merging group config", "group", userConfig.Group)
			// Полное переопределение, если поле указано в группе
			if groupConfig.Clients != nil {
				userConfig.Clients = slices.Clone(groupConfig.Clients)
			}
			if groupConfig.IncludeNodes != nil {
				userConfig.IncludeNodes = slices.Clone(groupConfig.IncludeNodes)
			}
			if groupConfig.NodeTemplates != nil {
				userConfig.NodeTemplates = make(map[string]map[string]string)
				for mode, templates := range groupConfig.NodeTemplates {
					userConfig.NodeTemplates[mode] = make(map[string]string)
					if templates != nil {
						maps.Copy(userConfig.NodeTemplates[mode], templates)
					}
				}
			}
			if groupConfig.Headers != nil {
				userConfig.Headers = make(map[string]string)
				maps.Copy(userConfig.Headers, groupConfig.Headers)
			}
		} else {
			cfg.Logger.Warn("Group not found for user", "group", userConfig.Group, "user", user)
			http.Error(w, fmt.Sprintf("group %s not found for user %s", userConfig.Group, user), http.StatusBadRequest)
			return
		}
	}

	// Apply defaults for missing fields only if not overridden by group
	if len(userConfig.Clients) == 0 {
		userConfig.Clients = slices.Clone(cfg.Subscription.Defaults.Clients)
		cfg.Logger.Debug("Applied default clients for user", "user", user)
	}
	if len(userConfig.IncludeNodes) == 0 {
		userConfig.IncludeNodes = slices.Clone(cfg.Subscription.Defaults.IncludeNodes)
		cfg.Logger.Debug("Applied default nodes for user", "user", user)
	}
	if len(userConfig.NodeTemplates) == 0 {
		userConfig.NodeTemplates = make(map[string]map[string]string)
		for mode, templates := range cfg.Subscription.Defaults.NodeTemplates {
			userConfig.NodeTemplates[mode] = make(map[string]string)
			maps.Copy(userConfig.NodeTemplates[mode], templates)
		}
		cfg.Logger.Debug("Applied default templates for user", "user", user)
	}
	if len(userConfig.Headers) == 0 {
		userConfig.Headers = make(map[string]string)
		maps.Copy(userConfig.Headers, cfg.Subscription.Defaults.Headers)
		cfg.Logger.Debug("Applied default headers for user", "user", user)
	}

	if !slices.Contains(userConfig.Clients, client) {
		cfg.Logger.Warn("Client not supported for user", "client", client, "user", user)
		http.Error(w, fmt.Sprintf("client %s not supported for user %s", client, user), http.StatusBadRequest)
		return
	}

	userIDs, err := api.GetUserIDs(&cfg, user)
	if err != nil {
		cfg.Logger.Error("Failed to fetch user IDs", "user", user, "error", err)
		http.Error(w, fmt.Sprintf("failed to fetch user IDs: %v", err), http.StatusInternalServerError)
		return
	}
	cfg.Logger.Debug("Fetched user IDs", "user", user, "count", len(userIDs))

	var configs []string
	var totalUplink, totalDownlink, maxSubEnd, maxTrafficCap int64

	tmpls := templates.GetTemplates()

	for _, node := range userConfig.IncludeNodes {
		cfg.Logger.Trace("Processing node for user", "node", node, "user", user)
		// Get template name for the specific mode
		modeTemplates, modeOk := userConfig.NodeTemplates[mode]
		if !modeOk || modeTemplates == nil {
			cfg.Logger.Debug("No templates for mode", "mode", mode, "user", user)
			continue
		}
		templateName, ok := modeTemplates[node]
		if !ok || templateName == "" {
			cfg.Logger.Debug("No template specified for node in mode or empty name", "node", node, "mode", mode, "user", user)
			continue
		}

		// Prepend mode-specific directory (base/ or advance/)
		templatePath := filepath.Join(mode, templateName)

		template, ok := tmpls[client][templatePath]
		if !ok {
			cfg.Logger.Warn("Template not found for client for user", "template", templatePath, "client", client, "user", user)
			continue
		}

		var userID string
		var nodeTraffic api.UserID
		for _, uid := range userIDs {
			if uid.NodeName == node && uid.User == user {
				userID = uid.ID
				nodeTraffic = uid
				break
			}
		}
		if userID == "" {
			cfg.Logger.Warn("No user ID found for user on node", "user", user, "node", node)
			continue
		}

		// Get domain for the node
		domain, ok := cfg.Subscription.Domains[node]
		if !ok {
			cfg.Logger.Warn("No domain specified for node", "node", node, "user", user)
			continue
		}

		totalUplink += nodeTraffic.Uplink
		totalDownlink += nodeTraffic.Downlink
		if nodeTraffic.SubEnd > maxSubEnd {
			maxSubEnd = nodeTraffic.SubEnd
		}
		if nodeTraffic.TrafficCap > maxTrafficCap {
			maxTrafficCap = nodeTraffic.TrafficCap
		}

		// Replace placeholders
		configStr := strings.ReplaceAll(template, "{user_id}", userID)
		configStr = strings.ReplaceAll(configStr, "{domain}", domain)
		configs = append(configs, configStr)
		cfg.Logger.Trace("Generated config for node", "node", node, "user", user, "domain", domain)
	}

	if len(configs) == 0 {
		cfg.Logger.Warn("No configurations generated for user", "user", user)
		http.Error(w, "no configurations generated", http.StatusNotFound)
		return
	}
	cfg.Logger.Debug("Generated configurations", "count", len(configs), "user", user)

	var output any
	if mode == "base" {
		// For all clients in base mode, combine configs as text and encode in base64
		combinedConfig := strings.Join(configs, "\n")
		encodedConfig := base64.StdEncoding.EncodeToString([]byte(combinedConfig))
		output = encodedConfig
		w.Header().Set("Content-Type", "text/plain")
		cfg.Logger.Trace("Prepared base64-encoded config for base mode", "user", user, "client", client)
	} else if client == "mihomo" {
		// For mihomo in advanced mode, combine into YAML proxies
		baseTemplate, ok := tmpls[client]["base/base.yaml"]
		if !ok {
			cfg.Logger.Warn("Base template not found for client for user", "client", client, "user", user)
			http.Error(w, "base template not found", http.StatusInternalServerError)
			return
		}

		var configStrings []string
		for _, config := range configs {
			lines := strings.Split(config, "\n")
			indentedLines := make([]string, 0, len(lines))
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					indentedLines = append(indentedLines, "  - "+line)
				}
			}
			configStrings = append(configStrings, strings.Join(indentedLines, "\n"))
		}
		combinedProxies := strings.Join(configStrings, "\n")
		configStr := strings.Replace(baseTemplate, "proxies: []", fmt.Sprintf("proxies:\n%s", combinedProxies), 1)
		output = []string{configStr}
		cfg.Logger.Trace("Combined proxies for mihomo", "user", user)
	} else {
		// For xray and singbox in advanced mode, return JSON array
		var parsedConfigs []any
		for _, configStr := range configs {
			var config any
			if clientFormats[client].Format == "json" {
				if err := json.Unmarshal([]byte(configStr), &config); err != nil {
					cfg.Logger.Error("Failed to parse template for client for user", "client", client, "user", user, "error", err)
					continue
				}
			} else {
				config = configStr
			}
			parsedConfigs = append(parsedConfigs, config)
		}
		if len(parsedConfigs) == 0 {
			cfg.Logger.Warn("No valid configurations generated for user", "user", user)
			http.Error(w, "no valid configurations generated", http.StatusNotFound)
			return
		}
		output = parsedConfigs
		cfg.Logger.Debug("Parsed configurations for non-mihomo client", "client", client, "count", len(parsedConfigs))
	}

	for k, v := range clientFormats[client].Header {
		// Skip setting Content-Type for base mode, as it's already set to text/plain
		if mode == "base" && k == "Content-Type" {
			continue
		}
		w.Header().Set(k, v)
	}

	userInfo := fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", totalUplink, totalDownlink, maxTrafficCap, maxSubEnd)
	w.Header().Set("Subscription-Userinfo", userInfo)
	for k, v := range userConfig.Headers {
		w.Header().Set(k, v)
	}
	cfg.Logger.Trace("Set response headers", "user", user)

	switch {
	case mode == "base":
		if _, err := w.Write([]byte(output.(string))); err != nil {
			cfg.Logger.Error("Failed to write base64-encoded response", "user", user, "error", err)
			http.Error(w, fmt.Sprintf("failed to write response: %v", err), http.StatusInternalServerError)
			return
		}
		cfg.Logger.Debug("Sent base64-encoded response", "user", user)
	case clientFormats[client].Format == "json":
		formattedJSON, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			cfg.Logger.Error("Failed to marshal JSON for client for user", "client", client, "user", user, "error", err)
			http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
		if _, err := w.Write(formattedJSON); err != nil {
			cfg.Logger.Error("Failed to write JSON response", "user", user, "error", err)
			http.Error(w, fmt.Sprintf("failed to write response: %v", err), http.StatusInternalServerError)
			return
		}
		cfg.Logger.Debug("Sent JSON response", "user", user)
	case clientFormats[client].Format == "yaml":
		if _, err := w.Write([]byte(output.([]string)[0])); err != nil {
			cfg.Logger.Error("Failed to write YAML response", "user", user, "error", err)
			http.Error(w, fmt.Sprintf("failed to write response: %v", err), http.StatusInternalServerError)
			return
		}
		cfg.Logger.Debug("Sent YAML response", "user", user)
	}
}
