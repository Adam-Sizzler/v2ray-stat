package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"

	"v2ray-stat/node/config"
	"v2ray-stat/node/lua"
	"v2ray-stat/node/proto"

	"github.com/google/uuid"
)

// Генерация случайного пароля (для trojan)
func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b), nil
}

// AddUser добавляет пользователя на ноду и возвращает сгенерированный credential
func (s *NodeServer) AddUser(ctx context.Context, req *proto.AddUserRequest) (*proto.AddUserResponse, error) {
	user := req.User
	inboundTag := req.InboundTag

	// Определяем протокол на основе конфигурации (примерная логика)
	var protocol string
	switch s.Cfg.V2rayStat.Type {
	case "xray":
		data, err := os.ReadFile(s.Cfg.Core.Config)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Xray config", "error", err)
			return &proto.AddUserResponse{Error: err.Error()}, nil
		}
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			s.Cfg.Logger.Error("Failed to parse Xray config", "error", err)
			return &proto.AddUserResponse{Error: err.Error()}, nil
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
			return &proto.AddUserResponse{Error: err.Error()}, nil
		}
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			s.Cfg.Logger.Error("Failed to parse Singbox config", "error", err)
			return &proto.AddUserResponse{Error: err.Error()}, nil
		}
		for _, inbound := range cfgSingbox.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Type
				break
			}
		}
	default:
		return &proto.AddUserResponse{Error: "Unsupported core type"}, nil
	}

	// Генерация credential в зависимости от протокола
	var credential string
	switch protocol {
	case "vless":
		credential = uuid.New().String()
	case "trojan":
		var err error
		credential, err = generateRandomPassword(16)
		if err != nil {
			return &proto.AddUserResponse{Error: err.Error()}, nil
		}
	default:
		return &proto.AddUserResponse{Error: fmt.Sprintf("Unsupported protocol: %s", protocol)}, nil
	}

	// Добавление пользователя в конфигурацию (логика зависит от ядра)
	err := AddUserToConfig(s.Cfg, user, credential, inboundTag)
	if err != nil {
		s.Cfg.Logger.Error("Failed to add user to config", "error", err)
		return &proto.AddUserResponse{Error: err.Error()}, nil
	}

	s.Cfg.Logger.Info("User added successfully", "user", user, "inbound_tag", inboundTag, "credential", credential)
	return &proto.AddUserResponse{Credential: credential}, nil
}

// AddUserToConfig adds a user to the configuration file.
func AddUserToConfig(cfg *config.NodeConfig, user, credential, inboundTag string) error {
	cfg.Logger.Debug("Starting user addition to configuration", "user", user, "inboundTag", inboundTag)
	configPath := cfg.Core.Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		cfg.Logger.Error("Failed to read config.json", "path", configPath, "error", err)
		return fmt.Errorf("failed to read config.json: %v", err)
	}

	proxyType := cfg.V2rayStat.Type
	var configData any
	var protocol string

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
				protocol = inbound.Protocol
				for _, client := range inbound.Settings.Clients {
					if protocol == "vless" && client.ID == credential {
						cfg.Logger.Warn("User with this ID already exists", "credential", credential)
						return fmt.Errorf("user with this id already exists")
					} else if protocol == "trojan" && client.Password == credential {
						cfg.Logger.Warn("User with this password already exists", "credential", credential)
						return fmt.Errorf("user with this password already exists")
					}
				}
				newClient := config.XrayClient{Email: user}
				switch protocol {
				case "vless":
					newClient.ID = credential
				case "trojan":
					newClient.Password = credential
				}
				cfgXray.Inbounds[i].Settings.Clients = append(cfgXray.Inbounds[i].Settings.Clients, newClient)
				found = true
				break
			}
		}
		if !found {
			cfg.Logger.Warn("Inbound not found", "inboundTag", inboundTag)
			return fmt.Errorf("inbound with tag %s not found", inboundTag)
		}
		configData = cfgXray

	case "singbox":
		var cfgSingBox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingBox); err != nil {
			cfg.Logger.Error("Failed to parse JSON", "error", err)
			return fmt.Errorf("failed to parse JSON: %v", err)
		}

		found := false
		for i, inbound := range cfgSingBox.Inbounds {
			if inbound.Tag == inboundTag {
				protocol = inbound.Type
				for _, user := range inbound.Users {
					if protocol == "vless" && user.UUID == credential {
						cfg.Logger.Warn("User with this UUID already exists", "credential", credential)
						return fmt.Errorf("user with this uuid already exists")
					} else if protocol == "trojan" && user.Password == credential {
						cfg.Logger.Warn("User with this password already exists", "credential", credential)
						return fmt.Errorf("user with this password already exists")
					}
				}
				newUser := config.SingboxClient{Name: user}
				switch protocol {
				case "vless":
					newUser.UUID = credential
				case "trojan":
					newUser.Password = credential
				}
				cfgSingBox.Inbounds[i].Users = append(cfgSingBox.Inbounds[i].Users, newUser)
				found = true
				break
			}
		}
		if !found {
			cfg.Logger.Warn("Inbound not found", "inboundTag", inboundTag)
			return fmt.Errorf("inbound with tag %s not found", inboundTag)
		}
		configData = cfgSingBox

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

	cfg.Logger.Debug("User added to configuration", "user", user, "inboundTag", inboundTag)

	if cfg.Features["auth_lua"] {
		cfg.Logger.Debug("Adding user to auth.lua", "user", user)
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

	return nil
}
