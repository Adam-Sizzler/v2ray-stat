package db

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"

	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// NodeClient represents a gRPC client for a node.
type NodeClient struct {
	Name   string
	URL    string
	Client proto.NodeServiceClient
}

// InitNodeClients initializes gRPC clients for all nodes.
func InitNodeClients(cfg *config.Config) ([]*NodeClient, error) {
	var nodeClients []*NodeClient
	for _, nodeCfg := range cfg.V2rayStat.Nodes {
		var opts []grpc.DialOption
		if nodeCfg.MTLSConfig != nil {
			cert, err := tls.LoadX509KeyPair(nodeCfg.MTLSConfig.Cert, nodeCfg.MTLSConfig.Key)
			if err != nil {
				cfg.Logger.Error("Failed to load client certificate", "node", nodeCfg.Name, "error", err)
				continue
			}
			caCert, err := os.ReadFile(nodeCfg.MTLSConfig.CACert)
			if err != nil {
				cfg.Logger.Error("Failed to read CA certificate", "node", nodeCfg.Name, "error", err)
				continue
			}
			certPool := x509.NewCertPool()
			if !certPool.AppendCertsFromPEM(caCert) {
				cfg.Logger.Error("Failed to parse CA certificate", "node", nodeCfg.Name)
				continue
			}
			tlsConfig := &tls.Config{
				Certificates:       []tls.Certificate{cert},
				RootCAs:            certPool,
				InsecureSkipVerify: true, // Отключаем проверку имени сервера (SAN)
			}
			creds := credentials.NewTLS(tlsConfig)
			opts = append(opts, grpc.WithTransportCredentials(creds))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		}

		conn, err := grpc.Dial(nodeCfg.URL, opts...)
		if err != nil {
			cfg.Logger.Error("Failed to connect to node", "node", nodeCfg.Name, "url", nodeCfg.URL, "error", err)
			continue
		}

		client := proto.NewNodeServiceClient(conn)
		nodeClients = append(nodeClients, &NodeClient{
			Name:   nodeCfg.Name,
			URL:    nodeCfg.URL,
			Client: client,
		})
	}
	return nodeClients, nil
}

