package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"v2ray-stat/backend/api"
	"v2ray-stat/backend/api/reset_stats"
	"v2ray-stat/backend/certgen"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/backend/grpcclient"
	"v2ray-stat/backend/users"
	"v2ray-stat/common"
	"v2ray-stat/constant"

	_ "github.com/mattn/go-sqlite3"
)

// startAPIServer starts the API server.
func startAPIServer(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config, wg *sync.WaitGroup) {
	defer wg.Done()
	server := &http.Server{
		Addr:    cfg.V2rayStat.Address + ":" + cfg.V2rayStat.Port,
		Handler: api.WithServerHeader(http.DefaultServeMux),
	}

	http.HandleFunc("/", api.Answer())
	http.HandleFunc("/api/v1/users", api.UsersHandler(manager, cfg))
	http.HandleFunc("/api/v1/dns_stats", api.DnsStatsHandler(manager, cfg))
	http.HandleFunc("/api/v1/stats", api.StatsHandler(manager, cfg))

	http.HandleFunc("/api/v1/add_user", api.TokenAuthMiddleware(cfg, api.AddUserHandler(manager, cfg)))
	http.HandleFunc("/api/v1/delete_user", api.TokenAuthMiddleware(cfg, api.DeleteUserHandler(manager, cfg)))
	http.HandleFunc("/api/v1/set_user_enabled", api.TokenAuthMiddleware(cfg, api.SetUserEnabledHandler(manager, cfg)))
	http.HandleFunc("/api/v1/update_ip_limit", api.TokenAuthMiddleware(cfg, api.UpdateIPLimitHandler(manager, cfg)))
	http.HandleFunc("/api/v1/update_renew", api.TokenAuthMiddleware(cfg, api.UpdateRenewHandler(manager, cfg)))
	http.HandleFunc("/api/v1/reset_dns_stats", api.TokenAuthMiddleware(cfg, reset_stats.DeleteDNSStatsHandler(manager, cfg)))
	http.HandleFunc("/api/v1/reset_bound_traffic", api.TokenAuthMiddleware(cfg, reset_stats.ResetTrafficStatsHandler(manager, cfg)))
	http.HandleFunc("/api/v1/reset_user_traffic", api.TokenAuthMiddleware(cfg, reset_stats.ResetClientsStatsHandler(manager, cfg)))

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

	// Проверяем и генерируем сертификаты, если они отсутствуют
	certgen.EnsureCertificates(&cfg)

	// Готовим wg
	var wg sync.WaitGroup
	wg.Add(4)

	go startAPIServer(ctx, manager, &cfg, &wg)
	go grpcclient.StartGrpcClient(ctx, &cfg, manager, &wg)
	go db.MonitorSubscriptionsAndSync(ctx, manager, fileDB, &cfg, &wg)
	go users.MonitorNodeData(ctx, manager, nodeClients, &cfg, &wg)

	log.Printf("[START] v2ray-stat-backend application %s", constant.Version)

	<-sigChan
	cfg.Logger.Info("Received termination signal, saving data")
	cancel()

	// Закрываем все gRPC-соединения
	for _, nc := range nodeClients {
		if err := nc.Close(); err != nil {
			cfg.Logger.Error("Failed to close node client", "node_name", nc.NodeName, "error", err)
		}
	}

	// Закрываем DatabaseManager
	manager.Close()

	// Ждём завершения горутин
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		cfg.Logger.Debug("All goroutines completed")
	case <-time.After(10 * time.Second):
		cfg.Logger.Warn("Timeout waiting for goroutines to complete, forcing shutdown")
		// Профилирование горутин при тайм-ауте
		pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
	}

	// Финальная синхронизация базы данных
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer syncCancel()
	if _, err := os.Stat(cfg.Paths.Database); os.IsNotExist(err) {
		cfg.Logger.Warn("File database does not exist, recreating", "path", cfg.Paths.Database)
		if fileDB, err = db.OpenAndInitDB(cfg.Paths.Database, "file", &cfg); err != nil {
			cfg.Logger.Error("Failed to recreate file database", "path", cfg.Paths.Database, "error", err)
		} else {
			defer fileDB.Close()
			if err := manager.SyncDBWithContext(syncCtx, fileDB, "memory to file"); err != nil {
				cfg.Logger.Error("Failed to perform final database synchronization", "error", err)
			} else {
				cfg.Logger.Info("Database synchronized successfully (memory to file)")
			}
		}
	} else {
		if err := manager.SyncDBWithContext(syncCtx, fileDB, "memory to file"); err != nil {
			cfg.Logger.Error("Failed to perform final database synchronization", "error", err)
		} else {
			cfg.Logger.Info("Database synchronized successfully (memory to file)")
		}
	}

	// Закрываем базы данных после синхронизации
	if err := memDB.Close(); err != nil {
		cfg.Logger.Error("Failed to close in-memory database", "error", err)
	}
	if err := fileDB.Close(); err != nil {
		cfg.Logger.Error("Failed to close file database", "error", err)
	}

	log.Printf("[STOP] Program terminated")
}
