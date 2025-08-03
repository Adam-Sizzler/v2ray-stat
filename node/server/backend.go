package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"v2ray-stat/node/config"
	"v2ray-stat/node/proto"
)

type NodeServer struct {
	proto.UnimplementedNodeServiceServer
	Cfg *config.NodeConfig
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
