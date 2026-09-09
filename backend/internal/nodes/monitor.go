package users

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"exodus/internal/config"
	"exodus/internal/db"
	"exodus/internal/nodehotcache"
	"exodus/internal/scheduler"
)

// NodeMonitor dynamically manages node monitoring with status tracking.
type NodeMonitor struct {
	db  *sql.DB
	cfg *config.BackendConfig

	// Active node contexts
	nodes     map[string]*nodeState
	nodesLock sync.RWMutex

	// Global context for shutdown
	globalCtx    context.Context
	globalCancel context.CancelFunc

	// Manual sync trigger
	syncNow chan struct{}
	// Manual deploy trigger
	deployNow chan deployRequest
	// Runtime traffic metrics snapshots by node UUID.
	metricsByNodeUUID map[string]*NodeMetricsSnapshot
	metricsLock       sync.RWMutex

	usageRecorder NodeUserUsageRecorder
	hotCache      *nodehotcache.Cache
	statusLock    sync.Mutex
	nodeMetaCache sync.Map
}

type nodeMetadata struct {
	UUID                      string
	ID                        int64
	ConsumptionMultiplier     int64
	NodeConsumptionMultiplier int64
}

func (nm *NodeMonitor) getNodeMetadata(nodeName string) (string, int64, int64, int64, error) {
	if val, ok := nm.nodeMetaCache.Load(nodeName); ok {
		meta := val.(nodeMetadata)
		return meta.UUID, meta.ID, meta.ConsumptionMultiplier, meta.NodeConsumptionMultiplier, nil
	}

	var meta nodeMetadata
	if err := nm.db.QueryRow(`SELECT uuid, id, consumption_multiplier, node_consumption_multiplier FROM nodes WHERE name = $1`, nodeName).Scan(
		&meta.UUID, &meta.ID, &meta.ConsumptionMultiplier, &meta.NodeConsumptionMultiplier,
	); err != nil {
		return "", 0, 0, 0, err
	}

	nm.nodeMetaCache.Store(nodeName, meta)
	return meta.UUID, meta.ID, meta.ConsumptionMultiplier, meta.NodeConsumptionMultiplier, nil
}

// NewNodeMonitor creates a new NodeMonitor.
func NewNodeMonitor(db *sql.DB, cfg *config.BackendConfig) *NodeMonitor {
	return &NodeMonitor{
		db:                db,
		cfg:               cfg,
		nodes:             make(map[string]*nodeState),
		syncNow:           make(chan struct{}, 1),
		deployNow:         make(chan deployRequest, 1),
		metricsByNodeUUID: make(map[string]*NodeMetricsSnapshot),
		hotCache:          nodehotcache.Default(cfg),
	}
}

func (nm *NodeMonitor) SetNodeUserUsageRecorder(recorder NodeUserUsageRecorder) {
	nm.usageRecorder = recorder
}

// Start begins the node monitoring loop.
func (nm *NodeMonitor) Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	// Store global context
	nm.globalCtx = ctx

	// Initialize cancel function for Stop()
	nm.globalCancel = func() {
		// Cancel all node contexts
		nm.nodesLock.RLock()
		defer nm.nodesLock.RUnlock()
		for _, state := range nm.nodes {
			state.cancel()
		}
	}

	// Initial load and start
	nm.cfg.Logger.Trace("Node monitor initial sync")
	nm.syncNodes()

	// Periodic sync every 30 seconds
	syncTicker := time.NewTicker(scheduler.RecordNodeUsageInterval)
	defer syncTicker.Stop()

	// Periodic watchdog for disconnected active nodes every NodeHealthCheckInterval (10 seconds)
	watchdogTicker := time.NewTicker(scheduler.NodeHealthCheckInterval)
	defer watchdogTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			nm.cfg.Logger.Info("Node monitor stopping")
			nm.stopAll()
			return
		case <-nm.syncNow:
			nm.cfg.Logger.Debug("Node monitor manual sync requested")
			nm.syncNodes()
		case deployReq := <-nm.deployNow:
			nm.cfg.Logger.Debug(
				"Node monitor deploy requested",
				"restart", deployReq.Restart,
				"force_restart", deployReq.ForceRestart,
				"node_targets", len(deployReq.NodeUUIDs),
			)
			nm.deployToConnectedNodes(deployReq.Restart, deployReq.ForceRestart, deployReq.NodeUUIDs)
		case <-syncTicker.C:
			nm.syncNodes()
		case <-watchdogTicker.C:
			nm.retryFailedNodes()
		}
	}
}

