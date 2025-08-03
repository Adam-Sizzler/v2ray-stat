package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/backend/users"
	"v2ray-stat/common"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}
	common.InitTimezone(cfg.Timezone)

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

	var wg sync.WaitGroup
	users.MonitorUsers(ctx, manager, nodeClients, &cfg, &wg)
	db.MonitorSubscriptionsAndSync(ctx, manager, fileDB, &cfg, &wg)

	log.Printf("[START] v2ray-stat application started")
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
