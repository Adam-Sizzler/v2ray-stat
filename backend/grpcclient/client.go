package grpcclient

import (
	"context"
	"io"
	"sync"
	"time"
	"v2ray-stat/backend/api"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/subscription/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

var (
	grpcConn *grpc.ClientConn
	stream   proto.SubscriptionService_DataStreamClient
	mu       sync.Mutex
)

func StartGrpcClient(ctx context.Context, cfg *config.Config, manager *manager.DatabaseManager, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		mu.Lock()
		if grpcConn != nil {
			grpcConn.Close()
			grpcConn = nil
			stream = nil
			cfg.Logger.Debug("gRPC connection closed in StartGrpcClient")
		}
		mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			cfg.Logger.Debug("Stopping gRPC client due to context cancellation")
			return
		default:
			connectAndHandle(ctx, cfg, manager)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				cfg.Logger.Info("Reconnecting to gRPC server")
				continue
			}
		}
	}
}

func connectAndHandle(ctx context.Context, cfg *config.Config, manager *manager.DatabaseManager) {
	localCtx, localCancel := context.WithCancel(ctx)
	defer localCancel()

	mu.Lock()
	if grpcConn != nil {
		grpcConn.Close()
		grpcConn = nil
		stream = nil
	}
	mu.Unlock()

	conn, err := grpc.Dial(
		cfg.V2rayStat.Subscription.Address+":"+cfg.V2rayStat.Subscription.Port,
		grpc.WithInsecure(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		cfg.Logger.Error("Failed to dial gRPC", "error", err)
		return
	}

	client := proto.NewSubscriptionServiceClient(conn)

	strm, err := client.DataStream(ctx)
	if err != nil {
		cfg.Logger.Error("Failed to open stream", "error", err)
		conn.Close()
		return
	}

	mu.Lock()
	grpcConn = conn
	stream = strm
	mu.Unlock()

	cfg.Logger.Info("gRPC stream connected to subscription")

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-localCtx.Done():
				cfg.Logger.Debug("Stopping heartbeat ticker due to local context cancellation")
				return
			case <-ticker.C:
				mu.Lock()
				if stream != nil {
					if err := stream.Send(&proto.Response{IsHeartbeat: true}); err != nil {
						cfg.Logger.Error("Failed to send heartbeat", "error", err)
						mu.Unlock()
						localCancel() // Прерываем при ошибке отправки
						return
					}
					cfg.Logger.Debug("Sent heartbeat")
				}
				mu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			if grpcConn != nil {
				grpcConn.Close()
				grpcConn = nil
				stream = nil
				cfg.Logger.Debug("gRPC connection closed due to context cancellation")
			}
			mu.Unlock()
			return
		case <-localCtx.Done():
			cfg.Logger.Info("gRPC stream closed due to local context cancellation")
			mu.Lock()
			if grpcConn != nil {
				grpcConn.Close()
				grpcConn = nil
				stream = nil
			}
			mu.Unlock()
			return
		default:
			req, err := strm.Recv()
			if err == io.EOF || status.Code(err) == codes.Canceled {
				cfg.Logger.Info("gRPC stream closed")
				mu.Lock()
				if grpcConn != nil {
					grpcConn.Close()
					grpcConn = nil
					stream = nil
				}
				mu.Unlock()
				return
			}
			if err != nil {
				cfg.Logger.Error("gRPC recv error", "error", err)
				mu.Lock()
				if grpcConn != nil {
					grpcConn.Close()
					grpcConn = nil
					stream = nil
				}
				mu.Unlock()
				return
			}

			nodeUsers, err := api.QueryUsers(ctx, manager, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to query users", "error", err)
				continue
			}

			resp := &proto.Response{
				RequestId: req.RequestId,
				NodeUsers: make([]*proto.NodeUsers, len(nodeUsers)),
			}

			for i, nu := range nodeUsers {
				users := make([]*proto.User, len(nu.Users))
				for j, u := range nu.Users {
					inbounds := make([]*proto.IdEntry, len(u.Inbounds))
					for k, ie := range u.Inbounds {
						inbounds[k] = &proto.IdEntry{InboundTag: ie.InboundTag, Id: ie.Id}
					}
					users[j] = &proto.User{
						User:       u.User,
						Inbounds:   inbounds,
						Rate:       u.Rate,
						Enabled:    u.Enabled,
						Created:    u.Created,
						SubEnd:     u.SubEnd,
						Renew:      int32(u.Renew),
						LimIp:      int32(u.LimIp),
						Ips:        u.Ips,
						Uplink:     u.Uplink,
						Downlink:   u.Downlink,
						TrafficCap: u.TrafficCap,
					}
				}
				resp.NodeUsers[i] = &proto.NodeUsers{
					Node:    nu.Node,
					Address: nu.Address,
					Port:    nu.Port,
					Users:   users,
				}
			}

			if err := strm.Send(resp); err != nil {
				cfg.Logger.Error("Failed to send response", "error", err)
				mu.Lock()
				if grpcConn != nil {
					grpcConn.Close()
					grpcConn = nil
					stream = nil
				}
				mu.Unlock()
				return
			}
		}
	}
}