// retryFailedNodes automatically re-attempts deploy for active nodes whose core failed to start or is disconnected.
func (nm *NodeMonitor) retryFailedNodes() {
	if nm == nil {
		return
	}

	ctx := nm.globalCtx
	if ctx == nil {
		ctx = context.Background()
	}

	rows, err := nm.db.QueryContext(ctx, `SELECT uuid::text, name FROM nodes WHERE is_disabled = false AND is_connected = false`)
	if err != nil {
		nm.cfg.Logger.Debug("Failed to query disconnected nodes for watchdog retry", "error", err)
		return
	}
	defer rows.Close()

	failedNodes := make(map[string]string)
	for rows.Next() {
		var uuid, name string
		if scanErr := rows.Scan(&uuid, &name); scanErr == nil {
			failedNodes[name] = uuid
		}
	}
	if err := rows.Err(); err != nil {
		nm.cfg.Logger.Debug("Error iterating disconnected nodes", "error", err)
	}
	if len(failedNodes) == 0 {
		return
	}

	failedTargets := make([]string, 0, len(failedNodes))
	nm.nodesLock.RLock()
	for name, uuid := range failedNodes {
		state, ok := nm.nodes[name]
		if !ok || state == nil {
			continue
		}
		state.mutex.RLock()
		hasClient := state.client != nil
		state.mutex.RUnlock()

		if hasClient {
			failedTargets = append(failedTargets, uuid)
		}
	}
	nm.nodesLock.RUnlock()

	if len(failedTargets) > 0 {
		nm.cfg.Logger.Debug("Node watchdog retrying deploy for active disconnected nodes", "count", len(failedTargets), "node_uuids", failedTargets)
		nm.deployToConnectedNodes(true, false, failedTargets)
	}
}

// syncNodes synchronizes monitored nodes with database.
func (nm *NodeMonitor) syncNodes() {
	// Load active nodes from DB
	dbNodes, err := nm.loadActiveNodes()
	if err != nil {
		nm.cfg.Logger.Warn("Failed to load nodes from DB", "error", err)
		return
	}

	// Build desired state
	desired := make(map[string]db.DBNode)
	for _, n := range dbNodes {
		desired[n.Name] = n
	}
	nm.cfg.Logger.Debug("Node monitor sync complete", "nodes", len(desired))

	nm.nodesLock.Lock()
	defer nm.nodesLock.Unlock()

	toStart := make(map[string]db.DBNode)

	// Stop removed or changed nodes
	for name, state := range nm.nodes {
		desiredNode, exists := desired[name]
		if !exists {
			nm.cfg.Logger.Debug("Node removed from DB, stopping monitor", "node", name)
			state.cancel()
			if state.conn != nil {
				state.conn.Close()
			}
			if state.nodeUUID != "" {
				nm.removeNodeMetrics(state.nodeUUID)
			}
			nm.nodeMetaCache.Delete(name)
			delete(nm.nodes, name)
			continue
		}
		if nodeConfigChanged(state, desiredNode) {
			nm.cfg.Logger.Info(
				"Node config changed, restarting monitor",
				"node", name,
				"old_address", state.address, "new_address", desiredNode.Address,
				"old_port", state.port, "new_port", desiredNode.Port,
				"old_proxy_url", state.proxyURL, "new_proxy_url", desiredNode.ProxyURL,
				"old_schema", state.apiSchema, "new_schema", desiredNode.APISchema,
				"old_path", state.apiPath, "new_path", desiredNode.APIPath,
			)
			state.cancel()
			if state.conn != nil {
				state.conn.Close()
			}
			nm.nodeMetaCache.Delete(name)
			delete(nm.nodes, name)
			toStart[name] = desiredNode
		}
	}

	// Start new nodes
	for name, dbNode := range desired {
		if _, exists := nm.nodes[name]; !exists {
			toStart[name] = dbNode
		}
	}

	for _, dbNode := range toStart {
		nm.startNode(dbNode)
	}
}

