package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"v2ray-stat/subscription/config"
)

// UserID represents a single user ID entry from the API.
type UserID struct {
	NodeName   string `json:"node_name"`
	User       string `json:"user"`
	ID         string `json:"id"`
	SubEnd     int64  `json:"sub_end"`
	InboundTag string `json:"inbound_tag"`
	Uplink     int64  `json:"uplink"`
	Downlink   int64  `json:"downlink"`
	TrafficCap int64  `json:"traffic_cap"`
}

// NodeUser represents a user within a node in the API response.
type NodeUser struct {
	User       string    `json:"user"`
	Inbounds   []Inbound `json:"inbounds"`
	Uplink     int64     `json:"uplink"`
	Downlink   int64     `json:"downlink"`
	SubEnd     int64     `json:"sub_end"`
	TrafficCap int64     `json:"traffic_cap"`
}

// Inbound represents an inbound configuration for a user.
type Inbound struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
}

// Node represents a node in the API response.
type Node struct {
	NodeName string     `json:"node_name"`
	Address  string     `json:"address"`
	Users    []NodeUser `json:"users"`
}

// BackendResponse represents the structure of /api/v1/users response.
type BackendResponse []Node

var (
	cachedResponse BackendResponse
	cacheMutex     sync.RWMutex
)

// GetUserIDs queries the backend API for user IDs and traffic data.
func GetUserIDs(cfg *config.Config, user string) ([]UserID, error) {
	cfg.Logger.Trace("Querying backend API for user IDs", "user", user)
	resp, err := http.Get("http://localhost:9952/api/v1/users")
	if err != nil {
		cfg.Logger.Error("Failed to query backend API", "error", err)
		// fallback to cache
		cacheMutex.RLock()
		if len(cachedResponse) > 0 {
			defer cacheMutex.RUnlock()
			cfg.Logger.Debug("Using cached response due to query failure", "user", user)
			return filterUserIDs(cachedResponse, user), nil
		}
		cacheMutex.RUnlock()
		return nil, fmt.Errorf("failed to query /api/v1/users and no cache available: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cfg.Logger.Error("Unexpected status code from backend API", "status", resp.StatusCode)
		cacheMutex.RLock()
		if len(cachedResponse) > 0 {
			defer cacheMutex.RUnlock()
			cfg.Logger.Debug("Using cached response due to non-200 status", "user", user)
			return filterUserIDs(cachedResponse, user), nil
		}
		cacheMutex.RUnlock()
		return nil, fmt.Errorf("unexpected status code from /api/v1/users and no cache available: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		cfg.Logger.Error("Failed to read backend response body", "error", err)
		// Try to return cached response if available
		cacheMutex.RLock()
		if len(cachedResponse) > 0 {
			defer cacheMutex.RUnlock()
			cfg.Logger.Debug("Using cached response due to read failure", "user", user)
			return filterUserIDs(cachedResponse, user), nil
		}
		cacheMutex.RUnlock()
		return nil, fmt.Errorf("failed to read /api/v1/users response and no cache available: %w", err)
	}

	var backendResponse BackendResponse
	if err := json.Unmarshal(body, &backendResponse); err != nil {
		cfg.Logger.Error("Failed to parse backend response", "error", err)
		// Try to return cached response if available
		cacheMutex.RLock()
		if len(cachedResponse) > 0 {
			defer cacheMutex.RUnlock()
			cfg.Logger.Debug("Using cached response due to parse failure", "user", user)
			return filterUserIDs(cachedResponse, user), nil
		}
		cacheMutex.RUnlock()
		return nil, fmt.Errorf("failed to parse /api/v1/users response and no cache available: %w", err)
	}

	cacheMutex.Lock()
	cachedResponse = backendResponse
	cacheMutex.Unlock()
	cfg.Logger.Debug("Updated cache with new backend response")

	userIDs := filterUserIDs(backendResponse, user)
	cfg.Logger.Trace("Filtered user IDs", "user", user, "count", len(userIDs))
	return userIDs, nil
}

// filterUserIDs extracts UserID entries for a specific user from a BackendResponse.
func filterUserIDs(backendResponse BackendResponse, user string) []UserID {
	var userIDs []UserID
	for _, node := range backendResponse {
		for _, u := range node.Users {
			if u.User == user {
				for _, inbound := range u.Inbounds {
					userIDs = append(userIDs, UserID{
						NodeName:   node.NodeName,
						User:       u.User,
						ID:         inbound.ID,
						InboundTag: inbound.InboundTag,
						SubEnd:     u.SubEnd,
						Uplink:     u.Uplink,
						Downlink:   u.Downlink,
						TrafficCap: u.TrafficCap,
					})
				}
			}
		}
	}

	return userIDs
}
