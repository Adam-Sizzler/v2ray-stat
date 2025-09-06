package grpcserver

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"v2ray-stat/subscription/config"
	"v2ray-stat/subscription/proto"
)

type server struct {
	proto.UnimplementedSubscriptionServiceServer
}

var (
	currentStream proto.SubscriptionService_DataStreamServer
	waiting       = make(map[string]chan *proto.Response)
	mu            sync.Mutex
	cfg           *config.Config
	lastResponse  *proto.Response
)

// StartGrpcServer starts the gRPC server and returns it for graceful shutdown.
func StartGrpcServer(ctx context.Context, config *config.Config, wg *sync.WaitGroup) *grpc.Server {
	defer wg.Done()
	cfg = config
	cfg.Ctx = ctx
	lis, err := net.Listen("tcp", ":"+cfg.V2RSSub.GrpcPort)
	if err != nil {
		cfg.Logger.Fatal("Failed to listen gRPC", "port", cfg.V2RSSub.GrpcPort, "error", err)
	}

	// Добавляем keepalive параметры для сервера
	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(10*1024*1024), // 10MB
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 60 * time.Second,
			Time:              10 * time.Second,
			Timeout:           5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	proto.RegisterSubscriptionServiceServer(s, &server{})

	cfg.Logger.Info("gRPC server listening", "port", cfg.V2RSSub.GrpcPort)

	go func() {
		if err := s.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			cfg.Logger.Fatal("Failed to serve gRPC", "port", cfg.V2RSSub.GrpcPort, "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		cfg.Logger.Debug("Stopping gRPC server")
		s.GracefulStop()
		cfg.Logger.Info("gRPC server stopped")
	}()

	return s
}

func (s *server) DataStream(stream proto.SubscriptionService_DataStreamServer) error {
	mu.Lock()
	if currentStream != nil {
		mu.Unlock()
		return status.Error(codes.AlreadyExists, "Stream already connected")
	}
	currentStream = stream
	mu.Unlock()
	cfg.Logger.Info("New gRPC stream established")

	defer func() {
		mu.Lock()
		currentStream = nil
		mu.Unlock()
		cfg.Logger.Info("gRPC stream closed")
	}()

	for {
		select {
		case <-stream.Context().Done():
			cfg.Logger.Info("gRPC stream closed due to context cancellation")
			return nil
		case <-cfg.Ctx.Done():
			cfg.Logger.Info("gRPC stream closed due to application context cancellation")
			return nil
		default:
			resp, err := stream.Recv()
			if err == io.EOF {
				cfg.Logger.Info("gRPC stream closed by client (EOF)")
				return nil
			}
			if err != nil {
				cfg.Logger.Error("Failed to receive gRPC message", "error", err)
				return err
			}

			if resp.IsHeartbeat {
				cfg.Logger.Debug("Received heartbeat")
				continue
			}

			mu.Lock()
			lastResponse = resp
			ch, ok := waiting[resp.RequestId]
			if ok {
				cfg.Logger.Debug("Sending response to waiting channel", "request_id", resp.RequestId)
				ch <- resp
				close(ch)
				delete(waiting, resp.RequestId)
			}
			mu.Unlock()
		}
	}
}
