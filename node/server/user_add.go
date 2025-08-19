package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"

	"google.golang.org/genproto/googleapis/rpc/status" // Import for google.rpc.Status
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status" // Alias to avoid conflict

	"github.com/google/uuid"

	"v2ray-stat/node/common"
	"v2ray-stat/node/config"
	"v2ray-stat/node/lua"
	"v2ray-stat/node/proto"
)

// generateRandomPassword generates a random password of the specified length.
func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b), nil
}

// AddUsers adds one or more users to the node.
func (s *NodeServer) AddUsers(ctx context.Context, req *proto.AddUsersRequest) (*proto.OperationResponse, error) {
	usernames := req.Usernames
	inboundTag := req.InboundTag

	// Determine protocol based on configuration
	var protocol string
	switch s.Cfg.V2rayStat.Type {
	case "xray":
		data, err := os.ReadFile(s.Cfg.Core.Config)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Xray config", "error", err)
			return nil, grpcstatus.Errorf(codes.Internal, "failed to read Xray config: %v", err)
		}
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			s.Cfg.Logger.Error("Failed to parse Xray config", "error", err)
			return nil, grpcstatus.Errorf(codes.Internal, "failed to parse Xray config: %v", err)
		}
		for _, inbound := range cfgXray.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Protocol
				break
			}
		}
	case "singbox":
		data, err := os.ReadFile(s.Cfg.Core.Config)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Singbox config", "error", err)
			return nil, grpcstatus.Errorf(codes.Internal, "failed to read Singbox config: %v", err)
		}
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			s.Cfg.Logger.Error("Failed to parse Singbox config", "error", err)
			return nil, grpcstatus.Errorf(codes.Internal, "failed to parse Singbox config: %v", err)
		}
		for _, inbound := range cfgSingbox.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Type
				break
			}
		}
	default:
		return nil, grpcstatus.Errorf(codes.InvalidArgument, "unsupported core type: %s", s.Cfg.V2rayStat.Type)
	}

	// Generate credentials for each user
	credentials := make(map[string]string)
	for _, user := range usernames {
		var credential string
		var err error
		switch protocol {
		case "vless":
			credential = uuid.New().String()
		case "trojan":
			credential, err = generateRandomPassword(16)
			if err != nil {
				return nil, grpcstatus.Errorf(codes.Internal, "failed to generate password for %s: %v", user, err)
			}
		default:
			return nil, grpcstatus.Errorf(codes.InvalidArgument, "unsupported protocol: %s", protocol)
		}
		credentials[user] = credential
	}

	// Add users to configuration
	err := AddUsersToConfig(s.Cfg, credentials, inboundTag)
	if err != nil {
		s.Cfg.Logger.Error("Failed to add users to config", "error", err)
		return nil, grpcstatus.Errorf(codes.FailedPrecondition, "failed to add users: %v", err)
	}

	s.Cfg.Logger.Info("Users added successfully", "users", usernames, "inbound_tag", inboundTag)
	return &proto.OperationResponse{
		Status:    &status.Status{Code: int32(codes.OK), Message: "success"},
		Usernames: usernames,
	}, nil
}

// AddUsersToConfig adds multiple users to the configuration file in one operation.
func AddUsersToConfig(cfg *config.NodeConfig, credentials map[string]string, inboundTag string) error {
	cfg.Logger.Debug("Starting users addition to configuration", "users", credentials, "inboundTag", inboundTag)
	configPath := cfg.Core.Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to read config.json: %v", err)
	}

	proxyType := cfg.V2rayStat.Type
	var configData any
	var protocol string
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
				protocol = inbound.Protocol
				existingCredentials := make(map[string]bool)
				for _, client := range inbound.Settings.Clients {
					if protocol == "vless" {
						existingCredentials[client.ID] = true
					} else if protocol == "trojan" {
						existingCredentials[client.Password] = true
					}
					if _, exists := credentials[client.Email]; exists {
						cfg.Logger.Warn("User already exists", "user", client.Email)
						return fmt.Errorf("user %s already exists", client.Email)
					}
				}
				for user, credential := range credentials {
					if existingCredentials[credential] {
						cfg.Logger.Warn("Credential already exists", "credential", credential)
						return fmt.Errorf("credential %s already exists", credential)
					}
					newClient := config.XrayClient{Email: user}
					switch protocol {
					case "vless":
						newClient.ID = credential
					case "trojan":
						newClient.Password = credential
					}
					cfgXray.Inbounds[i].Settings.Clients = append(cfgXray.Inbounds[i].Settings.Clients, newClient)
				}
				found = true
				break
			}
		}
		configData = cfgXray

	case "singbox":
		var cfgSingBox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingBox); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		for i, inbound := range cfgSingBox.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Type
				existingCredentials := make(map[string]bool)
				for _, user := range inbound.Users {
					if protocol == "vless" {
						existingCredentials[user.UUID] = true
					} else if protocol == "trojan" {
						existingCredentials[user.Password] = true
					}
					if _, exists := credentials[user.Name]; exists {
						cfg.Logger.Warn("User already exists", "user", user.Name)
						return fmt.Errorf("user %s already exists", user.Name)
					}
				}
				for user, credential := range credentials {
					if existingCredentials[credential] {
						cfg.Logger.Warn("Credential already exists", "credential", credential)
						return fmt.Errorf("credential %s already exists", credential)
					}
					newUser := config.SingboxClient{Name: user}
					switch protocol {
					case "vless":
						newUser.UUID = credential
					case "trojan":
						newUser.Password = credential
					}
					cfgSingBox.Inbounds[i].Users = append(cfgSingBox.Inbounds[i].Users, newUser)
				}
				found = true
				break
			}
		}
		configData = cfgSingBox

	default:
		cfg.Logger.Warn("Unsupported core type", "proxyType", proxyType)
		return fmt.Errorf("unsupported core type: %s", proxyType)
	}

	if !found {
		cfg.Logger.Warn("Inbound not found", "inboundTag", inboundTag)
		return fmt.Errorf("inbound with tag %s not found", inboundTag)
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

	cfg.Logger.Debug("Users added to configuration", "users", credentials, "inboundTag", inboundTag)

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
		cfg.Logger.Debug("Adding users to auth.lua", "users", credentials)
		for user, credential := range credentials {
			var credentialToAdd string
			if protocol == "trojan" {
				hash := sha256.Sum224([]byte(credential))
				credentialToAdd = hex.EncodeToString(hash[:])
				cfg.Logger.Trace("Hashed credential for trojan", "credential", credentialToAdd)
			} else {
				credentialToAdd = credential
				cfg.Logger.Trace("Using raw credential for vless", "credential", credentialToAdd)
			}
			if err := lua.AddUserToAuthLua(cfg, user, credentialToAdd); err != nil {
				cfg.Logger.Error("Failed to add user to auth.lua", "user", user, "error", err)
			} else {
				cfg.Logger.Debug("User added to auth.lua", "user", user)
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
