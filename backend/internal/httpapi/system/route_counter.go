package system

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"exodus/internal/config"

	"github.com/redis/go-redis/v9"
)

const (
	redisRouteCounterKey = "route_counter:stats"
	flushIntervalMs      = 5 * time.Second
)

type RouteStatItem struct {
	Method string `json:"method"`
	Route  string `json:"route"`
	Count  int64  `json:"count"`
}

type RouteStatsResponse struct {
	Routes []RouteStatItem `json:"routes"`
	Total  int64           `json:"total"`
}

type RouteCounter struct {
	client     *redis.Client
	cfg        *config.BackendConfig
	mu         sync.RWMutex
	keys       []string
	counts     []int64
	slotByKey  map[string]int
	slotCache  sync.Map
	isFlushing bool
	stopCh     chan struct{}
}

func NewRouteCounter(client *redis.Client, cfg *config.BackendConfig) *RouteCounter {
	return &RouteCounter{
		client:    client,
		cfg:       cfg,
		keys:      make([]string, 0),
		counts:    make([]int64, 0),
		slotByKey: make(map[string]int),
		stopCh:    make(chan struct{}),
	}
}

func (rc *RouteCounter) Start(ctx context.Context) {
	ticker := time.NewTicker(flushIntervalMs)
	go func() {
		for {
			select {
			case <-ticker.C:
				rc.flush(context.Background())
			case <-rc.stopCh:
				ticker.Stop()
				rc.flush(context.Background())
				return
			case <-ctx.Done():
				ticker.Stop()
				rc.flush(context.Background())
				return
			}
		}
	}()
}

func (rc *RouteCounter) Stop() {
	close(rc.stopCh)
}

func (rc *RouteCounter) Register(key string) int {
	if val, ok := rc.slotCache.Load(key); ok {
		return val.(int)
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()
	slot, exists := rc.slotByKey[key]
	if exists {
		rc.slotCache.Store(key, slot)
		return slot
	}

	slot = len(rc.keys)
	rc.keys = append(rc.keys, key)
	rc.counts = append(rc.counts, 0)
	rc.slotByKey[key] = slot
	rc.slotCache.Store(key, slot)
	return slot
}

func (rc *RouteCounter) Increment(key string) {
	slot := rc.Register(key)
	atomic.AddInt64(&rc.counts[slot], 1)
}

func (rc *RouteCounter) GetStats(ctx context.Context) RouteStatsResponse {
	if rc.client != nil {
		hash, err := rc.client.HGetAll(ctx, redisRouteCounterKey).Result()
		if err == nil && len(hash) > 0 {
			var total int64
			routes := make([]RouteStatItem, 0, len(hash))
			for key, valStr := range hash {
				count, parseErr := strconv.ParseInt(valStr, 10, 64)
				if parseErr != nil {
					continue
				}
				spaceIdx := strings.IndexByte(key, ' ')
				var method, route string
				if spaceIdx == -1 {
					method = ""
					route = key
				} else {
					method = key[:spaceIdx]
					route = key[spaceIdx+1:]
				}
				routes = append(routes, RouteStatItem{
					Method: method,
					Route:  route,
					Count:  count,
				})
				total += count
			}

			sort.Slice(routes, func(i, j int) bool {
				return routes[i].Count > routes[j].Count
			})

			return RouteStatsResponse{
				Routes: routes,
				Total:  total,
			}
		}
	}

	rc.mu.RLock()
	defer rc.mu.RUnlock()

	var total int64
	routes := make([]RouteStatItem, 0, len(rc.keys))
	for i, key := range rc.keys {
		count := atomic.LoadInt64(&rc.counts[i])
		if count > 0 {
			spaceIdx := strings.IndexByte(key, ' ')
			var method, route string
			if spaceIdx == -1 {
				method = ""
				route = key
			} else {
				method = key[:spaceIdx]
				route = key[spaceIdx+1:]
			}
			routes = append(routes, RouteStatItem{
				Method: method,
				Route:  route,
				Count:  count,
			})
			total += count
		}
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Count > routes[j].Count
	})

	return RouteStatsResponse{
		Routes: routes,
		Total:  total,
	}
}

func (rc *RouteCounter) flush(ctx context.Context) {
	if rc.isFlushing {
		return
	}
	rc.isFlushing = true
	defer func() {
		rc.isFlushing = false
	}()

	rc.mu.RLock()
	numSlots := len(rc.counts)
	rc.mu.RUnlock()

	type drainedItem struct {
		slot  int
		field string
		delta int64
	}

	drained := make([]drainedItem, 0)
	for i := 0; i < numSlots; i++ {
		delta := atomic.SwapInt64(&rc.counts[i], 0)
		if delta > 0 {
			rc.mu.RLock()
			field := rc.keys[i]
			rc.mu.RUnlock()
			drained = append(drained, drainedItem{slot: i, field: field, delta: delta})
		}
	}

	if len(drained) == 0 {
		return
	}

	if rc.client == nil {
		return
	}

	pipe := rc.client.Pipeline()
	for _, item := range drained {
		pipe.HIncrBy(ctx, redisRouteCounterKey, item.field, item.delta)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		for _, item := range drained {
			atomic.AddInt64(&rc.counts[item.slot], item.delta)
		}
		if rc.cfg != nil && rc.cfg.Logger != nil {
			rc.cfg.Logger.Warn("Failed to flush route counters to Redis", "error", err)
		}
	}
}

func Middleware(rc *RouteCounter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if rc != nil && rc.cfg != nil {
				path = strings.TrimPrefix(path, rc.cfg.Backend.Trimmed())
			}
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if strings.HasPrefix(path, "/api/") {
				key := r.Method + " " + path
				rc.Increment(key)
			}
			next.ServeHTTP(w, r)
		})
	}
}
