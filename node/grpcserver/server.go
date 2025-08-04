package grpcserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"v2ray-stat/node/config"
	"v2ray-stat/node/proto"
	"v2ray-stat/node/server"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// StartGRPCServer настраивает и запускает gRPC-сервер.
func StartGRPCServer(cfg *config.NodeConfig, nodeServer *server.NodeServer) error {
	isLocalhost := cfg.V2rayStat.Address == "127.0.0.1" || cfg.V2rayStat.Address == "0.0.0.0" || cfg.V2rayStat.Address == "localhost"

	var grpcServer *grpc.Server
	if cfg.MTLSConfig != nil {
		cfg.Logger.Debug("Configuring mTLS for gRPC server")
		cert, err := tls.LoadX509KeyPair(cfg.MTLSConfig.Cert, cfg.MTLSConfig.Key)
		if err != nil {
			cfg.Logger.Error("Failed to load server certificate", "error", err)
			return err
		}
		caCert, err := os.ReadFile(cfg.MTLSConfig.CACert)
		if err != nil {
			cfg.Logger.Error("Failed to read CA certificate", "error", err)
			return err
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			cfg.Logger.Error("Failed to parse CA certificate")
			return fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientCAs:    caCertPool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
		}
		creds := credentials.NewTLS(tlsConfig)
		grpcServer = grpc.NewServer(grpc.Creds(creds))
	} else if !isLocalhost {
		cfg.Logger.Error("mTLS is required for non-localhost addresses", "address", cfg.V2rayStat.Address)
		return fmt.Errorf("mTLS is required for non-localhost addresses")
	} else {
		cfg.Logger.Debug("Using insecure gRPC server for localhost")
		grpcServer = grpc.NewServer()
	}

	proto.RegisterNodeServiceServer(grpcServer, nodeServer)

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%s", cfg.V2rayStat.Address, cfg.V2rayStat.Port))
	if err != nil {
		cfg.Logger.Error("Failed to start listener", "error", err)
		return err
	}

	cfg.Logger.Info("Starting gRPC server", "address", cfg.V2rayStat.Address, "port", cfg.V2rayStat.Port)
	if err := grpcServer.Serve(listener); err != nil {
		cfg.Logger.Error("Failed to start gRPC server", "error", err)
		return err
	}
	return nil
}
