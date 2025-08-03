package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"v2ray-stat/common"
	"v2ray-stat/node/config"
	"v2ray-stat/node/proto"
	nodeserver "v2ray-stat/node/server" // <-- теперь пакет server называется nodeserver

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	cfg, err := config.LoadNodeConfig("node_config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load node config: %v\n", err)
		os.Exit(1)
	}

	common.InitTimezone(cfg.Timezone)

	var grpcServer *grpc.Server
	isLocalhost := cfg.V2rayStat.Address == "127.0.0.1" || cfg.V2rayStat.Address == "0.0.0.0" || cfg.V2rayStat.Address == "localhost"

	if cfg.MTLSConfig != nil && (!isLocalhost || isLocalhost) {
		cfg.Logger.Debug("Configuring mTLS for gRPC server")
		cert, err := tls.LoadX509KeyPair(cfg.MTLSConfig.Cert, cfg.MTLSConfig.Key)
		if err != nil {
			cfg.Logger.Error("Failed to load server certificate", "error", err)
			os.Exit(1)
		}
		caCert, err := os.ReadFile(cfg.MTLSConfig.CACert)
		if err != nil {
			cfg.Logger.Error("Failed to read CA certificate", "error", err)
			os.Exit(1)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			cfg.Logger.Error("Failed to parse CA certificate")
			os.Exit(1)
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
		os.Exit(1)
	} else {
		cfg.Logger.Debug("Using insecure gRPC server for localhost")
		grpcServer = grpc.NewServer()
	}

	// Вот здесь используем переименованный импорт nodeserver
	proto.RegisterNodeServiceServer(grpcServer, &nodeserver.NodeServer{Cfg: &cfg})

	cfg.Logger.Info("Starting gRPC server", "address", cfg.V2rayStat.Address, "port", cfg.V2rayStat.Port)
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%s", cfg.V2rayStat.Address, cfg.V2rayStat.Port))
	if err != nil {
		cfg.Logger.Error("Failed to start listener", "error", err)
		os.Exit(1)
	}

	if err := grpcServer.Serve(listener); err != nil {
		cfg.Logger.Error("Failed to start gRPC server", "error", err)
		os.Exit(1)
	}
}
