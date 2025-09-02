package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"v2ray-stat/backend/api"
	"v2ray-stat/subscription/config"
	"v2ray-stat/subscription/grpcserver"
	"v2ray-stat/subscription/handler"
	"v2ray-stat/subscription/templates"

	"v2ray-stat/common"
	"v2ray-stat/constant"
)

// startAPIServer starts the API server.
func startAPIServer(ctx context.Context, cfg *config.Config, wg *sync.WaitGroup) {
	defer wg.Done()
	server := &http.Server{
		Addr:    cfg.V2RSSub.Address + ":" + cfg.V2RSSub.Port,
		Handler: api.WithServerHeader(http.DefaultServeMux),
	}

	http.HandleFunc("/api/v1/sub", handler.SubscriptionHandler)

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
	if cfg.Logger == nil {
		log.Fatalf("cfg.Logger is nil after LoadConfig")
	}
	cfg.Logger.Debug("Global config initialized", "config", fmt.Sprintf("%+v", cfg))
	common.InitTimezone(cfg.Timezone, cfg.Logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initial load of templates
	if err := templates.LoadTemplates(&cfg); err != nil {
		cfg.Logger.Fatal("Failed to load templates", "error", err)
	} else {
		cfg.Logger.Info("Templates loaded successfully")
	}

	var wg sync.WaitGroup
	wg.Add(4)

	grpcServer := grpcserver.StartGrpcServer(ctx, &cfg, &wg)
	go startAPIServer(ctx, &cfg, &wg)
	go config.WatchConfig(ctx, &cfg, &wg)
	go templates.WatchTemplates(ctx, &cfg, &wg)

	log.Printf("[START] v2ray-stat-subscription application %s", constant.Version)

	<-sigChan
	cfg.Logger.Info("Received termination signal")

	cancel()

	// Останавливаем gRPC-сервер
	cfg.Logger.Debug("Stopping gRPC server")
	grpcServer.GracefulStop()
	cfg.Logger.Info("gRPC server stopped")

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

	cfg.Logger.Info("Program terminated")
}
