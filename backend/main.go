package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"v2ray-stat/backend/api"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/backend/users"
	"v2ray-stat/common"
	"v2ray-stat/constant"

	_ "github.com/mattn/go-sqlite3"
)

// withServerHeader adds a Server header to all responses.
func withServerHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHeader := fmt.Sprintf("MuxCloud/%s (WebServer)", constant.Version)
		w.Header().Set("Server", serverHeader)
		w.Header().Set("X-Powered-By", "MuxCloud")
		next.ServeHTTP(w, r)
	})
}

// startAPIServer starts the API server.
func startAPIServer(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config, wg *sync.WaitGroup) {
	server := &http.Server{
		Addr:    cfg.V2rayStat.Address + ":" + cfg.V2rayStat.Port,
		Handler: withServerHeader(http.DefaultServeMux),
	}

	// Placeholder
	http.HandleFunc("/", api.Answer())

	// Read-only endpoints (no token required)
	http.HandleFunc("/api/v1/stats", api.StatsCustomHandler(manager, cfg))
	http.HandleFunc("/api/v1/node_stats", api.NodeStatsHandler(manager, cfg))
	http.HandleFunc("/api/v1/aggregate_stats", api.AggregateStatsHandler(manager, cfg))

	cfg.Logger.Debug("Starting API server", "address", server.Addr)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			cfg.Logger.Fatal("Failed to start server", "error", err)
		}
	}()

	<-ctx.Done()

	cfg.Logger.Debug("Shutting down API server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		cfg.Logger.Error("Error shutting down server", "error", err)
	}
	wg.Done()
}

func main() {
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}
	common.InitTimezone(cfg.Timezone, cfg.Logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	memDB, fileDB, err := db.InitDatabase(&cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to initialize database", "error", err)
	}
	defer memDB.Close()
	defer fileDB.Close()

	manager, err := manager.NewDatabaseManager(memDB, ctx, 1, 300, 500, &cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to create DatabaseManager", "error", err)
	}

	nodeClients, err := db.InitNodeClients(&cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to initialize node clients", "error", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start tasks
	var wg sync.WaitGroup
	wg.Add(1)
	go startAPIServer(ctx, manager, &cfg, &wg)
	db.MonitorSubscriptionsAndSync(ctx, manager, fileDB, &cfg, &wg)
	users.MonitorUsers(ctx, manager, nodeClients, &cfg, &wg)
	users.MonitorTrafficStats(ctx, manager, nodeClients, &cfg, &wg)
	users.MonitorLogData(ctx, manager, nodeClients, &cfg, &wg)

	log.Printf("[START] v2ray-stat-backend application %s", constant.Version)
	<-sigChan
	cfg.Logger.Info("Received termination signal, saving data")
	cancel()

	wg.Wait()

	syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer syncCancel()
	if _, err := os.Stat(cfg.Paths.Database); os.IsNotExist(err) {
		cfg.Logger.Warn("File database does not exist, recreating", "path", cfg.Paths.Database)
		if fileDB, err = db.OpenAndInitDB(cfg.Paths.Database, "file", &cfg); err != nil {
			cfg.Logger.Error("Failed to recreate file database", "path", cfg.Paths.Database, "error", err)
		} else {
			defer fileDB.Close()
		}
	}
	if err := manager.SyncDBWithContext(syncCtx, fileDB, "memory to file"); err != nil {
		cfg.Logger.Error("Failed to perform final database synchronization", "error", err)
	} else {
		cfg.Logger.Info("Database synchronized successfully (memory to file)")
	}

	manager.Close()
	log.Printf("[STOP] Program terminated")
}
