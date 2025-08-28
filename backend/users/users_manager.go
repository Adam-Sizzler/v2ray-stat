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
	"v2ray-stat/common"
	"v2ray-stat/proto"
)

// SyncUsersWithNode synchronizes users from multiple nodes with the database.
func SyncUsersWithNode(ctx context.Context, manager *manager.DatabaseManager, nodeClients []*db.NodeClient, cfg *config.Config) error {
	cfg.Logger.Debug("Starting user synchronization", "node_count", len(nodeClients))

	var wg sync.WaitGroup
	resultsCh := make(chan struct {
		nodeName string
		users    *proto.ListUsersResponse
		err      error
	}, len(nodeClients))

	// Fetch users from each node
	for _, nc := range nodeClients {
		wg.Add(1)
		go func(nc *db.NodeClient) {
			defer wg.Done()

			if nc == nil || nc.Client == nil {
				cfg.Logger.Error("Node client is nil", "node_name", nc.NodeName)
				resultsCh <- struct {
					nodeName string
					users    *proto.ListUsersResponse
					err      error
				}{nc.NodeName, nil, fmt.Errorf("nil gRPC client for node %s", nc.NodeName)}
				return
			}

			grpcCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			cfg.Logger.Debug("Requesting user list from node", "node_name", nc.NodeName)
			users, err := nc.Client.ListUsers(grpcCtx, &proto.ListUsersRequest{})
			if err != nil {
				cfg.Logger.Error("Failed to list users from node", "node_name", nc.NodeName, "error", err)
				resultsCh <- struct {
					nodeName string
					users    *proto.ListUsersResponse
					err      error
				}{nc.NodeName, nil, err}
				return
			}
			cfg.Logger.Debug("Received user list from node", "node_name", nc.NodeName, "user_count", len(users.Users))
			resultsCh <- struct {
				nodeName string
				users    *proto.ListUsersResponse
				err      error
			}{nc.NodeName, users, nil}
		}(nc)
	}

	wg.Wait()
	close(resultsCh)

	// Process results
	for res := range resultsCh {
		if res.err != nil {
			cfg.Logger.Error("Skipping node due to error", "node_name", res.nodeName, "error", res.err)
			continue
		}
		if res.users == nil {
			cfg.Logger.Error("No user data received from node", "node_name", res.nodeName)
			continue
		}

		nodeUsers := make(map[string]bool, len(res.users.Users))
		for _, user := range res.users.Users {
			nodeUsers[user.Username] = true
		}
		cfg.Logger.Debug("Fetched users from node", "node_name", res.nodeName, "user_count", len(nodeUsers))

		// Fetch users from database
		var dbUsers []string
		err := manager.ExecuteHighPriority(func(db *sql.DB) error {
			rows, err := db.Query("SELECT user FROM user_traffic WHERE node_name = ?", res.nodeName)
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
			cfg.Logger.Error("Failed to get users from database", "node_name", res.nodeName, "error", err)
			continue
		}
		cfg.Logger.Debug("Fetched users from database", "node_name", res.nodeName, "db_user_count", len(dbUsers))

		// Synchronize users
		addedUsers, addedIDs, updatedUsers, deletedUsers := 0, 0, 0, 0
		err = manager.ExecuteHighPriority(func(db *sql.DB) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("start transaction for node %s: %w", res.nodeName, err)
			}
			defer tx.Rollback()

			// Запрос для вставки новых пользователей
			stmtInsertUser, err := tx.Prepare(`
				INSERT INTO user_traffic (node_name, user, rate, last_seen, created, enabled)
				VALUES (?, ?, 0, ?, ?, ?)
				ON CONFLICT(node_name, user) DO NOTHING`)
			if err != nil {
				return fmt.Errorf("prepare insert user statement: %w", err)
			}
			defer stmtInsertUser.Close()

			// Запрос для обновления существующих пользователей
			stmtUpdateUser, err := tx.Prepare(`
				UPDATE user_traffic
				SET enabled = ?
				WHERE node_name = ? AND user = ?`)
			if err != nil {
				return fmt.Errorf("prepare update user statement: %w", err)
			}
			defer stmtUpdateUser.Close()

			stmtInsertID, err := tx.Prepare(`
				INSERT OR IGNORE INTO user_ids (node_name, user, id, inbound_tag)
				VALUES (?, ?, ?, ?)`)
			if err != nil {
				return fmt.Errorf("prepare insert id statement: %w", err)
			}
			defer stmtInsertID.Close()

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

			currentTime := time.Now().In(common.TimeLocation).Unix()
			for _, user := range res.users.Users {
				enabledStr := "false"
				if user.Enabled {
					enabledStr = "true"
				}

				cfg.Logger.Debug("Processing user", "node_name", res.nodeName, "user", user.Username, "enabled", enabledStr)

				// Проверяем, существует ли пользователь
				var exists int
				err = tx.QueryRow(`SELECT COUNT(*) FROM user_traffic WHERE node_name = ? AND user = ?`, res.nodeName, user.Username).Scan(&exists)
				if err != nil {
					return fmt.Errorf("check existence for user %s: %w", user.Username, err)
				}

				if exists == 0 {
					// Вставка нового пользователя
					result, err := stmtInsertUser.Exec(res.nodeName, user.Username, currentTime, currentTime, enabledStr)
					if err != nil {
						return fmt.Errorf("insert user %s: %w", user.Username, err)
					}
					rowsAffected, err := result.RowsAffected()
					if err != nil {
						return fmt.Errorf("check rows affected for insert user %s: %w", user.Username, err)
					}
					if rowsAffected > 0 {
						addedUsers++
						cfg.Logger.Debug("User added", "node_name", res.nodeName, "user", user.Username)
					}
				} else {
					// Обновление существующего пользователя (только enabled)
					result, err := stmtUpdateUser.Exec(enabledStr, res.nodeName, user.Username)
					if err != nil {
						return fmt.Errorf("update user %s: %w", user.Username, err)
					}
					rowsAffected, err := result.RowsAffected()
					if err != nil {
						return fmt.Errorf("check rows affected for update user %s: %w", user.Username, err)
					}
					if rowsAffected > 0 {
						updatedUsers++
						cfg.Logger.Debug("User updated", "node_name", res.nodeName, "user", user.Username)
					}
				}

				// Вставка ID для пользователя
				for _, ui := range user.IdInbounds {
					cfg.Logger.Debug("Inserting ID for user", "node_name", res.nodeName, "user", user.Username, "id", ui.Id, "inbound_tag", ui.InboundTag)
					result, err := stmtInsertID.Exec(res.nodeName, user.Username, ui.Id, ui.InboundTag)
					if err != nil {
						return fmt.Errorf("insert id %s for user %s: %w", ui.Id, user.Username, err)
					}
					rowsAffected, err := result.RowsAffected()
					if err != nil {
						return fmt.Errorf("check rows affected for id %s: %w", ui.Id, err)
					}
					if rowsAffected > 0 {
						addedIDs++
						cfg.Logger.Debug("ID added for user", "node_name", res.nodeName, "user", user.Username, "id", ui.Id)
					}
				}
			}

			// Удаление пользователей, которых нет в nodeUsers
			for _, user := range dbUsers {
				if !nodeUsers[user] {
					cfg.Logger.Debug("Deleting user", "node_name", res.nodeName, "user", user)
					result, err := stmtDeleteUser.Exec(res.nodeName, user)
					if err != nil {
						return fmt.Errorf("delete user %s from user_traffic: %w", user, err)
					}
					rowsAffected, err := result.RowsAffected()
					if err != nil {
						return fmt.Errorf("check rows affected for user deletion %s: %w", user, err)
					}
					if rowsAffected > 0 {
						deletedUsers++
						cfg.Logger.Debug("User deleted", "node_name", res.nodeName, "user", user)
					}

					_, err = stmtDeleteID.Exec(res.nodeName, user)
					if err != nil {
						return fmt.Errorf("delete user %s from user_ids: %w", user, err)
					}
				}
			}

			cfg.Logger.Debug("Committing transaction for node", "node_name", res.nodeName)
			return tx.Commit()
		})
		if err != nil {
			cfg.Logger.Error("Failed to sync users for node", "node_name", res.nodeName, "error", err)
			continue
		}

		cfg.Logger.Debug("Successfully synced users for node",
			"node_name", res.nodeName,
			"added_users", addedUsers,
			"updated_users", updatedUsers,
			"added_ids", addedIDs,
			"deleted_users", deletedUsers)
	}

	return nil
}
