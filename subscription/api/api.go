package api

import (
	"fmt"

	"v2ray-stat/subscription/config"
	"v2ray-stat/subscription/grpcserver"
	"v2ray-stat/subscription/proto" // Исправлен путь
)

// UserID represents a single user ID entry from the API.
type UserID struct {
	NodeName   string `json:"node_name"`
	User       string `json:"user"`
	ID         string `json:"id"`
	SubEnd     int64  `json:"sub_end"`
	InboundTag string `json:"inbound_tag"`
	Uplink     int64  `json:"uplink"`
	Downlink   int64  `json:"downlink"`
	TrafficCap int64  `json:"traffic_cap"`
}

// NodeUser represents a user within a node in the API response.
type NodeUser struct {
	User       string    `json:"user"`
	Inbounds   []Inbound `json:"inbounds"`
	Uplink     int64     `json:"uplink"`
	Downlink   int64     `json:"downlink"`
	SubEnd     int64     `json:"sub_end"`
	TrafficCap int64     `json:"traffic_cap"`
}

// Inbound represents an inbound configuration for a user.
type Inbound struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
}

// Node represents a node in the API response.
type Node struct {
	NodeName string     `json:"node_name"`
	Address  string     `json:"address"`
	Users    []NodeUser `json:"users"`
}

// BackendResponse represents the structure of /api/v1/users response.
type BackendResponse []Node

func GetUserIDs(cfg *config.Config, user string) ([]UserID, error) {
	cfg.Logger.Trace("Querying via gRPC for user IDs")

	resp, err := grpcserver.SendRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to get gRPC response: %w", err)
	}

	return processGrpcResponse(resp), nil
}

func processGrpcResponse(resp *proto.Response) []UserID {
	var userIDs []UserID
	for _, nu := range resp.NodeUsers {
		for _, u := range nu.Users {
			for _, inbound := range u.Inbounds {
				userIDs = append(userIDs, UserID{
					NodeName:   nu.Node,
					User:       u.User,
					ID:         inbound.Id,
					InboundTag: inbound.InboundTag,
					SubEnd:     u.SubEnd,
					Uplink:     u.Uplink,
					Downlink:   u.Downlink,
					TrafficCap: u.TrafficCap,
				})
			}
		}
	}
	return userIDs
}
