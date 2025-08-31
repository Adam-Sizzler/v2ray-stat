package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"v2ray-stat/backend/config"
	"v2ray-stat/backend/db/manager"
)

// IdEntry представляет одиночную пару ID и inbound_tag для пользователя.
type IdEntry struct {
	InboundTag string `json:"inbound_tag"`
	Id         string `json:"id"`
}

// User представляет сущность пользователя из таблиц user_traffic, user_data и user_ids.
type User struct {
	User       string    `json:"user"`
	Inbounds   []IdEntry `json:"inbounds"`
	Rate       string    `json:"rate"`
	Enabled    string    `json:"enabled"`
	Created    int64     `json:"created"`
	SubEnd     int64     `json:"sub_end"`
	Renew      int       `json:"renew"`
	LimIp      int       `json:"lim_ip"`
	Ips        string    `json:"ips"`
	Uplink     int64     `json:"uplink"`
	Downlink   int64     `json:"downlink"`
	TrafficCap int64     `json:"traffic_cap"`
}

// NodeUsers представляет пользователей, сгруппированных по ноде, с адресом и портом ноды.
type NodeUsers struct {
	Node    string `json:"node_name"`
	Address string `json:"address"`
	Port    string `json:"port"`
	Users   []User `json:"users"`
}

// UsersHandler returns a list of users grouped by node from the database in JSON format.
func UsersHandler(manager *manager.DatabaseManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Начало обработки запроса UsersHandler")

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		if r.Method != http.MethodGet {
			cfg.Logger.Warn("Недопустимый HTTP-метод", "method", r.Method)
			http.Error(w, "Недопустимый метод. Используйте GET", http.StatusMethodNotAllowed)
			return
		}

		nodeUsers, err := queryUsers(r.Context(), manager, cfg)
		if err != nil {
			cfg.Logger.Error("Ошибка в UsersHandler", "error", err)
			http.Error(w, "Ошибка обработки данных", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Debug("Кодирование ответа в JSON", "nodes_count", len(nodeUsers))
		if err := json.NewEncoder(w).Encode(nodeUsers); err != nil {
			cfg.Logger.Error("Не удалось закодировать JSON", "error", err)
			http.Error(w, "Ошибка формирования ответа", http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API users: успешно завершено", "nodes_count", len(nodeUsers))
	}
}

// queryUsers выполняет запрос к базе данных для получения пользователей и возвращает NodeUsers.
func queryUsers(ctx context.Context, manager *manager.DatabaseManager, cfg *config.Config) ([]NodeUsers, error) {
	var nodeUsers []NodeUsers
	err := manager.ExecuteLowPriority(func(db *sql.DB) error {
		cfg.Logger.Debug("Выполнение запроса к таблицам user_traffic, user_data, user_ids и nodes")

		query := `
			SELECT ut.node_name, n.address, n.port, ut.user, uu.id, uu.inbound_tag, ut.rate, ut.enabled, ut.created, ud.sub_end, ud.renew, ud.lim_ip, ud.ips, ut.uplink, ut.downlink, ud.traffic_cap
			FROM user_traffic ut
			LEFT JOIN user_data ud ON ut.user = ud.user
			LEFT JOIN user_ids uu ON ut.user = uu.user AND ut.node_name = uu.node_name
			LEFT JOIN nodes n ON ut.node_name = n.node_name
		`
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			cfg.Logger.Error("Не удалось выполнить SQL-запрос", "error", err)
			return fmt.Errorf("не удалось выполнить SQL-запрос: %v", err)
		}
		defer rows.Close()

		nodeMap := make(map[string]*NodeUsers)

		for rows.Next() {
			var nodeName, address, port, userName, id, inboundTag string
			var enabled, ips string
			var rate string
			var renew, limIp int
			var uplink, downlink, created, subEnd, trafficCap int64
			var idNull, inboundTagNull, enabledNull, ipsNull, addressNull, portNull sql.NullString

			if err := rows.Scan(
				&nodeName,
				&addressNull,
				&portNull,
				&userName,
				&idNull,
				&inboundTagNull,
				&rate,
				&enabledNull,
				&created,
				&subEnd,
				&renew,
				&limIp,
				&ipsNull,
				&uplink,
				&downlink,
				&trafficCap,
			); err != nil {
				cfg.Logger.Error("Не удалось прочитать строку", "error", err)
				return fmt.Errorf("не удалось прочитать строку: %v", err)
			}

			id = idNull.String
			inboundTag = inboundTagNull.String
			enabled = enabledNull.String
			ips = ipsNull.String
			address = addressNull.String
			port = portNull.String

			if _, exists := nodeMap[nodeName]; !exists {
				nodeMap[nodeName] = &NodeUsers{
					Node:    nodeName,
					Address: address,
					Port:    port,
					Users:   []User{},
				}
			}

			userFound := false
			for i, user := range nodeMap[nodeName].Users {
				if user.User == userName {
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

			if !userFound {
				user := User{
					User:       userName,
					Inbounds:   []IdEntry{},
					Rate:       rate,
					Enabled:    enabled,
					Created:    created,
					SubEnd:     subEnd,
					Renew:      renew,
					LimIp:      limIp,
					Ips:        ips,
					Uplink:     uplink,
					Downlink:   downlink,
					TrafficCap: trafficCap,
				}
				if id != "" && inboundTag != "" {
					user.Inbounds = append(user.Inbounds, IdEntry{
						InboundTag: inboundTag,
						Id:         id,
					})
				}
				nodeMap[nodeName].Users = append(nodeMap[nodeName].Users, user)
			}

			cfg.Logger.Trace("Прочитан пользователь", "node_name", nodeName, "user", userName, "id", id, "inbound_tag", inboundTag, "enabled", enabled)
		}
		if err := rows.Err(); err != nil {
			cfg.Logger.Error("Ошибка при итерации строк", "error", err)
			return fmt.Errorf("ошибка при итерации строк: %v", err)
		}

		for _, node := range nodeMap {
			nodeUsers = append(nodeUsers, *node)
		}

		if len(nodeUsers) == 0 {
			cfg.Logger.Warn("Пользователи не найдены в таблицах user_traffic, user_data, user_ids и nodes")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodeUsers, nil
}