// loadActiveNodes loads enabled nodes from database.
func (nm *NodeMonitor) loadActiveNodes() ([]db.DBNode, error) {
	ctx := nm.globalCtx
	if ctx == nil {
		ctx = context.Background()
	}
	nodes, err := db.LoadNodesFromDB(ctx, nm.db, nm.cfg)
	if err != nil {
		return nil, err
	}

	for i := range nodes {
		nodes[i].APISchema = normalizeNodeSchema(nodes[i].APISchema)
		nodes[i].APIPath = normalizeNodePath(nodes[i].APIPath)
	}
	return nodes, nil
}

// startNode starts monitoring a single node.
func (nm *NodeMonitor) startNode(dbNode db.DBNode) {
	ctx, cancel := context.WithCancel(nm.globalCtx)

	state := &nodeState{
		nodeUUID:      dbNode.UUID,
		nodeName:      dbNode.Name,
		address:       dbNode.Address,
		port:          dbNode.Port,
		proxyURL:      dbNode.ProxyURL,
		apiSchema:     dbNode.APISchema,
		apiPath:       dbNode.APIPath,
		grpcAuthToken: dbNode.GRPCAuthToken,
		ctx:           ctx,
		cancel:        cancel,
	}

	nm.nodes[dbNode.Name] = state

	// Mark as connecting in DB
	nm.updateConnectionStatus(dbNode.Name, false, true, "")

	go nm.monitorNode(state)

	nm.cfg.Logger.Info("Started monitoring node", "node", dbNode.Name, "address", dbNode.Address, "port", dbNode.Port, "schema", dbNode.APISchema, "path", dbNode.APIPath)
}

func (nm *NodeMonitor) Stop() {
	if nm != nil && nm.globalCancel != nil {
		nm.globalCancel()
	}
}

// stopAll cancels and stops all connections.
func (nm *NodeMonitor) stopAll() {
	nm.nodesLock.Lock()
	defer nm.nodesLock.Unlock()

	for name, state := range nm.nodes {
		nm.cfg.Logger.Trace("Canceling node monitor context", "node", name)
		state.cancel()
		if state.conn != nil {
			state.conn.Close()
		}
		delete(nm.nodes, name)
	}
}

// RefreshNodes manually triggers a sync loop.
func (nm *NodeMonitor) RefreshNodes() {
	select {
	case nm.syncNow <- struct{}{}:
	default:
	}
}

// IsNodeConnected checks if a node is connected.
func (nm *NodeMonitor) IsNodeConnected(nodeName string) bool {
	nm.nodesLock.RLock()
	defer nm.nodesLock.RUnlock()

	state, exists := nm.nodes[nodeName]
	if !exists || state == nil {
		return false
	}

	state.mutex.RLock()
	defer state.mutex.RUnlock()
	return state.isConnected
}

// GetMetricsSnapshot returns metrics for a node.
func (nm *NodeMonitor) GetMetricsSnapshot(nodeUUID string) *NodeMetricsSnapshot {
	nm.metricsLock.RLock()
	defer nm.metricsLock.RUnlock()
	return nm.metricsByNodeUUID[nodeUUID]
}

func (nm *NodeMonitor) updateNodeMetricsSnapshot(nodeUUID string, usersOnline int, delta trafficStatsDelta) {
	if nm == nil || strings.TrimSpace(nodeUUID) == "" {
		return
	}

	nm.metricsLock.Lock()
	defer nm.metricsLock.Unlock()

	snapshot, exists := nm.metricsByNodeUUID[nodeUUID]
	if !exists || snapshot == nil {
		snapshot = &NodeMetricsSnapshot{
			NodeUUID:  nodeUUID,
			Inbounds:  make(map[string]TagTrafficCounters),
			Outbounds: make(map[string]TagTrafficCounters),
		}
		nm.metricsByNodeUUID[nodeUUID] = snapshot
	}

	snapshot.UsersOnline = usersOnline
	snapshot.UpdatedAt = time.Now().UTC()

	for tag, item := range delta.InboundByTag {
		current := snapshot.Inbounds[tag]
		current.UploadBytes += item.UploadBytes
		current.DownloadBytes += item.DownloadBytes
		snapshot.Inbounds[tag] = current
	}

	for tag, item := range delta.OutboundByTag {
		current := snapshot.Outbounds[tag]
		current.UploadBytes += item.UploadBytes
		current.DownloadBytes += item.DownloadBytes
		snapshot.Outbounds[tag] = current
	}
}

