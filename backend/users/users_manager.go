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

// SyncUsersWithNode синхронизирует пользователей с нод с базой данных (параллельно).
func SyncUsersWithNode(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, nc := range nodeClients {
		wg.Add(1)

		go func(nc *db.NodeClient) {
			defer wg.Done()

			// Проверка существования ноды
			err := manager.ExecuteHighPriority(func(db *sql.DB) error {
				var count int
				err := db.QueryRow("SELECT COUNT(*) FROM nodes WHERE node_name = ?", nc.NodeName).Scan(&count)
				if err != nil {
					return fmt.Errorf("check node existence: %w", err)
				}
				if count == 0 {
					return fmt.Errorf("node %s not found in nodes table", nc.NodeName)
				}
				return nil
			})
			if err != nil {
				cfg.Logger.Warn("Node check failed", "node_name", nc.NodeName, "error", err)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}

			// Получение пользователей с ноды
			grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			resp, err := nc.Client.GetUsers(grpcCtx, &proto.GetUsersRequest{})
			if err != nil {
				cfg.Logger.Error("Failed to get users from node", "node_name", nc.NodeName, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("node %s: %w", nc.NodeName, err))
				mu.Unlock()
				return
			}

			nodeUsers := make(map[string]bool, len(resp.Users))
			for _, user := range resp.Users {
				nodeUsers[user.Username] = true
			}
			cfg.Logger.Debug("Fetched users from node", "node_name", nc.NodeName, "user_count", len(nodeUsers))

			// Получение пользователей из базы данных
			var dbUsers []string
			err = manager.ExecuteHighPriority(func(db *sql.DB) error {
				rows, err := db.Query("SELECT user FROM user_traffic WHERE node_name = ?", nc.NodeName)
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
				cfg.Logger.Error("Failed to get users from database", "node_name", nc.NodeName, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("node %s: %w", nc.NodeName, err))
				mu.Unlock()
				return
			}

			// Синхронизация пользователей
			addedUsers, addedIDs, updatedUsers, deletedUsers := 0, 0, 0, 0
			err = manager.ExecuteHighPriority(func(db *sql.DB) error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return fmt.Errorf("start transaction for node %s: %w", nc.NodeName, err)
				}
				defer tx.Rollback()

				stmtUpsertUser, err := tx.Prepare(`
					INSERT INTO user_traffic (node_name, user, rate, created, enabled)
					VALUES (?, ?, 0, ?, ?)
					ON CONFLICT(node_name, user) DO UPDATE SET
					    enabled = excluded.enabled,
					    created = excluded.created`)
				if err != nil {
					return fmt.Errorf("prepare upsert user statement: %w", err)
				}
				defer stmtUpsertUser.Close()

				stmtInsertID, err := tx.Prepare(`
					INSERT OR IGNORE INTO user_ids (node_name, user, id, inbound_tag)
					VALUES (?, ?, ?, ?)`)
				if err != nil {
					return fmt.Errorf("prepare insert id statement: %w", err)
				}
				defer stmtInsertID.Close()

				currentTime := time.Now().Format("2006-01-02-15")
				for _, user := range resp.Users {
					enabledStr := "false"
					if user.Enabled {
						enabledStr = "true"
					}

					_, err := stmtUpsertUser.Exec(nc.NodeName, user.Username, currentTime, enabledStr)
					if err != nil {
						return fmt.Errorf("upsert user %s: %w", user.Username, err)
					}

					var exists int
					err = tx.QueryRow(`SELECT COUNT(*) FROM user_traffic WHERE node_name=? AND user=?`, nc.NodeName, user.Username).Scan(&exists)
					if err != nil {
						return fmt.Errorf("check existence for user %s: %w", user.Username, err)
					}
					if exists == 1 {
						var created string
						err = tx.QueryRow(`SELECT created FROM user_traffic WHERE node_name=? AND user=?`, nc.NodeName, user.Username).Scan(&created)
						if err != nil {
							return fmt.Errorf("get created for user %s: %w", user.Username, err)
						}
						if created == currentTime {
							addedUsers++
						} else {
							updatedUsers++
						}
					}

					for _, ui := range user.UuidInbounds {
						result, err := stmtInsertID.Exec(nc.NodeName, user.Username, ui.Uuid, ui.InboundTag)
						if err != nil {
							return fmt.Errorf("insert id %s for user %s: %w", ui.Uuid, user.Username, err)
						}
						rowsAffected, err := result.RowsAffected()
						if err != nil {
							return fmt.Errorf("check rows affected for id %s: %w", ui.Uuid, err)
						}
						if rowsAffected > 0 {
							addedIDs++
						}
					}
				}

				stmtDeleteUser, err := tx.Prepare("DELETE FROM user_traffic WHERE node_name = ? AND user = ?")
				if err != nil {
					return fmt.Errorf("prepare delete user statement: %w", err)
				}
				defer stmtDeleteUser.Close()

				stmtDeleteID, err := tx.Prepare("DELETE FROM user_ids WHERE node_name = ? AND user = ?")
				if err != nil {
					return fmt.Errorf("prepare delete id statement: %w", err)
				}
				defer stmtDeleteID.Close()

				for _, user := range dbUsers {
					if !nodeUsers[user] {
						result, err := stmtDeleteUser.Exec(nc.NodeName, user)
						if err != nil {
							return fmt.Errorf("delete user %s from user_traffic: %w", user, err)
						}
						rowsAffected, err := result.RowsAffected()
						if err != nil {
							return fmt.Errorf("check rows affected for user deletion %s: %w", user, err)
						}
						if rowsAffected > 0 {
							deletedUsers++
						}

						_, err = stmtDeleteID.Exec(nc.NodeName, user)
						if err != nil {
							return fmt.Errorf("delete user %s from user_ids: %w", user, err)
						}
					}
				}

				return tx.Commit()
			})
			if err != nil {
				cfg.Logger.Error("Failed to sync users for node", "node_name", nc.NodeName, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("node %s: %w", nc.NodeName, err))
				mu.Unlock()
				return
			}

			cfg.Logger.Debug("Successfully synced users for node",
				"node_name", nc.NodeName,
				"added_users", addedUsers,
				"updated_users", updatedUsers,
				"added_ids", addedIDs,
				"deleted_users", deletedUsers)

		}(nc)
	}

	wg.Wait()

	if len(errs) > 0 {
		for i, err := range errs {
			cfg.Logger.Error("Synchronization error", "index", i, "error", err)
		}
		return fmt.Errorf("synchronization failed with %d errors", len(errs))
	}
	return nil
}

// MonitorUsers периодически синхронизирует пользователей с нод с базой данных.
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
