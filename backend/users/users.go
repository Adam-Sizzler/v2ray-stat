package users

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db"
	"v2ray-stat/backend/db/manager"
	"v2ray-stat/node/proto"
)

// SyncUsersWithNode синхронизирует пользователей с ноды с базой данных, добавляя новых и удаляя отсутствующих.
func SyncUsersWithNode(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config) error {
	var errs []error
	for _, nc := range nodeClients {
		grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		resp, err := nc.Client.GetUsers(grpcCtx, &proto.GetUsersRequest{})
		if err != nil {
			cfg.Logger.Error("Failed to get users from node", "node", nc.Name, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nc.Name, err))
			continue
		}

		nodeUsers := make(map[string]bool)
		for _, user := range resp.Users {
			nodeUsers[user.Username] = true
		}

		var dbUsers []string
		err = manager.ExecuteHighPriority(func(db *sql.DB) error {
			rows, err := db.Query("SELECT user FROM user_traffic WHERE node_name = ?", nc.Name)
			if err != nil {
				return fmt.Errorf("query users from user_traffic: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var user string
				if err := rows.Scan(&user); err != nil {
					return fmt.Errorf("scan user: %w", err)
				}
				dbUsers = append(dbUsers, user)
			}
			return rows.Err()
		})
		if err != nil {
			cfg.Logger.Error("Failed to get users from database", "node", nc.Name, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nc.Name, err))
			continue
		}

		addedUsers, addedUUIDs, deletedUsers := 0, 0, 0
		err = manager.ExecuteHighPriority(func(db *sql.DB) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("start transaction for node %s: %w", nc.Name, err)
			}
			defer tx.Rollback()

			for _, user := range resp.Users {
				// Добавляем пользователя в user_traffic с колонкой created
				_, err = tx.Exec(`
					INSERT OR IGNORE INTO user_traffic (node_name, user, rate, created)
					VALUES (?, ?, 0, ?)`,
					nc.Name, user.Username, time.Now().Format("2006-01-02-15"))
				if err != nil {
					return fmt.Errorf("insert user %s into user_traffic: %w", user.Username, err)
				}
				addedUsers++

				for _, ui := range user.UuidInbounds {
					_, err := tx.Exec(`
						INSERT OR IGNORE INTO user_uuids (node_name, user, uuid, inbound_tag)
						VALUES (?, ?, ?, ?)`,
						nc.Name, user.Username, ui.Uuid, ui.InboundTag)
					if err != nil {
						return fmt.Errorf("insert uuid %s for user %s into user_uuids: %w", ui.Uuid, user.Username, err)
					}
					addedUUIDs++
				}
			}

			for _, user := range dbUsers {
				if !nodeUsers[user] {
					_, err := tx.Exec("DELETE FROM user_traffic WHERE node_name = ? AND user = ?", nc.Name, user)
					if err != nil {
						return fmt.Errorf("delete user %s from user_traffic: %w", user, err)
					}
					_, err = tx.Exec("DELETE FROM user_uuids WHERE node_name = ? AND user = ?", nc.Name, user)
					if err != nil {
						return fmt.Errorf("delete user %s from user_uuids: %w", user, err)
					}
					deletedUsers++
				}
			}

			return tx.Commit()
		})
		if err != nil {
			cfg.Logger.Error("Failed to sync users for node", "node", nc.Name, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nc.Name, err))
			continue
		}

		cfg.Logger.Debug("Successfully synced users for node",
			"node", nc.Name,
			"added_users", addedUsers,
			"added_uuids", addedUUIDs,
			"deleted_users", deletedUsers)
	}

	if len(errs) > 0 {
		return fmt.Errorf("synchronization errors: %v", errs)
	}
	return nil
}

// MonitorUsersAndLogs periodically synchronizes users from nodes with the database.
func MonitorUsers(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(cfg.V2rayStat.Monitor.TickerInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := SyncUsersWithNode(ctx, manager, nodeClients, cfg); err != nil {
					cfg.Logger.Error("Failed to synchronize users", "error", err)
				}
			case <-ctx.Done():
				cfg.Logger.Debug("User monitoring stopped")
				return
			}
		}
	}()
}