func (nm *NodeMonitor) removeNodeMetrics(nodeUUID string) {
	if nm == nil || strings.TrimSpace(nodeUUID) == "" {
		return
	}
	nm.metricsLock.Lock()
	defer nm.metricsLock.Unlock()
	delete(nm.metricsByNodeUUID, nodeUUID)
}

func (nm *NodeMonitor) SnapshotNodeMetrics() map[string]NodeMetricsSnapshot {
	if nm == nil {
		return map[string]NodeMetricsSnapshot{}
	}

	nm.metricsLock.RLock()
	defer nm.metricsLock.RUnlock()

	result := make(map[string]NodeMetricsSnapshot, len(nm.metricsByNodeUUID))
	for nodeUUID, source := range nm.metricsByNodeUUID {
		if source == nil {
			continue
		}

		copySnapshot := NodeMetricsSnapshot{
			NodeUUID:    source.NodeUUID,
			UsersOnline: source.UsersOnline,
			Inbounds:    make(map[string]TagTrafficCounters, len(source.Inbounds)),
			Outbounds:   make(map[string]TagTrafficCounters, len(source.Outbounds)),
			UpdatedAt:   source.UpdatedAt,
		}
		for tag, item := range source.Inbounds {
			copySnapshot.Inbounds[tag] = item
		}
		for tag, item := range source.Outbounds {
			copySnapshot.Outbounds[tag] = item
		}

		result[nodeUUID] = copySnapshot
	}
	return result
}

func (nm *NodeMonitor) RequestSync() {
	if nm == nil {
		return
	}
	select {
	case nm.syncNow <- struct{}{}:
	default:
	}
}

func (nm *NodeMonitor) RequestDeploy(restart bool, nodeUUIDs ...string) {
	nm.RequestDeployWithForce(restart, false, nodeUUIDs...)
}

func (nm *NodeMonitor) RequestDeployWithForce(restart bool, forceRestart bool, nodeUUIDs ...string) {
	if nm == nil {
		return
	}
	normalizedTargets := normalizeNodeUUIDTargets(nodeUUIDs)
	req := deployRequest{
		Restart:      restart,
		ForceRestart: forceRestart,
		NodeUUIDs:    normalizedTargets,
	}
	if nm.cfg != nil && nm.cfg.Logger != nil {
		nm.cfg.Logger.Debug("Node deploy requested", "restart", restart, "force_restart", forceRestart, "node_targets", len(normalizedTargets))
	}
	select {
	case nm.deployNow <- req:
		if nm.cfg != nil && nm.cfg.Logger != nil {
			nm.cfg.Logger.Debug("Node deploy queue accepted request", "restart", restart, "force_restart", forceRestart, "node_targets", len(normalizedTargets))
		}
	default:
		// Drain and replace pending
		select {
		case <-nm.deployNow:
		default:
		}
		nm.deployNow <- req
		if nm.cfg != nil && nm.cfg.Logger != nil {
			nm.cfg.Logger.Debug("Node deploy queue replaced previous pending request", "restart", restart, "force_restart", forceRestart, "node_targets", len(normalizedTargets))
		}
	}
}

func normalizeNodeUUIDTargets(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		uuid := strings.TrimSpace(item)
		if uuid == "" {
			continue
		}
		if _, exists := seen[uuid]; exists {
			continue
		}
		seen[uuid] = struct{}{}
		result = append(result, uuid)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func nodeConfigChanged(state *nodeState, desired db.DBNode) bool {
	if state.address != desired.Address {
		return true
	}
	if state.port != desired.Port {
		return true
	}
	if strings.TrimSpace(state.proxyURL) != strings.TrimSpace(desired.ProxyURL) {
		return true
	}
	if normalizeNodeSchema(state.apiSchema) != normalizeNodeSchema(desired.APISchema) {
		return true
	}
	if normalizeNodePath(state.apiPath) != normalizeNodePath(desired.APIPath) {
		return true
	}
	if strings.TrimSpace(state.grpcAuthToken) != strings.TrimSpace(desired.GRPCAuthToken) {
		return true
	}
	return false
}
