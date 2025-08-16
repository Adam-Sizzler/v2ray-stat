package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"v2ray-stat/node/api"
	"v2ray-stat/node/config"
	"v2ray-stat/node/logprocessor"
	"v2ray-stat/node/proto"
)

type NodeServer struct {
	proto.UnimplementedNodeServiceServer
	Cfg          *config.NodeConfig
	logProcessor *logprocessor.LogProcessor
}

// ключ уникальности
func makeUserKey(username, uuid, tag string) string {
	return fmt.Sprintf("%s|%s|%s", username, uuid, tag)
}

// processUsers обрабатывает пользователей из конфигурации и добавляет их в userMap.
func processUsers[T any](data []byte, userMap map[string]*proto.User, cfg *config.NodeConfig, isDisabled bool, process func(cfg T, userMap map[string]*proto.User)) error {
	var config T
	if err := json.Unmarshal(data, &config); err != nil {
		cfg.Logger.Error("Failed to parse config", "error", err)
		return err
	}
	process(config, userMap)
	return nil
}

func (s *NodeServer) GetUsers(ctx context.Context, req *proto.GetUsersRequest) (*proto.GetUsersResponse, error) {
	userMap := make(map[string]*proto.User)

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
		err = processUsers(data, userMap, s.Cfg, false, func(cfg config.ConfigXray, userMap map[string]*proto.User) {
			for _, inbound := range cfg.Inbounds {
				for _, client := range inbound.Settings.Clients {
					ui := &proto.UUIDInbound{Uuid: client.ID, InboundTag: inbound.Tag}
					key := makeUserKey(client.Email, client.ID, inbound.Tag)

					userMap[key] = &proto.User{
						Username:     client.Email,
						UuidInbounds: []*proto.UUIDInbound{ui},
						Enabled:      true,
					}
				}
			}
		})
		if err != nil {
			return nil, fmt.Errorf("process Xray main config: %w", err)
		}

		// Process disabled users
		disabledData, err := os.ReadFile(disabledUsersPath)
		if err == nil && len(disabledData) > 0 {
			err = processUsers(disabledData, userMap, s.Cfg, true, func(cfg config.DisabledUsersConfigXray, userMap map[string]*proto.User) {
				for _, inbound := range cfg.Inbounds {
					for _, client := range inbound.Settings.Clients {
						ui := &proto.UUIDInbound{Uuid: client.ID, InboundTag: inbound.Tag}
						key := makeUserKey(client.Email, client.ID, inbound.Tag)

						userMap[key] = &proto.User{
							Username:     client.Email,
							UuidInbounds: []*proto.UUIDInbound{ui},
							Enabled:      false,
						}
					}
				}
			})
			if err != nil {
				return nil, fmt.Errorf("process Xray disabled users: %w", err)
			}
		}

	case "singbox":
		// Process main config
		data, err := os.ReadFile(mainConfigPath)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Singbox main config", "path", mainConfigPath, "error", err)
			return nil, fmt.Errorf("read main config: %w", err)
		}
		err = processUsers(data, userMap, s.Cfg, false, func(cfg config.ConfigSingbox, userMap map[string]*proto.User) {
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
					key := makeUserKey(user.Name, uuid, inbound.Tag)

					userMap[key] = &proto.User{
						Username:     user.Name,
						UuidInbounds: []*proto.UUIDInbound{ui},
						Enabled:      true,
					}
				}
			}
		})
		if err != nil {
			return nil, fmt.Errorf("process Singbox main config: %w", err)
		}

		// Process disabled users
		disabledData, err := os.ReadFile(disabledUsersPath)
		if err == nil && len(disabledData) > 0 {
			err = processUsers(disabledData, userMap, s.Cfg, true, func(cfg config.DisabledUsersConfigSingbox, userMap map[string]*proto.User) {
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
						key := makeUserKey(user.Name, uuid, inbound.Tag)

						userMap[key] = &proto.User{
							Username:     user.Name,
							UuidInbounds: []*proto.UUIDInbound{ui},
							Enabled:      false,
						}
					}
				}
			})
			if err != nil {
				return nil, fmt.Errorf("process Singbox disabled users: %w", err)
			}
		}

	default:
		s.Cfg.Logger.Error("Unsupported core type", "type", s.Cfg.V2rayStat.Type)
		return nil, fmt.Errorf("unsupported core type: %s", s.Cfg.V2rayStat.Type)
	}

	resp := &proto.GetUsersResponse{
		Users: make([]*proto.User, 0, len(userMap)),
	}
	for _, user := range userMap {
		resp.Users = append(resp.Users, user)
	}
	s.Cfg.Logger.Debug("Returning users", "count", len(resp.Users))
	return resp, nil
}

func (s *NodeServer) GetApiResponse(ctx context.Context, req *proto.GetApiResponseRequest) (*proto.GetApiResponseResponse, error) {
	s.Cfg.Logger.Debug("Received GetApiResponse request")
	apiData, err := api.GetApiResponse(s.Cfg)
	if err != nil {
		s.Cfg.Logger.Error("Failed to get API response", "error", err)
		return nil, fmt.Errorf("failed to get API response: %w", err)
	}

	response := &proto.GetApiResponseResponse{}
	for _, stat := range apiData.Stat {
		response.Stats = append(response.Stats, &proto.Stat{
			Name:  stat.Name,
			Value: stat.Value,
		})
	}

	s.Cfg.Logger.Debug("Returning API response", "stats_count", len(response.Stats))
	return response, nil
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
