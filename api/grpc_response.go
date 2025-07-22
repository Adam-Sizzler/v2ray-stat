package api

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"v2ray-stat/config"

	statsSingbox "github.com/v2ray/v2ray-core/app/stats/command"
	statsXray "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Stat represents a single statistic entry.
type Stat struct {
	Name  string
	Value string
}

// ApiResponse contains the collected statistics.
type ApiResponse struct {
	Stat []Stat
}

// GetApiResponse retrieves statistics from the gRPC server for Xray or Singbox.
func GetApiResponse(cfg *config.Config) (*ApiResponse, error) {
	cfg.Logger.Debug("Connecting to gRPC server", "address", "127.0.0.1:9953")
	clientConn, err := grpc.NewClient("127.0.0.1:9953", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cfg.Logger.Error("Failed to connect to gRPC server", "error", err)
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}
	defer clientConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stats []Stat

	switch cfg.V2rayStat.Type {
	case "xray":
		cfg.Logger.Debug("Executing gRPC request for Xray")
		client := statsXray.NewStatsServiceClient(clientConn)
		req := &statsXray.QueryStatsRequest{
			Pattern: "",
		}
		xrayResp, err := client.QueryStats(ctx, req)
		if err != nil {
			cfg.Logger.Error("Failed to execute gRPC request for Xray", "error", err)
			return nil, fmt.Errorf("failed to execute gRPC request for Xray: %w", err)
		}

		for _, s := range xrayResp.GetStat() {
			cfg.Logger.Trace("Processing Xray stat", "name", s.GetName(), "value", s.GetValue())
			stats = append(stats, Stat{
				Name:  s.GetName(),
				Value: strconv.FormatInt(s.GetValue(), 10),
			})
		}
		cfg.Logger.Trace("Retrieved Xray stats", "count", len(xrayResp.GetStat()))

	case "singbox":
		cfg.Logger.Debug("Executing gRPC request for Singbox")
		client := statsSingbox.NewStatsServiceClient(clientConn)
		req := &statsSingbox.QueryStatsRequest{
			Pattern: "",
		}
		singboxResp, err := client.QueryStats(ctx, req)
		if err != nil {
			cfg.Logger.Error("Failed to execute gRPC request for Singbox", "error", err)
			return nil, fmt.Errorf("failed to execute gRPC request for Singbox: %w", err)
		}
		for _, s := range singboxResp.GetStat() {
			cfg.Logger.Trace("Processing Singbox stat", "name", s.GetName(), "value", s.GetValue())
			stats = append(stats, Stat{
				Name:  s.GetName(),
				Value: strconv.FormatInt(s.GetValue(), 10),
			})
		}
		cfg.Logger.Debug("Retrieved Singbox stats", "count", len(singboxResp.GetStat()))
	}

	return &ApiResponse{Stat: stats}, nil
}
