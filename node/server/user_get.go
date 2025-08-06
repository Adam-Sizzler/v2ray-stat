package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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

func (s *NodeServer) GetUsers(ctx context.Context, req *proto.GetUsersRequest) (*proto.GetUsersResponse, error) {
	userMap := make(map[string][]*proto.UUIDInbound)

	switch s.Cfg.V2rayStat.Type {
	case "xray":
		data, err := os.ReadFile(s.Cfg.Core.Config)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Xray config", "error", err)
			return nil, err
		}
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			s.Cfg.Logger.Error("Failed to parse Xray config", "error", err)
			return nil, err
		}
		for _, inbound := range cfgXray.Inbounds {
			for _, client := range inbound.Settings.Clients {
				ui := &proto.UUIDInbound{Uuid: client.ID, InboundTag: inbound.Tag}
				userMap[client.Email] = append(userMap[client.Email], ui)
			}
		}
	case "singbox":
		data, err := os.ReadFile(s.Cfg.Core.Config)
		if err != nil {
			s.Cfg.Logger.Error("Failed to read Singbox config", "error", err)
			return nil, err
		}
		var cfgSingbox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingbox); err != nil {
			s.Cfg.Logger.Error("Failed to parse Singbox config", "error", err)
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
				userMap[user.Name] = append(userMap[user.Name], ui)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported core type: %s", s.Cfg.V2rayStat.Type)
	}

	resp := &proto.GetUsersResponse{}
	for username, uis := range userMap {
		user := &proto.User{
			Username:     username,
			UuidInbounds: uis,
		}
		resp.Users = append(resp.Users, user)
	}
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
