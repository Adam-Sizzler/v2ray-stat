package server

import (
	"context"
	"fmt"

	"v2ray-stat/node/api"
	"v2ray-stat/node/config"
	"v2ray-stat/node/logprocessor"
	"v2ray-stat/node/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodeServer implements the NodeService gRPC service.
type NodeServer struct {
	proto.UnimplementedNodeServiceServer
	Cfg          *config.NodeConfig
	logProcessor *logprocessor.LogProcessor
}

// GetApiStats retrieves API statistics from the node.
func (s *NodeServer) GetApiStats(ctx context.Context, req *proto.GetApiStatsRequest) (*proto.GetApiStatsResponse, error) {
	s.Cfg.Logger.Debug("Received GetApiStats request")
	apiData, err := api.GetApiResponse(s.Cfg)
	if err != nil {
		s.Cfg.Logger.Error("Failed to get API response", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get API response: %v", err)
	}

	response := &proto.GetApiStatsResponse{}
	for _, stat := range apiData.Stat {
		response.Stats = append(response.Stats, &proto.Stat{
			Name:  stat.Name,
			Value: stat.Value,
		})
	}

	s.Cfg.Logger.Debug("Returning API stats", "stats_count", len(response.Stats))
	return response, nil
}

// GetLogData retrieves processed log data from the node.
func (s *NodeServer) GetLogData(ctx context.Context, req *proto.GetLogDataRequest) (*proto.GetLogDataResponse, error) {
	response, err := s.logProcessor.ReadNewLines()
	if err != nil {
		s.Cfg.Logger.Error("Failed to read log data", "error", err)
		return nil, status.Errorf(codes.Internal, "read log data: %v", err)
	}
	s.Cfg.Logger.Debug("Returning log data via gRPC", "users", len(response.UserLogData))
	return response, nil
}

// NewNodeServer creates a new NodeServer instance.
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
