package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"v2ray-stat/node/common"
	"v2ray-stat/node/config"
	"v2ray-stat/node/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetUserEnabled enables or disables a user on the node.
func (s *NodeServer) SetUserEnabled(ctx context.Context, req *proto.SetUserEnabledRequest) (*proto.OperationResponse, error) {
	err := toggleUserEnabled(s.Cfg, req.Username, req.Enabled)
	if err != nil {
		s.Cfg.Logger.Error("Ошибка переключения статуса пользователя", "user", req.Username, "enabled", req.Enabled, "error", err)
		return nil, status.Errorf(codes.FailedPrecondition, "failed to toggle user enabled status: %v", err)
	}
	s.Cfg.Logger.Info("Статус пользователя переключён успешно", "user", req.Username, "enabled", req.Enabled)
	return &proto.OperationResponse{}, nil
}

// toggleUserEnabled toggles the enabled status of a user in configuration files.
func toggleUserEnabled(cfg *config.NodeConfig, userIdentifier string, enabled bool) error {
	cfg.Logger.Debug("Переключение статуса пользователя", "user", userIdentifier, "enabled", enabled)
	mainConfigPath := cfg.Core.Config
	disabledUsersPath := filepath.Join(cfg.Core.Dir, ".disabled_users")

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	cfg.Logger.Trace("Статус пользователя", "user", userIdentifier, "status", status)

	switch cfg.V2rayStat.Type {
	case "xray":
		mainConfigData, err := os.ReadFile(mainConfigPath)
		if err != nil {
			cfg.Logger.Error("Failed to read Xray main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error reading Xray main config: %v", err)
		}
		var mainConfig config.ConfigXray
		if err := json.Unmarshal(mainConfigData, &mainConfig); err != nil {
			cfg.Logger.Error("Failed to parse Xray main config", "error", err)
			return fmt.Errorf("error parsing Xray main config: %v", err)
		}

		var disabledConfig config.DisabledUsersConfigXray
		disabledConfigData, err := os.ReadFile(disabledUsersPath)
		if err != nil {
			if os.IsNotExist(err) {
				cfg.Logger.Warn("Disabled users file does not exist", "path", disabledUsersPath)
				disabledConfig = config.DisabledUsersConfigXray{Inbounds: []config.XrayInbound{}}
			} else {
				cfg.Logger.Error("Failed to read Xray disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error reading Xray disabled users file: %v", err)
			}
		} else if len(disabledConfigData) == 0 {
			cfg.Logger.Warn("Empty disabled users file", "path", disabledUsersPath)
			disabledConfig = config.DisabledUsersConfigXray{Inbounds: []config.XrayInbound{}}
		} else {
			if err := json.Unmarshal(disabledConfigData, &disabledConfig); err != nil {
				cfg.Logger.Error("Failed to parse Xray disabled users file", "error", err)
				return fmt.Errorf("error parsing Xray disabled users file: %v", err)
			}
		}

		sourceInbounds := mainConfig.Inbounds
		targetInbounds := disabledConfig.Inbounds
		if enabled {
			sourceInbounds = disabledConfig.Inbounds
			targetInbounds = mainConfig.Inbounds
		}

		userMap := make(map[string]config.XrayClient)
		found := false
		for i, inbound := range sourceInbounds {
			if inbound.Protocol == "vless" || inbound.Protocol == "trojan" {
				newClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients))
				clientMap := make(map[string]bool)
				for _, client := range inbound.Settings.Clients {
					cfg.Logger.Trace("Processing client in inbound", "tag", inbound.Tag, "email", client.Email)
					if client.Email == userIdentifier {
						if !clientMap[client.Email] {
							userMap[inbound.Tag] = client
							clientMap[client.Email] = true
							found = true
						}
					} else {
						if !clientMap[client.Email] {
							newClients = append(newClients, client)
							clientMap[client.Email] = true
						}
					}
				}
				sourceInbounds[i].Settings.Clients = newClients
			}
		}

		if !found {
			cfg.Logger.Error("User not found in inbounds with vless or trojan protocols", "user", userIdentifier)
			return fmt.Errorf("user %s not found in inbounds with vless or trojan protocols", userIdentifier)
		}

		for _, inbound := range targetInbounds {
			if inbound.Protocol == "vless" || inbound.Protocol == "trojan" {
				for _, client := range inbound.Settings.Clients {
					if client.Email == userIdentifier {
						cfg.Logger.Error("User already exists in target Xray config", "user", userIdentifier, "tag", inbound.Tag)
						return fmt.Errorf("user %s already exists in target Xray config with tag %s", userIdentifier, inbound.Tag)
					}
				}
			}
		}

		for i, inbound := range targetInbounds {
			if inbound.Protocol == "vless" || inbound.Protocol == "trojan" {
				if client, exists := userMap[inbound.Tag]; exists {
					clientMap := make(map[string]bool)
					newClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients)+1)
					for _, c := range inbound.Settings.Clients {
						if !clientMap[c.Email] {
							newClients = append(newClients, c)
							clientMap[c.Email] = true
						}
					}
					if !clientMap[userIdentifier] {
						newClients = append(newClients, client)
						cfg.Logger.Debug("User set to status in inbound", "user", userIdentifier, "status", status, "tag", inbound.Tag)
					}
					targetInbounds[i].Settings.Clients = newClients
				}
			}
		}

		for _, mainInbound := range mainConfig.Inbounds {
			if (mainInbound.Protocol == "vless" || mainInbound.Protocol == "trojan") && !hasInboundXray(targetInbounds, mainInbound.Tag) {
				if client, exists := userMap[mainInbound.Tag]; exists {
					newInbound := mainInbound
					newInbound.Settings.Clients = []config.XrayClient{client}
					targetInbounds = append(targetInbounds, newInbound)
					cfg.Logger.Info("Created new inbound for user", "tag", newInbound.Tag, "user", userIdentifier)
				}
			}
		}

		if enabled {
			mainConfig.Inbounds = targetInbounds
			disabledConfig.Inbounds = sourceInbounds
		} else {
			mainConfig.Inbounds = sourceInbounds
			disabledConfig.Inbounds = targetInbounds
		}

		mainConfigData, err = json.MarshalIndent(mainConfig, "", "  ")
		if err != nil {
			cfg.Logger.Error("Failed to serialize Xray main config", "error", err)
			return fmt.Errorf("error serializing Xray main config: %v", err)
		}
		if err := os.WriteFile(mainConfigPath, mainConfigData, 0644); err != nil {
			cfg.Logger.Error("Failed to write Xray main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error writing Xray main config: %v", err)
		}

		if len(disabledConfig.Inbounds) > 0 {
			disabledConfigData, err = json.MarshalIndent(disabledConfig, "", "  ")
			if err != nil {
				cfg.Logger.Error("Failed to serialize Xray disabled users file", "error", err)
				return fmt.Errorf("error serializing Xray disabled users file: %v", err)
			}
			if err := os.WriteFile(disabledUsersPath, disabledConfigData, 0644); err != nil {
				cfg.Logger.Error("Failed to write Xray disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error writing Xray disabled users file: %v", err)
			}
		} else {
			if err := os.Remove(disabledUsersPath); err != nil && !os.IsNotExist(err) {
				cfg.Logger.Error("Failed to remove empty .disabled_users for Xray", "error", err)
			}
		}

		if cfg.Features["restart"] {
			if err := common.RestartService("xray", cfg); err != nil {
				return fmt.Errorf("failed to restart xray service: %v", err)
			}
		}

	case "singbox":
		mainConfigData, err := os.ReadFile(mainConfigPath)
		if err != nil {
			cfg.Logger.Error("Failed to read Singbox main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error reading Singbox main config: %v", err)
		}
		var mainConfig config.ConfigSingbox
		if err := json.Unmarshal(mainConfigData, &mainConfig); err != nil {
			cfg.Logger.Error("Failed to parse Singbox main config", "error", err)
			return fmt.Errorf("error parsing Singbox main config: %v", err)
		}

		var disabledConfig config.DisabledUsersConfigSingbox
		disabledConfigData, err := os.ReadFile(disabledUsersPath)
		if err != nil {
			if os.IsNotExist(err) {
				cfg.Logger.Warn("Disabled users file does not exist", "path", disabledUsersPath)
				disabledConfig = config.DisabledUsersConfigSingbox{Inbounds: []config.SingboxInbound{}}
			} else {
				cfg.Logger.Error("Failed to read Singbox disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error reading Singbox disabled users file: %v", err)
			}
		} else if len(disabledConfigData) == 0 {
			cfg.Logger.Warn("Empty disabled users file", "path", disabledUsersPath)
			disabledConfig = config.DisabledUsersConfigSingbox{Inbounds: []config.SingboxInbound{}}
		} else {
			if err := json.Unmarshal(disabledConfigData, &disabledConfig); err != nil {
				cfg.Logger.Error("Failed to parse Singbox disabled users file", "error", err)
				return fmt.Errorf("error parsing Singbox disabled users file: %v", err)
			}
		}

		sourceInbounds := mainConfig.Inbounds
		targetInbounds := disabledConfig.Inbounds
		if enabled {
			sourceInbounds = disabledConfig.Inbounds
			targetInbounds = mainConfig.Inbounds
		}

		userMap := make(map[string]config.SingboxClient)
		found := false
		for i, inbound := range sourceInbounds {
			if inbound.Type == "vless" || inbound.Type == "trojan" {
				newUsers := make([]config.SingboxClient, 0, len(inbound.Users))
				userNameMap := make(map[string]bool)
				for _, user := range inbound.Users {
					cfg.Logger.Trace("Processing user in inbound", "tag", inbound.Tag, "name", user.Name)
					if user.Name == userIdentifier {
						if !userNameMap[user.Name] {
							userMap[inbound.Tag] = user
							userNameMap[user.Name] = true
							found = true
						}
					} else {
						if !userNameMap[user.Name] {
							newUsers = append(newUsers, user)
							userNameMap[user.Name] = true
						}
					}
				}
				sourceInbounds[i].Users = newUsers
			}
		}

		if !found {
			cfg.Logger.Error("User not found in inbounds with vless or trojan protocols for Singbox", "user", userIdentifier)
			return fmt.Errorf("user %s not found in inbounds with vless or trojan protocols for Singbox", userIdentifier)
		}

		for _, inbound := range targetInbounds {
			if inbound.Type == "vless" || inbound.Type == "trojan" {
				for _, user := range inbound.Users {
					if user.Name == userIdentifier {
						cfg.Logger.Error("User already exists in target Singbox config", "user", userIdentifier, "tag", inbound.Tag)
						return fmt.Errorf("user %s already exists in target Singbox config with tag %s", userIdentifier, inbound.Tag)
					}
				}
			}
		}

		for i, inbound := range targetInbounds {
			if inbound.Type == "vless" || inbound.Type == "trojan" {
				if user, exists := userMap[inbound.Tag]; exists {
					userNameMap := make(map[string]bool)
					newUsers := make([]config.SingboxClient, 0, len(inbound.Users)+1)
					for _, u := range inbound.Users {
						if !userNameMap[u.Name] {
							newUsers = append(newUsers, u)
							userNameMap[u.Name] = true
						}
					}
					if !userNameMap[userIdentifier] {
						newUsers = append(newUsers, user)
						cfg.Logger.Debug("User set to status in inbound", "user", userIdentifier, "status", status, "tag", inbound.Tag)
					}
					targetInbounds[i].Users = newUsers
				}
			}
		}

		for _, mainInbound := range mainConfig.Inbounds {
			if (mainInbound.Type == "vless" || mainInbound.Type == "trojan") && !hasInboundSingbox(targetInbounds, mainInbound.Tag) {
				if user, exists := userMap[mainInbound.Tag]; exists {
					newInbound := mainInbound
					newInbound.Users = []config.SingboxClient{user}
					targetInbounds = append(targetInbounds, newInbound)
					cfg.Logger.Info("Created new inbound for user", "tag", newInbound.Tag, "user", userIdentifier)
				}
			}
		}

		if enabled {
			mainConfig.Inbounds = targetInbounds
			disabledConfig.Inbounds = sourceInbounds
		} else {
			mainConfig.Inbounds = sourceInbounds
			disabledConfig.Inbounds = targetInbounds
		}

		mainConfigData, err = json.MarshalIndent(mainConfig, "", "  ")
		if err != nil {
			cfg.Logger.Error("Failed to serialize Singbox main config", "error", err)
			return fmt.Errorf("error serializing Singbox main config: %v", err)
		}
		if err := os.WriteFile(mainConfigPath, mainConfigData, 0644); err != nil {
			cfg.Logger.Error("Failed to write Singbox main config", "path", mainConfigPath, "error", err)
			return fmt.Errorf("error writing Singbox main config: %v", err)
		}

		if len(disabledConfig.Inbounds) > 0 {
			disabledConfigData, err = json.MarshalIndent(disabledConfig, "", "  ")
			if err != nil {
				cfg.Logger.Error("Failed to serialize Singbox disabled users file", "error", err)
				return fmt.Errorf("error serializing Singbox disabled users file: %v", err)
			}
			if err := os.WriteFile(disabledUsersPath, disabledConfigData, 0644); err != nil {
				cfg.Logger.Error("Failed to write Singbox disabled users file", "path", disabledUsersPath, "error", err)
				return fmt.Errorf("error writing Singbox disabled users file: %v", err)
			}
		} else {
			if err := os.Remove(disabledUsersPath); err != nil && !os.IsNotExist(err) {
				cfg.Logger.Error("Failed to remove empty .disabled_users for Singbox", "error", err)
			}
		}

		if cfg.Features["restart"] {
			if err := common.RestartService("sing-box", cfg); err != nil {
				return fmt.Errorf("failed to restart sing-box service: %v", err)
			}
		}
	}

	cfg.Logger.Debug("Статус пользователя переключён", "user", userIdentifier, "enabled", enabled)
	return nil
}

// hasInboundXray checks if an inbound with the given tag exists in the Xray configuration.
func hasInboundXray(inbounds []config.XrayInbound, tag string) bool {
	for _, inbound := range inbounds {
		if inbound.Tag == tag {
			return true
		}
	}
	return false
}

// hasInboundSingbox checks if an inbound with the given tag exists in the Singbox configuration.
func hasInboundSingbox(inbounds []config.SingboxInbound, tag string) bool {
	for _, inbound := range inbounds {
		if inbound.Tag == tag {
			return true
		}
	}
	return false
}