// OpenAndInitDB opens and initializes a SQLite database.
func OpenAndInitDB(dbPath string, dbType string, cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		cfg.Logger.Error("Failed to open database", "dbType", dbType, "path", dbPath, "error", err)
		return nil, fmt.Errorf("failed to open %s database: %v", dbType, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	var tableCount int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='clients_stats'").Scan(&tableCount)
	if err != nil {
		cfg.Logger.Error("Failed to check table existence", "dbType", dbType, "error", err)
		db.Close()
		return nil, fmt.Errorf("failed to check table existence for %s database: %v", dbType, err)
	}
	if tableCount > 0 {
		cfg.Logger.Debug("Tables already exist", "dbType", dbType)
		return db, nil
	}

	sqlStmt := `
		PRAGMA foreign_keys = ON;
        PRAGMA cache_size = 2000;
        PRAGMA journal_mode = WAL;
        PRAGMA synchronous = NORMAL;
        PRAGMA temp_store = MEMORY;
        PRAGMA busy_timeout = 5000;

        CREATE TABLE IF NOT EXISTS nodes (
            name TEXT PRIMARY KEY,
            address TEXT NOT NULL UNIQUE
        );
        CREATE TABLE IF NOT EXISTS traffic_stats (
            node_name TEXT,
            source TEXT,
            rate INTEGER DEFAULT 0,
            uplink INTEGER DEFAULT 0,
            downlink INTEGER DEFAULT 0,
            sess_uplink INTEGER DEFAULT 0,
            sess_downlink INTEGER DEFAULT 0,
            PRIMARY KEY (node_name, source),
            FOREIGN KEY (node_name) REFERENCES nodes(name) ON DELETE CASCADE
        );
        CREATE TABLE IF NOT EXISTS user_stats (
            node_name TEXT,
            user TEXT,
            last_seen TEXT DEFAULT '',
            rate INTEGER DEFAULT 0,
            enabled TEXT DEFAULT 'true',
            sub_end TEXT DEFAULT '',
            renew INTEGER DEFAULT 0,
            lim_ip INTEGER DEFAULT 0,
            ips TEXT DEFAULT '',
            uplink INTEGER DEFAULT 0,
            downlink INTEGER DEFAULT 0,
            sess_uplink INTEGER DEFAULT 0,
            sess_downlink INTEGER DEFAULT 0,
            created TEXT,
            PRIMARY KEY (node_name, user),
            FOREIGN KEY (node_name) REFERENCES nodes(name) ON DELETE CASCADE
        );
        CREATE TABLE IF NOT EXISTS user_uuids (
            node_name TEXT,
            user TEXT,
            uuid TEXT,
            inbound_tag TEXT,
            PRIMARY KEY (node_name, user, uuid, inbound_tag),
            FOREIGN KEY (node_name, user) REFERENCES user_stats(node_name, user) ON DELETE CASCADE,
            FOREIGN KEY (node_name) REFERENCES nodes(name) ON DELETE CASCADE
        );
        CREATE TABLE IF NOT EXISTS dns_stats (
            node_name TEXT,
            user TEXT,
            count INTEGER DEFAULT 1,
            domain TEXT,
            PRIMARY KEY (node_name, user, domain),
            FOREIGN KEY (node_name) REFERENCES nodes(name) ON DELETE CASCADE
        );

        CREATE INDEX IF NOT EXISTS idx_clients_stats_user ON user_stats(node_name, user);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_rate ON user_stats(rate);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_enabled ON user_stats(enabled);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_sub_end ON user_stats(sub_end);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_renew ON user_stats(renew);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_sess_uplink ON user_stats(sess_uplink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_sess_downlink ON user_stats(sess_downlink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_uplink ON user_stats(uplink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_downlink ON user_stats(downlink);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_lim_ip ON user_stats(lim_ip);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_ips ON user_stats(ips);
        CREATE INDEX IF NOT EXISTS idx_clients_stats_last_seen ON user_stats(last_seen);
    `
	if _, err = db.Exec(sqlStmt); err != nil {
		cfg.Logger.Error("Failed to execute SQL script", "dbType", dbType, "error", err)
		db.Close()
		return nil, fmt.Errorf("failed to execute SQL script for %s database: %v", dbType, err)
	}

	for _, node := range cfg.V2rayStat.Nodes {
		_, err = db.Exec("INSERT OR IGNORE INTO nodes (name, address) VALUES (?, ?)", node.Name, node.URL)
		if err != nil {
			cfg.Logger.Error("Failed to insert node", "name", node.Name, "address", node.URL, "error", err)
		}
	}

	cfg.Logger.Info("Database initialized", "dbType", dbType)
	return db, nil
}

// InitDatabase initializes in-memory and file databases.
func InitDatabase(cfg *config.Config) (memDB, fileDB *sql.DB, err error) {
	memDB, err = OpenAndInitDB(":memory:", "in-memory", cfg)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			memDB.Close()
		}
	}()

	fileExists := true
	if _, err := os.Stat(cfg.Paths.Database); os.IsNotExist(err) {
		cfg.Logger.Warn("File database does not exist, will create new", "path", cfg.Paths.Database)
		fileExists = false
	} else if err != nil {
		cfg.Logger.Error("Failed to check file database", "path", cfg.Paths.Database, "error", err)
		return nil, nil, fmt.Errorf("error checking file database: %v", err)
	}

	fileDB, err = OpenAndInitDB(cfg.Paths.Database, "file", cfg)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			fileDB.Close()
		}
	}()

	if fileExists {
		var tableCount int
		err = fileDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='user_stats'").Scan(&tableCount)
		if err == nil && tableCount > 0 {
			tempManager, err := manager.NewDatabaseManager(fileDB, context.Background(), 1, 300, 500, cfg)
			if err != nil {
				cfg.Logger.Error("Failed to create temporary DatabaseManager", "error", err)
				return nil, nil, fmt.Errorf("failed to create temporary DatabaseManager: %v", err)
			}
			syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer syncCancel()
			if err = tempManager.SyncDBWithContext(syncCtx, memDB, "file to memory"); err != nil {
				cfg.Logger.Error("Failed to synchronize database (file to memory)", "error", err)
			}
			tempManager.Close()
		}
	}

	cfg.Logger.Info("Database initialization completed", "in-memory", true, "file", true)
	return memDB, fileDB, nil
}

// MonitorSubscriptionsAndSync runs periodic subscription checks and database synchronization.
func MonitorSubscriptionsAndSync(ctx context.Context, manager *manager.DatabaseManager, fileDB *sql.DB, cfg *config.Config, wg *sync.WaitGroup) {
	cfg.Logger.Debug("Starting subscription and sync monitoring")
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer syncCancel()
				if _, err := os.Stat(cfg.Paths.Database); os.IsNotExist(err) {
					cfg.Logger.Warn("File database does not exist, recreating", "path", cfg.Paths.Database)
					fileDB, err = OpenAndInitDB(cfg.Paths.Database, "file", cfg)
					if err != nil {
						cfg.Logger.Error("Failed to recreate file database", "path", cfg.Paths.Database, "error", err)
						continue
					}
				} else if err != nil {
					cfg.Logger.Error("Failed to check file database", "path", cfg.Paths.Database, "error", err)
					continue
				}
				if err := manager.SyncDBWithContext(syncCtx, fileDB, "memory to file"); err != nil {
					cfg.Logger.Error("Failed to synchronize database (memory to file)", "error", err)
				} else {
					cfg.Logger.Info("Database synchronized successfully (memory to file)")
				}
			case <-ctx.Done():
				cfg.Logger.Debug("Stopped subscription and sync monitoring")
				return
			}
		}
	}()
}
