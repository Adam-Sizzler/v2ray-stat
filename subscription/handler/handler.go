package handler

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
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
	"happ": {
		Format: "json",
		Header: map[string]string{"Content-Type": "application/json"},
	},
}

// SubscriptionHandler handles /api/v1/sub?client=<client>&user=<user>.
func SubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	client := r.URL.Query().Get("client")
	user := r.URL.Query().Get("user")
	if client == "" {
		client = "xray"
	}
	cfg := config.GetConfig()
	cfg.Logger.Debug("Received subscription request", "client", client, "user", user)
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

	userConfig := cfg.Subscription.Users[user]
	cfg.Logger.Trace("User config before merging", "user", user, "config", fmt.Sprintf("%+v", userConfig))

	// Apply group settings if specified
	if userConfig.Group != "" {
		if groupConfig, ok := cfg.Subscription.Groups[userConfig.Group]; ok {
			cfg.Logger.Debug("Merging group config", "group", userConfig.Group)
			// Merge group settings with user settings, user settings take precedence
			if len(userConfig.Clients) == 0 {
				userConfig.Clients = groupConfig.Clients
			}
			if len(userConfig.IncludeNodes) == 0 {
				userConfig.IncludeNodes = groupConfig.IncludeNodes
			}
			if len(userConfig.NodeTemplates) == 0 {
				userConfig.NodeTemplates = make(map[string]string)
				maps.Copy(userConfig.NodeTemplates, groupConfig.NodeTemplates)
			} else {
				// Merge templates, user templates override group templates
				for k, v := range groupConfig.NodeTemplates {
					if _, exists := userConfig.NodeTemplates[k]; !exists {
						userConfig.NodeTemplates[k] = v
					}
				}
			}

			// Merge headers, user headers override group headers
			if len(userConfig.Headers) == 0 {
				userConfig.Headers = make(map[string]string)
				for k, v := range groupConfig.Headers {
					userConfig.Headers[k] = v
				}
			} else {
				for k, v := range groupConfig.Headers {
					if _, exists := userConfig.Headers[k]; !exists {
						userConfig.Headers[k] = v
					}
				}
			}
		} else {
			cfg.Logger.Warn("Group not found for user", "group", userConfig.Group, "user", user)
			http.Error(w, fmt.Sprintf("group %s not found for user %s", userConfig.Group, user), http.StatusBadRequest)
			return
		}
	}

	// Apply defaults for missing fields
	if len(userConfig.Clients) == 0 {
		userConfig.Clients = cfg.Subscription.Defaults.Clients
		cfg.Logger.Debug("Applied default clients for user", "user", user)
	}
	if len(userConfig.IncludeNodes) == 0 {
		userConfig.IncludeNodes = cfg.Subscription.Defaults.IncludeNodes
		cfg.Logger.Debug("Applied default nodes for user", "user", user)
	}
	if len(userConfig.NodeTemplates) == 0 {
		userConfig.NodeTemplates = cfg.Subscription.Defaults.NodeTemplates
		cfg.Logger.Debug("Applied default templates for user", "user", user)
	}
	if len(userConfig.Headers) == 0 {
		userConfig.Headers = make(map[string]string)
		maps.Copy(userConfig.Headers, cfg.Subscription.Defaults.Headers)
		cfg.Logger.Debug("Applied default headers for user", "user", user)
	} else {
		for k, v := range cfg.Subscription.Defaults.Headers {
			if _, exists := userConfig.Headers[k]; !exists {
				userConfig.Headers[k] = v
			}
		}
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
		templateName, ok := userConfig.NodeTemplates[node]
		if !ok {
			templateName, ok = cfg.Subscription.Defaults.NodeTemplates[node]
			if !ok {
				cfg.Logger.Warn("No template specified for node in user or defaults, skipping", "node", node, "user", user)
				continue
			}
		}

		template, ok := tmpls[client][templateName]
		if !ok {
			cfg.Logger.Warn("Template not found for client for user", "template", templateName, "client", client, "user", user)
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

		totalUplink += nodeTraffic.Uplink
		totalDownlink += nodeTraffic.Downlink
		if nodeTraffic.SubEnd > maxSubEnd {
			maxSubEnd = nodeTraffic.SubEnd
		}
		if nodeTraffic.TrafficCap > maxTrafficCap {
			maxTrafficCap = nodeTraffic.TrafficCap
		}

		configStr := strings.ReplaceAll(template, "{user_id}", userID)
		configs = append(configs, configStr)
		cfg.Logger.Trace("Generated config for node", "node", node, "user", user)
	}

	if len(configs) == 0 {
		cfg.Logger.Warn("No configurations generated for user", "user", user)
		http.Error(w, "no configurations generated", http.StatusNotFound)
		return
	}
	cfg.Logger.Debug("Generated configurations", "count", len(configs), "user", user)

	var output any
	if client == "mihomo" {
		baseTemplate, ok := tmpls[client]["base"]
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
					indentedLines = append(indentedLines, line)
				}
			}
			configStrings = append(configStrings, strings.Join(indentedLines, "\n"))
		}
		combinedProxies := strings.Join(configStrings, "\n")
		configStr := strings.Replace(baseTemplate, "proxies: []", fmt.Sprintf("proxies:\n%s", combinedProxies), 1)
		output = []string{configStr}
		cfg.Logger.Trace("Combined proxies for mihomo", "user", user)
	} else {
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
		w.Header().Set(k, v)
	}

	userInfo := fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", totalUplink, totalDownlink, maxTrafficCap, maxSubEnd)
	w.Header().Set("Subscription-Userinfo", userInfo)
	for k, v := range userConfig.Headers {
		w.Header().Set(k, v)
	}
	cfg.Logger.Trace("Set response headers", "user", user)

	switch clientFormats[client].Format {
	case "json":
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
	case "yaml":
		if _, err := w.Write([]byte(output.([]string)[0])); err != nil {
			cfg.Logger.Error("Failed to write YAML response", "user", user, "error", err)
			http.Error(w, fmt.Sprintf("failed to write response: %v", err), http.StatusInternalServerError)
			return
		}
		cfg.Logger.Debug("Sent YAML response", "user", user)
	}
}
