package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"v2ray-stat/node/common"
	"v2ray-stat/node/config"
	"v2ray-stat/node/lua"
	"v2ray-stat/node/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DeleteUser deletes a user from the node configuration.
func (s *NodeServer) DeleteUser(ctx context.Context, req *proto.DeleteUserRequest) (*proto.OperationResponse, error) {
	err := DeleteUserFromConfig(s.Cfg, req.Username, req.InboundTag)
	if err != nil {
		s.Cfg.Logger.Error("Failed to delete user from config", "error", err)
		return nil, status.Errorf(codes.FailedPrecondition, "failed to delete user: %v", err)
	}

	s.Cfg.Logger.Info("User deleted successfully", "user", req.Username, "inbound_tag", req.InboundTag)
	return &proto.OperationResponse{}, nil
}

// DeleteUserFromConfig removes a user from the configuration file.
func DeleteUserFromConfig(cfg *config.NodeConfig, user, inboundTag string) error {
	cfg.Logger.Debug("Starting user deletion from configuration", "user", user, "inboundTag", inboundTag)
	configPath := cfg.Core.Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to read config.json: %v", err)
	}

	proxyType := cfg.V2rayStat.Type
	var configData any

	switch proxyType {
	case "xray":
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		found := false
		for i, inbound := range cfgXray.Inbounds {
			if inbound.Tag == inboundTag {
				updatedClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients))
				for _, client := range inbound.Settings.Clients {
					if client.Email != user {
						updatedClients = append(updatedClients, client)
					}
				}
				if len(updatedClients) < len(inbound.Settings.Clients) {
					cfgXray.Inbounds[i].Settings.Clients = updatedClients
					found = true
				}
			}
		}
		if !found {
			cfg.Logger.Warn("Inbound or user not found", "inboundTag", inboundTag, "user", user)
			return fmt.Errorf("inbound with tag %s or user %s not found", inboundTag, user)
		}
		configData = cfgXray

	case "singbox":
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		found := false
		for i, inbound := range cfgSingbox.Inbounds {
			if inbound.Tag == inboundTag {
				updatedUsers := make([]config.SingboxClient, 0, len(inbound.Users))
				for _, u := range inbound.Users {
					if u.Name != user {
						updatedUsers = append(updatedUsers, u)
					}
				}
				if len(updatedUsers) < len(inbound.Users) {
					cfgSingbox.Inbounds[i].Users = updatedUsers
					found = true
				}
			}
		}
		if !found {
			cfg.Logger.Warn("Inbound or user not found", "inboundTag", inboundTag, "user", user)
			return fmt.Errorf("inbound with tag %s or user %s not found", inboundTag, user)
		}
		configData = cfgSingbox

	default:
		cfg.Logger.Warn("Unsupported core type", "proxyType", proxyType)
		return fmt.Errorf("unsupported core type: %s", proxyType)
	}

	updateData, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		cfg.Logger.Error("Failed to marshal JSON", "error", err)
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}
	if err := os.WriteFile(configPath, updateData, 0644); err != nil {
		cfg.Logger.Error("Failed to write config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to write config.json: %v", err)
	}

	cfg.Logger.Debug("User removed from configuration", "user", user, "inboundTag", inboundTag)

	if cfg.Features["restart"] {
		serviceName := "xray"
		if proxyType == "singbox" {
			serviceName = "sing-box"
		}
		if err := common.RestartService(serviceName, cfg); err != nil {
			return fmt.Errorf("failed to restart core service: %v", err)
		}
	}

	if cfg.Features["auth_lua"] {
		cfg.Logger.Debug("Deleting user from auth.lua", "user", user)
		if err := lua.DeleteUserFromAuthLua(cfg, user); err != nil {
			cfg.Logger.Error("Failed to delete user from auth.lua", "user", user, "error", err)
		} else {
			cfg.Logger.Debug("User removed from auth.lua", "user", user)
			if cfg.Features["restart"] {
				if err := common.RestartService("haproxy", cfg); err != nil {
					return fmt.Errorf("failed to restart haproxy: %v", err)
				}
			}
		}
	}

	return nil
}
