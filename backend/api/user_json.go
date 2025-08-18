package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// IdEntry represents a single ID and inbound_tag pair for a user.
type IdEntry struct {
	InboundTag string `json:"inbound_tag"`
	Id         string `json:"id"`
}

// User represents a user entity from the user_traffic, user_data, and user_ids tables.
type User struct {
	User         string    `json:"user"`
	Inbounds     []IdEntry `json:"inbounds"`
	Rate         string    `json:"rate"`
	Enabled      string    `json:"enabled"`
	Created      string    `json:"created"`
	SubEnd       string    `json:"sub_end"`
	Renew        int       `json:"renew"`
	LimIp        int       `json:"lim_ip"`
	Ips          string    `json:"ips"`
	Uplink       int64     `json:"uplink"`
	Downlink     int64     `json:"downlink"`
	SessUplink   int64     `json:"sess_uplink"`
	SessDownlink int64     `json:"sess_downlink"`
}

// NodeUsers represents users grouped by node with the node's address.
type NodeUsers struct {
	Node    string `json:"node_name"`
	Address string `json:"address"`
	Users   []User `json:"users"`
}

// UsersHandler returns a list of users grouped by node from the database in JSON format.
func UsersHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting UsersHandler request processing")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Invalid method. Use GET", http.StatusMethodNotAllowed)
			return
		}

		var nodeUsers []NodeUsers
		err := manager.ExecuteLowPriority(func(db *sql.DB) error {
			cfg.Logger.Debug("Executing query on user_traffic, user_data, user_ids, and nodes tables")

			// Первый запрос: получаем пользователей и их данные
			query := `
				SELECT ut.node_name, n.address, ut.user, uu.id, uu.inbound_tag, ut.rate, ut.enabled, ut.created, ud.sub_end, ud.renew, ud.lim_ip, ud.ips, ut.uplink, ut.downlink, ut.sess_uplink, ut.sess_downlink
				FROM user_traffic ut
				LEFT JOIN user_data ud ON ut.user = ud.user
				LEFT JOIN user_ids uu ON ut.user = uu.user AND ut.node_name = uu.node_name
				LEFT JOIN nodes n ON ut.node_name = n.node_name
			`
			rows, err := db.Query(query)
			if err != nil {
				cfg.Logger.Error("Failed to execute SQL query", "error", err)
				return fmt.Errorf("failed to execute SQL query: %v", err)
			}
			defer rows.Close()

			// Временная мапа для группировки пользователей по нодам
			nodeMap := make(map[string]*NodeUsers)

			for rows.Next() {
				var nodeName, address, userName, id, inboundTag string
				var enabled, subEnd, ips string
				var rate string
				var renew, limIp int
				var uplink, downlink, sessUplink, sessDownlink int64
				var idNull, inboundTagNull, enabledNull, created, subEndNull, ipsNull, addressNull sql.NullString

				if err := rows.Scan(
					&nodeName,
					&addressNull,
					&userName,
					&idNull,
					&inboundTagNull,
					&rate,
					&enabledNull,
					&created,
					&subEndNull,
					&renew,
					&limIp,
					&ipsNull,
					&uplink,
					&downlink,
					&sessUplink,
					&sessDownlink,
				); err != nil {
					cfg.Logger.Error("Failed to scan row", "error", err)
					return fmt.Errorf("failed to scan row: %v", err)
				}

				// Устанавливаем значения, учитывая NULL
				id = idNull.String
				inboundTag = inboundTagNull.String
				enabled = enabledNull.String
				subEnd = subEndNull.String
				ips = ipsNull.String
				address = addressNull.String

				// Инициализируем NodeUsers, если нода ещё не добавлена
				if _, exists := nodeMap[nodeName]; !exists {
					nodeMap[nodeName] = &NodeUsers{
						Node:    nodeName,
						Address: address,
						Users:   []User{},
					}
				}

				// Ищем существующего пользователя в nodeMap
				userFound := false
				for i, user := range nodeMap[nodeName].Users {
					if user.User == userName {
						// Добавляем новую пару id и inbound_tag, если id не пустой
						if id != "" && inboundTag != "" {
							nodeMap[nodeName].Users[i].Inbounds = append(nodeMap[nodeName].Users[i].Inbounds, IdEntry{
								InboundTag: inboundTag,
								Id:         id,
							})
						}
						userFound = true
						break
					}
				}

				// Если пользователь не найден, создаём новую запись
				if !userFound {
					user := User{
						User:         userName,
						Inbounds:     []IdEntry{},
						Rate:         rate,
						Enabled:      enabled,
						Created:      created.String,
						SubEnd:       subEnd,
						Renew:        renew,
						LimIp:        limIp,
						Ips:          ips,
						Uplink:       uplink,
						Downlink:     downlink,
						SessUplink:   sessUplink,
						SessDownlink: sessDownlink,
					}
					// Добавляем пару id и inbound_tag, если они не пустые
					if id != "" && inboundTag != "" {
						user.Inbounds = append(user.Inbounds, IdEntry{
							InboundTag: inboundTag,
							Id:         id,
						})
					}
					nodeMap[nodeName].Users = append(nodeMap[nodeName].Users, user)
				}

				cfg.Logger.Trace("Read user", "node_name", nodeName, "user", userName, "id", id, "inbound_tag", inboundTag, "enabled", enabled)
			}
			if err := rows.Err(); err != nil {
				cfg.Logger.Error("Error iterating rows", "error", err)
				return fmt.Errorf("error iterating rows: %v", err)
			}

			// Преобразуем мапу в слайс NodeUsers
			for _, node := range nodeMap {
				nodeUsers = append(nodeUsers, *node)
			}

			if len(nodeUsers) == 0 {
				cfg.Logger.Warn("No users found in user_traffic, user_data, user_ids, and nodes tables")
			}
			return nil
		})
		if err != nil {
			cfg.Logger.Error("Error in UsersHandler", "error", err)
			http.Error(w, "Error processing data", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Encoding response to JSON", "nodes_count", len(nodeUsers))
		if err := json.NewEncoder(w).Encode(nodeUsers); err != nil {
			cfg.Logger.Error("Failed to encode JSON", "error", err)
			http.Error(w, "Error forming response", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API users: completed successfully", "nodes_count", len(nodeUsers))
	}
}
