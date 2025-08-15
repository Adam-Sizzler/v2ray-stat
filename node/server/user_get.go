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

func (s *NodeServer) GetUsers(ctx context.Context, req *proto.GetUsersRequest) (*proto.GetUsersResponse, error) {
	userMap := make(map[string]struct {
		uis     []*proto.UUIDInbound
		enabled bool
	})

	mainConfigPath := s.Cfg.Core.Config
	disabledUsersPath := filepath.Join(s.Cfg.Core.Dir, ".disabled_users")

	switch s.Cfg.V2rayStat.Type {
	case "xray":
		// Read main config
		data, err := os.ReadFile(mainConfigPath)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Xray main config", "error", err)
			return nil, err
		}
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			s.Cfg.Logger.Error("Failed to parse Xray main config", "error", err)
			return nil, err
		}
		for _, inbound := range cfgXray.Inbounds {
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

		// Read disabled users file if exists
		disabledData, err := os.ReadFile(disabledUsersPath)
		if err == nil {
			var disabledCfg config.DisabledUsersConfigXray
			if err := json.Unmarshal(disabledData, &disabledCfg); err != nil {
				s.Cfg.Logger.Error("Failed to parse Xray disabled users file", "error", err)
				return nil, err
			}
			for _, inbound := range disabledCfg.Inbounds {
				for _, client := range inbound.Settings.Clients {
					ui := &proto.UUIDInbound{Uuid: client.ID, InboundTag: inbound.Tag}
					if val, exists := userMap[client.Email]; exists {
						val.uis = append(val.uis, ui)
						val.enabled = false // Set enabled to false if in disabled
						userMap[client.Email] = val
					} else {
						userMap[client.Email] = struct {
							uis     []*proto.UUIDInbound
							enabled bool
						}{uis: []*proto.UUIDInbound{ui}, enabled: false}
					}
				}
			}
		} else if !os.IsNotExist(err) {
			s.Cfg.Logger.Error("Failed to read Xray disabled users file", "error", err)
			return nil, err
		}

	case "singbox":
		// Read main config
		data, err := os.ReadFile(mainConfigPath)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Singbox main config", "error", err)
			return nil, err
		}
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			s.Cfg.Logger.Error("Failed to parse Singbox main config", "error", err)
			return nil, err
		}
		for _, inbound := range cfgSingbox.Inbounds {
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

		// Read disabled users file if exists
		disabledData, err := os.ReadFile(disabledUsersPath)
		if err == nil {
			var disabledCfg config.DisabledUsersConfigSingbox
			if err := json.Unmarshal(disabledData, &disabledCfg); err != nil {
				s.Cfg.Logger.Error("Failed to parse Singbox disabled users file", "error", err)
				return nil, err
			}
			for _, inbound := range disabledCfg.Inbounds {
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
						val.enabled = false // Set enabled to false if in disabled
						userMap[user.Name] = val
					} else {
						userMap[user.Name] = struct {
							uis     []*proto.UUIDInbound
							enabled bool
						}{uis: []*proto.UUIDInbound{ui}, enabled: false}
					}
				}
			}
		} else if !os.IsNotExist(err) {
			s.Cfg.Logger.Error("Failed to read Singbox disabled users file", "error", err)
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported core type: %s", s.Cfg.V2rayStat.Type)
	}

	resp := &proto.GetUsersResponse{}
	for username, val := range userMap {
		user := &proto.User{
			Username:     username,
			UuidInbounds: val.uis,
			Enabled:      val.enabled,
		}
		resp.Users = append(resp.Users, user)
	}
	return resp, nil
}

// NewNodeServer создаёт новый сервер ноды.
func NewNodeServer(cfg *config.NodeConfig) (*NodeServer, error) {
	processor, err := logprocessor.NewLogProcessor(cfg)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	s.Cfg.Logger.Debug("Returning log data via gRPC", "users", len(response.UserLogData))
	return response, nil
}
