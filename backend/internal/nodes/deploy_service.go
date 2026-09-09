package users

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"exodus/internal/logger"
	"exodus/internal/proto"

	"google.golang.org/grpc/codes"
)

func (nm *NodeMonitor) deployToConnectedNodes(restart bool, forceRestart bool, requestedNodeUUIDs []string) {
	if nm == nil {
		return
	}
	nm.cfg.Logger.Debug("Deploying node configs", "restart", restart, "force_restart", forceRestart, "requested_node_targets", len(requestedNodeUUIDs))

	dbNodes, err := nm.loadActiveNodes()
	if err != nil {
		nm.cfg.Logger.Warn("Failed to load nodes for deploy", "error", err)
		return
	}

	nodesByName := make(map[string]string, len(dbNodes))
	for _, n := range dbNodes {
		nodesByName[n.Name] = n.UUID
	}

	targetFilter := make(map[string]struct{}, len(requestedNodeUUIDs))
	for _, nodeUUID := range requestedNodeUUIDs {
		trimmed := strings.TrimSpace(nodeUUID)
		if trimmed == "" {
			continue
		}
		targetFilter[trimmed] = struct{}{}
	}

	targets := make([]deployTarget, 0)
	nm.nodesLock.RLock()
	for nodeName, state := range nm.nodes {
		if state == nil {
			continue
		}
		state.mutex.RLock()
		client := state.client
		state.mutex.RUnlock()
		if client == nil {
			continue
		}
		nodeUUID, ok := nodesByName[nodeName]
		if !ok {
			continue
		}
		if len(targetFilter) > 0 {
			if _, allowed := targetFilter[nodeUUID]; !allowed {
				continue
			}
		}
		targets = append(targets, deployTarget{
			name:   nodeName,
			uuid:   nodeUUID,
			client: client,
		})
	}
	nm.nodesLock.RUnlock()
	nm.cfg.Logger.Debug("Prepared deploy targets", "active_nodes", len(dbNodes), "connected_targets", len(targets), "requested_node_targets", len(targetFilter), "restart", restart, "force_restart", forceRestart)

	if len(targets) == 0 {
		nm.cfg.Logger.Warn("No connected nodes to deploy")
		return
	}

	// Node-independent data: identical for every target in this deploy cycle,
	// so it's loaded once instead of once per node (was a per-target N+1).
	sharedLists := nm.loadSharedLists(nm.globalCtx)

	batchStart := time.Now()
	var lastProfileUUID string

	for _, target := range targets {
		start := time.Now()
		configJSON, profileUUID, err := nm.buildNodeConfigForDeploy(nm.globalCtx, target.uuid)
		if err != nil {
			nm.cfg.Logger.Warn("Failed to build node deploy config", "node", target.name, "node_uuid", target.uuid, "error", err)
			continue
		}
		if profileUUID != "" {
			lastProfileUUID = profileUUID
		}

		var parsedConfig struct {
			Inbounds []any `json:"inbounds"`
		}
		_ = json.Unmarshal(configJSON, &parsedConfig)
		inboundsCount := len(parsedConfig.Inbounds)
		nm.cfg.Logger.RoleService(logger.RoleWorkers, "StartAllNodesByProfileQueueProcessor").Info(fmt.Sprintf("Node %s has %d active inbounds.", target.uuid, inboundsCount))

		genDuration := time.Since(start).Milliseconds()
		nm.cfg.Logger.RoleService(logger.RoleWorkers, "StartAllNodesByProfileQueueProcessor").Info(fmt.Sprintf("Generated config for nodes by Profile in %dms", genDuration))

		pluginConfig, modulesErr := nm.loadNodePluginRuntimeConfig(nm.globalCtx, target.uuid)
		if modulesErr != nil {
			nm.cfg.Logger.Warn("Failed to load node plugin settings for deploy payload", "node", target.name, "node_uuid", target.uuid, "error", modulesErr)
		}
		haproxyInboundTags := normalizeHaproxyInboundTags(pluginConfig.HaproxyAuth.InboundTags)

		ingressIPs, ingressASNs := resolvePluginFilters(pluginConfig.IngressFilter.BlockedIPs, pluginConfig.IngressFilter.BlockedASNs, sharedLists)
		egressIPs, egressASNs := resolvePluginFilters(pluginConfig.EgressFilter.BlockedIPs, pluginConfig.EgressFilter.BlockedASNs, sharedLists)

		modules := &deployModulesTaskBlock{
			IngressFilter: deployIngressFilterBlock{
				Enabled:     pluginConfig.IngressFilter.Enabled,
				BlockedIPs:  ingressIPs,
				BlockedASNs: ingressASNs,
			},
			EgressFilter: deployEgressFilterBlock{
				Enabled:      pluginConfig.EgressFilter.Enabled,
				BlockedIPs:   egressIPs,
				BlockedPorts: normalizePortSlice(pluginConfig.EgressFilter.BlockedPorts),
				BlockedASNs:  egressASNs,
			},
		}
		if pluginConfig.PreStart.Enabled {
			modules.PreStart.Enabled = true
			if pluginConfig.PreStart.CleanupSockets.Enabled && len(pluginConfig.PreStart.CleanupSockets.Files) > 0 {
				modules.PreStart.CleanupSockets = &deployCleanupSocketsBlock{
					Enabled: true,
					Files:   normalizeStringSlice(pluginConfig.PreStart.CleanupSockets.Files),
				}
			}
		}
		if pluginConfig.HaproxyAuth.Enabled {
			haproxyUsers, haproxyEnabled, usersErr := nm.loadNodeHaproxyUsers(nm.globalCtx, target.uuid, haproxyInboundTags)
			if usersErr != nil {
				nm.cfg.Logger.Warn("Failed to load node users for HAPROXY payload", "node", target.name, "node_uuid", target.uuid, "error", usersErr)
			} else {
				modules.HaproxyEnabled = haproxyEnabled
				modules.HaproxyUsers = haproxyUsers
			}
		}

		// Payload compression measurements matching transport logging
		modulesStart := time.Now()
		modulesBytes, _ := json.Marshal(modules)
		rawModulesSize := float64(len(modulesBytes))
		compressedModulesSize := rawModulesSize
		var gzBuf bytes.Buffer
		gzWriter := gzip.NewWriter(&gzBuf)
		if _, err := gzWriter.Write(modulesBytes); err == nil {
			_ = gzWriter.Close()
			compressedModulesSize = float64(gzBuf.Len())
		}
		modulesDuration := time.Since(modulesStart).Milliseconds()
		nm.cfg.Logger.RoleService(logger.RoleWorkers, "AxiosService").Info(fmt.Sprintf("[ZSTD] [SYNC-NODE-PLUGINS] %dms | %.2f B -> %.2f B", modulesDuration, rawModulesSize, compressedModulesSize))

		configStart := time.Now()
		rawConfigSize := float64(len(configJSON))
		compressedConfigSize := rawConfigSize
		gzBuf.Reset()
		gzWriter = gzip.NewWriter(&gzBuf)
		if _, err := gzWriter.Write(configJSON); err == nil {
			_ = gzWriter.Close()
			compressedConfigSize = float64(gzBuf.Len())
		}
		configDuration := time.Since(configStart).Milliseconds()
		nm.cfg.Logger.RoleService(logger.RoleWorkers, "AxiosService").Info(fmt.Sprintf("[ZSTD] [START XRAY] %dms | %.2f B -> %.2f B", configDuration, rawConfigSize, compressedConfigSize))

		restartFlag := restart
		forceRestartFlag := forceRestart
		taskPayload, err := json.Marshal(deployTaskPayload{
			Config:       configJSON,
			Restart:      &restartFlag,
			ForceRestart: &forceRestartFlag,
			Modules:      modules,
		})
		if err != nil {
			nm.cfg.Logger.Warn("Failed to serialize deploy payload", "node", target.name, "error", err)
			continue
		}

		ctxBase := nm.globalCtx
		if ctxBase == nil {
			ctxBase = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctxBase, 60*time.Second)
		nm.cfg.Logger.Debug("Submitting deploy task", "node", target.name, "payload_bytes", len(taskPayload), "restart", restart, "force_restart", forceRestart)
		resp, err := target.client.SubmitTask(ctx, &proto.NodeTask{
			TaskId:    fmt.Sprintf("deploy-%d", time.Now().UnixNano()),
			Operation: "deploy_config",
			Payload:   taskPayload,
		})
		cancel()

		if err != nil {
			nm.cfg.Logger.Warn("Deploy task failed", "node", target.name, "error", err)
			nm.updateConnectionStatus(target.name, false, false, fmt.Sprintf("Deploy transport error: %v", err))
			continue
		}
		if resp == nil || resp.Code != int32(codes.OK) {
			if resp == nil {
				nm.cfg.Logger.Warn("Deploy task returned nil status", "node", target.name)
				nm.updateConnectionStatus(target.name, false, false, "Deploy task returned nil status")
			} else {
				nm.cfg.Logger.Warn("Deploy task rejected", "node", target.name, "code", resp.Code, "message", resp.Message)
				nm.updateConnectionStatus(target.name, false, false, firstNonEmptyString(resp.Message, "Deploy task rejected"))
			}
			continue
		}

		if hasCoreReady, coreReady, coreMessage := parseDeployCoreState(resp.Message); hasCoreReady {
			if coreReady {
				nm.updateConnectionStatus(target.name, true, false, "")
			} else {
				nm.updateConnectionStatus(target.name, false, false, coreMessage)
			}
		} else {
			nm.updateConnectionStatus(target.name, false, true, "")
		}

		nm.cfg.Logger.Debug("Node config deployed", "node", target.name, "restart", restart, "force_restart", forceRestart, "message", resp.Message)
	}

	if lastProfileUUID != "" {
		batchDuration := time.Since(batchStart).Milliseconds()
		nm.cfg.Logger.RoleService(logger.RoleWorkers, "StartAllNodesByProfileQueueProcessor").Info(fmt.Sprintf("Started all nodes with profile %s in %dms", lastProfileUUID, batchDuration))
	}
}
