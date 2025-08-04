package main

import (
	"fmt"
	"os"

	"v2ray-stat/common"
	"v2ray-stat/node/config"
	"v2ray-stat/node/grpcserver"
	"v2ray-stat/node/server"
)

func main() {
	cfg, err := config.LoadNodeConfig("node_config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load node config: %v\n", err)
		os.Exit(1)
	}

	common.InitTimezone(cfg.Timezone, cfg.Logger)

	nodeServer, err := server.NewNodeServer(&cfg)
	if err != nil {
		cfg.Logger.Error("Failed to create node server", "error", err)
		os.Exit(1)
	}

	if err := grpcserver.StartGRPCServer(&cfg, nodeServer); err != nil {
		cfg.Logger.Error("Failed to start gRPC server", "error", err)
		os.Exit(1)
	}
}
