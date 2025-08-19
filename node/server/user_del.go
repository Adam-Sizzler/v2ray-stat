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

	// protobuf
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status" // grpc
)

// DeleteUsers deletes one or more users from the node.
func (s *NodeServer) DeleteUsers(ctx context.Context, req *proto.DeleteUsersRequest) (*proto.OperationResponse, error) {
	err := DeleteUsersFromConfig(s.Cfg, req.Usernames, req.InboundTag)
	if err != nil {
		s.Cfg.Logger.Error("Failed to delete users from config", "error", err)
		return nil, grpcstatus.Errorf(codes.FailedPrecondition, "failed to delete users: %v", err)
	}

	s.Cfg.Logger.Info("Users deleted successfully", "users", req.Usernames, "inbound_tag", req.InboundTag)
	return &proto.OperationResponse{
		Status:    &status.Status{Code: int32(codes.OK), Message: "success"},
		Usernames: req.Usernames,
	}, nil
}

// DeleteUsersFromConfig removes one or more users from the configuration file in one operation.
func DeleteUsersFromConfig(cfg *config.NodeConfig, usernames []string, inboundTag string) error {
	cfg.Logger.Debug("Starting users deletion from configuration", "users", usernames, "inboundTag", inboundTag)
	configPath := cfg.Core.Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to read config.json: %v", err)
	}

	proxyType := cfg.V2rayStat.Type
	var configData any
	found := false

	switch proxyType {
	case "xray":
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		for i, inbound := range cfgXray.Inbounds {
			if inbound.Tag == inboundTag {
				updatedClients := make([]config.XrayClient, 0, len(inbound.Settings.Clients))
				userSet := make(map[string]bool)
				for _, u := range usernames {
					userSet[u] = true
				}
				for _, client := range inbound.Settings.Clients {
					if !userSet[client.Email] {
						updatedClients = append(updatedClients, client)
					} else {
						found = true
					}
				}
				if len(updatedClients) < len(inbound.Settings.Clients) {
					cfgXray.Inbounds[i].Settings.Clients = updatedClients
					found = true
				}
			}
		}
		configData = cfgXray

	case "singbox":
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		for i, inbound := range cfgSingbox.Inbounds {
			if inbound.Tag == inboundTag {
				updatedUsers := make([]config.SingboxClient, 0, len(inbound.Users))
				userSet := make(map[string]bool)
				for _, u := range usernames {
					userSet[u] = true
				}
				for _, u := range inbound.Users {
					if !userSet[u.Name] {
						updatedUsers = append(updatedUsers, u)
					} else {
						found = true
					}
				}
				if len(updatedUsers) < len(inbound.Users) {
					cfgSingbox.Inbounds[i].Users = updatedUsers
					found = true
				}
			}
		}
		configData = cfgSingbox

	default:
		cfg.Logger.Warn("Unsupported core type", "proxyType", proxyType)
		return fmt.Errorf("unsupported core type: %s", proxyType)
	}

	if !found {
		cfg.Logger.Warn("Inbound or some users not found", "inboundTag", inboundTag, "users", usernames)
		return fmt.Errorf("inbound with tag %s or some users not found", inboundTag)
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

	cfg.Logger.Debug("Users removed from configuration", "users", usernames, "inboundTag", inboundTag)

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
		cfg.Logger.Debug("Deleting users from auth.lua", "users", usernames)
		for _, user := range usernames {
			if err := lua.DeleteUserFromAuthLua(cfg, user); err != nil {
				cfg.Logger.Error("Failed to delete user from auth.lua", "user", user, "error", err)
			} else {
				cfg.Logger.Debug("User removed from auth.lua", "user", user)
			}
		}
		if cfg.Features["restart"] {
			if err := common.RestartService("haproxy", cfg); err != nil {
				return fmt.Errorf("failed to restart haproxy: %v", err)
			}
		}
	}

	return nil
}
