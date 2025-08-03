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

// Константы для SQL-запросов
const (
	queryInsertUserStats = `
		INSERT OR IGNORE INTO user_stats(node_name, user, rate, enabled, created)
		VALUES (?, ?, ?, ?, ?)`
	queryInsertUserUUIDs = `
		INSERT OR IGNORE INTO user_uuids(node_name, user, uuid, inbound_tag)
		VALUES (?, ?, ?, ?)`
	querySelectDBUsers = `
		SELECT user FROM user_stats WHERE node_name = ?`
	queryDeleteUserStats = `
		DELETE FROM user_stats WHERE node_name = ? AND user = ?`
	queryDeleteUserUUIDs = `
		DELETE FROM user_uuids WHERE node_name = ? AND user = ?`
)

// SyncUsersWithNode synchronizes users from a node with the database, adding new users and removing absent ones.
func SyncUsersWithNode(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config) error {
	var errs []error
	for _, nc := range nodeClients {
		// Создаём контекст с таймаутом для gRPC-запроса
		grpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Выполняем один gRPC-запрос для получения пользователей
		resp, err := nc.Client.GetUsers(grpcCtx, &proto.GetUsersRequest{})
		if err != nil {
			cfg.Logger.Error("Failed to get users from node", "node", nc.Name, "error", err)
			errs = append(errs, fmt.Errorf("node %s: %w", nc.Name, err))
			continue
		}

		// Создаём карту пользователей из gRPC-ответа
		nodeUsers := make(map[string]bool)
		for _, user := range resp.Users {
			nodeUsers[user.Username] = true
		}

		// Получаем текущих пользователей из базы данных (низкоприоритетная задача)
		var dbUsers []string
		err = manager.ExecuteLowPriority(func(db *sql.DB) error {
			rows, err := db.Query(querySelectDBUsers, nc.Name)
			if err != nil {
				return fmt.Errorf("query users from user_stats: %w", err)
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

		// Выполняем все операции для ноды в одной транзакции (высокоприоритетная задача)
		addedUsers, addedUUIDs, deletedUsers := 0, 0, 0
		err = manager.ExecuteHighPriority(func(db *sql.DB) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("start transaction for node %s: %w", nc.Name, err)
			}
			defer tx.Rollback()

			// Добавляем новых пользователей и их UUIDs
			for _, user := range resp.Users {
				_, err := tx.Exec(queryInsertUserStats,
					nc.Name, user.Username, 0, "true", time.Now().Format("2006-01-02-15"))
				if err != nil {
					return fmt.Errorf("insert user %s into user_stats: %w", user.Username, err)
				}
				addedUsers++

				for _, ui := range user.UuidInbounds {
					_, err := tx.Exec(queryInsertUserUUIDs,
						nc.Name, user.Username, ui.Uuid, ui.InboundTag)
					if err != nil {
						return fmt.Errorf("insert uuid %s for user %s into user_uuids: %w", ui.Uuid, user.Username, err)
					}
					addedUUIDs++
				}
			}

			// Удаляем пользователей, отсутствующих в gRPC-ответе
			for _, user := range dbUsers {
				if !nodeUsers[user] {
					_, err := tx.Exec(queryDeleteUserStats, nc.Name, user)
					if err != nil {
						return fmt.Errorf("delete user %s from user_stats: %w", user, err)
					}
					_, err = tx.Exec(queryDeleteUserUUIDs, nc.Name, user)
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
