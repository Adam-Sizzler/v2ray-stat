package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"v2ray-stat/node/config"
	"v2ray-stat/node/logprocessor"
	"v2ray-stat/node/proto"
)

type NodeServer struct {
	proto.UnimplementedNodeServiceServer
	Cfg          *config.NodeConfig
	logProcessor *logprocessor.LogProcessor
}

// processUsers обрабатывает пользователей из конфигурации и добавляет их в userMap.
func processUsers[T any](data []byte, userMap map[string]struct {
	uis     []*proto.UUIDInbound
	enabled bool
}, cfg *config.NodeConfig, isDisabled bool, process func(cfg T, userMap map[string]struct {
	uis     []*proto.UUIDInbound
	enabled bool
})) error {
	var config T
	if err := json.Unmarshal(data, &config); err != nil {
		cfg.Logger.Error("Failed to parse config", "error", err)
		return err
	}
	process(config, userMap)
	return nil
}

func (s *NodeServer) GetUsers(ctx context.Context, req *proto.GetUsersRequest) (*proto.GetUsersResponse, error) {
	userMap := make(map[string]struct {
		uis     []*proto.UUIDInbound
		enabled bool
	})

	mainConfigPath := s.Cfg.Core.Config
	disabledUsersPath := filepath.Join(s.Cfg.Core.Dir, ".disabled_users")

	switch s.Cfg.V2rayStat.Type {
	case "xray":
		// Process main config
		data, err := os.ReadFile(mainConfigPath)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Xray main config", "path", mainConfigPath, "error", err)
			return nil, fmt.Errorf("read main config: %w", err)
		}
		err = processUsers(data, userMap, s.Cfg, false, func(cfg config.ConfigXray, userMap map[string]struct {
			uis     []*proto.UUIDInbound
			enabled bool
		}) {
			for _, inbound := range cfg.Inbounds {
				for _, client := range inbound.Settings.Clients {
					ui := &proto.UUIDInbound{Uuid: client.ID, InboundTag: inbound.Tag}
					if val, exists := userMap[client.Email]; exists {
						val.uis = append(val.uis, ui)
						userMap[client.Email] = val
					} else {
						userMap[client.Email] = struct {
							uis     []*proto.UUIDInbound
							enabled bool
						}{uis: []*proto.UUIDInbound{ui}, enabled: true}
					}
				}
			}
		})
		if err != nil {
			return nil, fmt.Errorf("process Xray main config: %w", err)
		}

		// Process disabled users
		disabledData, err := os.ReadFile(disabledUsersPath)
		if err == nil {
			if len(disabledData) == 0 {
				s.Cfg.Logger.Debug("Empty disabled users file", "path", disabledUsersPath)
			} else {
				err = processUsers(disabledData, userMap, s.Cfg, true, func(cfg config.DisabledUsersConfigXray, userMap map[string]struct {
					uis     []*proto.UUIDInbound
					enabled bool
				}) {
					for _, inbound := range cfg.Inbounds {
						for _, client := range inbound.Settings.Clients {
							ui := &proto.UUIDInbound{Uuid: client.ID, InboundTag: inbound.Tag}
							if val, exists := userMap[client.Email]; exists {
								val.uis = append(val.uis, ui)
								val.enabled = false
								userMap[client.Email] = val
							} else {
								userMap[client.Email] = struct {
									uis     []*proto.UUIDInbound
									enabled bool
								}{uis: []*proto.UUIDInbound{ui}, enabled: false}
							}
						}
					}
				})
				if err != nil {
					return nil, fmt.Errorf("process Xray disabled users: %w", err)
				}
			}
		} else if !os.IsNotExist(err) {
			s.Cfg.Logger.Error("Failed to read Xray disabled users file", "path", disabledUsersPath, "error", err)
			return nil, fmt.Errorf("read disabled users file: %w", err)
		}

	case "singbox":
		// Process main config
		data, err := os.ReadFile(mainConfigPath)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Singbox main config", "path", mainConfigPath, "error", err)
			return nil, fmt.Errorf("read main config: %w", err)
		}
		err = processUsers(data, userMap, s.Cfg, false, func(cfg config.ConfigSingbox, userMap map[string]struct {
			uis     []*proto.UUIDInbound
			enabled bool
		}) {
			for _, inbound := range cfg.Inbounds {
				for _, user := range inbound.Users {
					var uuid string
					switch inbound.Type {
					case "vless":
						uuid = user.UUID
					case "trojan":
						uuid = user.Password
					}
					ui := &proto.UUIDInbound{Uuid: uuid, InboundTag: inbound.Tag}
					if val, exists := userMap[user.Name]; exists {
						val.uis = append(val.uis, ui)
						userMap[user.Name] = val
					} else {
						userMap[user.Name] = struct {
							uis     []*proto.UUIDInbound
							enabled bool
						}{uis: []*proto.UUIDInbound{ui}, enabled: true}
					}
				}
			}
		})
		if err != nil {
			return nil, fmt.Errorf("process Singbox main config: %w", err)
		}

		// Process disabled users
		disabledData, err := os.ReadFile(disabledUsersPath)
		if err == nil {
			if len(disabledData) == 0 {
				s.Cfg.Logger.Debug("Empty disabled users file", "path", disabledUsersPath)
			} else {
				err = processUsers(disabledData, userMap, s.Cfg, true, func(cfg config.DisabledUsersConfigSingbox, userMap map[string]struct {
					uis     []*proto.UUIDInbound
					enabled bool
				}) {
					for _, inbound := range cfg.Inbounds {
						for _, user := range inbound.Users {
							var uuid string
							switch inbound.Type {
							case "vless":
								uuid = user.UUID
							case "trojan":
								uuid = user.Password
							}
							ui := &proto.UUIDInbound{Uuid: uuid, InboundTag: inbound.Tag}
							if val, exists := userMap[user.Name]; exists {
								val.uis = append(val.uis, ui)
								val.enabled = false
								userMap[user.Name] = val
							} else {
								userMap[user.Name] = struct {
									uis     []*proto.UUIDInbound
									enabled bool
								}{uis: []*proto.UUIDInbound{ui}, enabled: false}
							}
						}
					}
				})
				if err != nil {
					return nil, fmt.Errorf("process Singbox disabled users: %w", err)
				}
			}
		} else if !os.IsNotExist(err) {
			s.Cfg.Logger.Error("Failed to read Singbox disabled users file", "path", disabledUsersPath, "error", err)
			return nil, fmt.Errorf("read disabled users file: %w", err)
		}

	default:
		s.Cfg.Logger.Error("Unsupported core type", "type", s.Cfg.V2rayStat.Type)
		return nil, fmt.Errorf("unsupported core type: %s", s.Cfg.V2rayStat.Type)
	}

	resp := &proto.GetUsersResponse{
		Users: make([]*proto.User, 0, len(userMap)),
	}
	for username, val := range userMap {
		resp.Users = append(resp.Users, &proto.User{
			Username:     username,
			UuidInbounds: val.uis,
			Enabled:      val.enabled,
		})
	}
	s.Cfg.Logger.Debug("Returning users", "count", len(resp.Users))
	return resp, nil
}

// NewNodeServer создаёт новый сервер ноды.
func NewNodeServer(cfg *config.NodeConfig) (*NodeServer, error) {
	processor, err := logprocessor.NewLogProcessor(cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create log processor", "error", err)
		return nil, fmt.Errorf("create log processor: %w", err)
	}
	server := &NodeServer{
		Cfg:          cfg,
		logProcessor: processor,
	}
	return server, nil
}

// GetLogData возвращает обработанные данные логов.
func (s *NodeServer) GetLogData(ctx context.Context, req *proto.GetLogDataRequest) (*proto.GetLogDataResponse, error) {
	response, err := s.logProcessor.ReadNewLines()
	if err != nil {
		s.Cfg.Logger.Error("Failed to read log data", "error", err)
		return nil, fmt.Errorf("read log data: %w", err)
	}
	s.Cfg.Logger.Debug("Returning log data via gRPC", "users", len(response.UserLogData))
	return response, nil
}
