package system

import (
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const linuxClockTicksPerSecond = 100

var (
	goProcessStartedAt = time.Now()
	goHealthCPUState   processCPUState
)

type goHealthResponse struct {
	PM2Stats       []pm2ProcessStat `json:"pm2Stats"`
	RuntimeMetrics []runtimeMetric  `json:"runtimeMetrics"`
	RuntimeSummary goRuntimeSummary `json:"runtimeSummary"`
	RuntimeNotes   []string         `json:"runtimeNotes"`
}

type pm2ProcessStat struct {
	Name   string `json:"name"`
	Memory string `json:"memory"`
	CPU    string `json:"cpu"`
}

type goRuntimeMetric struct {
	Name            string          `json:"name"`
	InstanceType    string          `json:"instanceType"`
	InstanceID      int             `json:"instanceId"`
	PID             int             `json:"pid"`
	StartedAt       string          `json:"startedAt"`
	UptimeSeconds   int64           `json:"uptimeSeconds"`
	Runtime         goRuntimeInfo   `json:"runtime"`
	CPU             goCPUInfo       `json:"cpu"`
	Memory          goMemoryInfo    `json:"memory"`
	GC              goGCInfo        `json:"gc"`
	Scheduler       goSchedulerInfo `json:"scheduler"`
	Process         goProcessInfo   `json:"process"`
	CollectedAt     string          `json:"collectedAt"`
	CollectedAtUnix int64           `json:"collectedAtUnix"`
}

type runtimeMetric struct {
	RSS              float64         `json:"rss"`
	HeapUsed         float64         `json:"heapUsed"`
	HeapTotal        float64         `json:"heapTotal"`
	External         float64         `json:"external"`
	ArrayBuffers     float64         `json:"arrayBuffers"`
	EventLoopDelayMs float64         `json:"eventLoopDelayMs"`
	EventLoopP99Ms   float64         `json:"eventLoopP99Ms"`
	ActiveHandles    float64         `json:"activeHandles"`
	Uptime           float64         `json:"uptime"`
	PID              float64         `json:"pid"`
	Timestamp        float64         `json:"timestamp"`
	InstanceID       string          `json:"instanceId"`
	InstanceType     string          `json:"instanceType"`
	Name             string          `json:"name"`
	StartedAt        string          `json:"startedAt"`
	UptimeSeconds    int64           `json:"uptimeSeconds"`
	Runtime          goRuntimeInfo   `json:"runtime"`
	CPU              goCPUInfo       `json:"cpu"`
	Memory           goMemoryInfo    `json:"memory"`
	GC               goGCInfo        `json:"gc"`
	Scheduler        goSchedulerInfo `json:"scheduler"`
	Process          goProcessInfo   `json:"process"`
	CollectedAt      string          `json:"collectedAt"`
	CollectedAtUnix  int64           `json:"collectedAtUnix"`
}

type goRuntimeInfo struct {
	Language string `json:"language"`
	Version  string `json:"version"`
	GOOS     string `json:"goos"`
	GOARCH   string `json:"goarch"`
}

type goCPUInfo struct {
	Cores                 int     `json:"cores"`
	GOMAXPROCS            int     `json:"gomaxprocs"`
	ProcessPercent        float64 `json:"processPercent"`
	ProcessCPUSeconds     float64 `json:"processCpuSeconds"`
	ProcessCPUPercentMode string  `json:"processCpuPercentMode"`
}

type goMemoryInfo struct {
	RSSBytes          uint64  `json:"rssBytes"`
	AllocBytes        uint64  `json:"allocBytes"`
	TotalAllocBytes   uint64  `json:"totalAllocBytes"`
	SysBytes          uint64  `json:"sysBytes"`
	HeapAllocBytes    uint64  `json:"heapAllocBytes"`
	HeapSysBytes      uint64  `json:"heapSysBytes"`
	HeapIdleBytes     uint64  `json:"heapIdleBytes"`
	HeapInuseBytes    uint64  `json:"heapInuseBytes"`
	HeapReleasedBytes uint64  `json:"heapReleasedBytes"`
	StackInuseBytes   uint64  `json:"stackInuseBytes"`
	OtherSysBytes     uint64  `json:"otherSysBytes"`
	HeapUsedPercent   float64 `json:"heapUsedPercent"`
}

type goGCInfo struct {
	NumGC          uint32  `json:"numGc"`
	PauseTotalNs   uint64  `json:"pauseTotalNs"`
	LastPauseNs    uint64  `json:"lastPauseNs"`
	LastGCUnixNano uint64  `json:"lastGcUnixNano"`
	GCCPUFraction  float64 `json:"gcCpuFraction"`
	PauseP99Ms     float64 `json:"pauseP99Ms"`
	PauseP99Source string  `json:"pauseP99Source"`
	GOGC           int     `json:"gogc"`
}

type goSchedulerInfo struct {
	Goroutines             int     `json:"goroutines"`
	CgoCalls               int64   `json:"cgoCalls"`
	SchedulerDelayMs       float64 `json:"schedulerDelayMs"`
	SchedulerP99Ms         float64 `json:"schedulerP99Ms"`
	SchedulerLatencySource string  `json:"schedulerLatencySource"`
}

type goProcessInfo struct {
	OpenFileDescriptors int `json:"openFileDescriptors"`
	Threads             int `json:"threads"`
}

type goRuntimeSummary struct {
	TotalProcesses          int     `json:"totalProcesses"`
	StartedAt               string  `json:"startedAt"`
	UptimeSeconds           int64   `json:"uptimeSeconds"`
	TotalRSSBytes           uint64  `json:"totalRssBytes"`
	HeapAllocBytes          uint64  `json:"heapAllocBytes"`
	AverageCPUPercent       float64 `json:"averageCpuPercent"`
	AverageGoroutines       int     `json:"averageGoroutines"`
	AverageSchedulerDelayMs float64 `json:"averageSchedulerDelayMs"`
	AverageSchedulerP99Ms   float64 `json:"averageSchedulerP99Ms"`
}

type processCPUState struct {
	mu                    sync.Mutex
	lastWall              time.Time
	lastProcessCPUSeconds float64
}

func buildGoHealthResponse() goHealthResponse {
	now := time.Now()
	startedAt := goProcessStartedAt.UTC().Format(time.RFC3339)
	uptimeSeconds := int64(now.Sub(goProcessStartedAt).Seconds())

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	rssBytes := readProcessRSSBytes()
	if rssBytes == 0 {
		rssBytes = mem.Sys
	}

	processCPUSeconds, hasProcessCPU := readProcessCPUSeconds()
	cpuPercent, cpuMode := sampleProcessCPUPercent(now, processCPUSeconds, hasProcessCPU)
	gcPauseP99Ms, gcPauseSource := readGCPauseP99Ms(&mem)
	schedulerDelayMs, schedulerP99Ms, schedulerSource := readRuntimeHistogramStatsMs("/sched/latencies:seconds")
	threads := readProcessThreads()

	metric := goRuntimeMetric{
		Name:          "Exodus REST API-0",
		InstanceType:  "api",
		InstanceID:    0,
		PID:           os.Getpid(),
		StartedAt:     startedAt,
		UptimeSeconds: uptimeSeconds,
		Runtime: goRuntimeInfo{
			Language: "go",
			Version:  runtime.Version(),
			GOOS:     runtime.GOOS,
			GOARCH:   runtime.GOARCH,
		},
		CPU: goCPUInfo{
			Cores:                 runtime.NumCPU(),
			GOMAXPROCS:            runtime.GOMAXPROCS(0),
			ProcessPercent:        roundFloat(cpuPercent, 2),
			ProcessCPUSeconds:     roundFloat(processCPUSeconds, 3),
			ProcessCPUPercentMode: cpuMode,
		},
		Memory: goMemoryInfo{
			RSSBytes:          rssBytes,
			AllocBytes:        mem.Alloc,
			TotalAllocBytes:   mem.TotalAlloc,
			SysBytes:          mem.Sys,
			HeapAllocBytes:    mem.HeapAlloc,
			HeapSysBytes:      mem.HeapSys,
			HeapIdleBytes:     mem.HeapIdle,
			HeapInuseBytes:    mem.HeapInuse,
			HeapReleasedBytes: mem.HeapReleased,
			StackInuseBytes:   mem.StackInuse,
			OtherSysBytes:     mem.OtherSys,
			HeapUsedPercent:   roundFloat(percentOf(mem.HeapAlloc, mem.HeapSys), 2),
		},
		GC: goGCInfo{
			NumGC:          mem.NumGC,
			PauseTotalNs:   mem.PauseTotalNs,
			LastPauseNs:    lastGCPauseNs(&mem),
			LastGCUnixNano: mem.LastGC,
			GCCPUFraction:  roundFloat(mem.GCCPUFraction, 6),
			PauseP99Ms:     roundFloat(gcPauseP99Ms, 3),
			PauseP99Source: gcPauseSource,
			GOGC:           readGOGCSetting(),
		},
		Scheduler: goSchedulerInfo{
			Goroutines:             runtime.NumGoroutine(),
			CgoCalls:               runtime.NumCgoCall(),
			SchedulerDelayMs:       roundFloat(schedulerDelayMs, 3),
			SchedulerP99Ms:         roundFloat(schedulerP99Ms, 3),
			SchedulerLatencySource: schedulerSource,
		},
		Process: goProcessInfo{
			OpenFileDescriptors: countOpenFileDescriptors(),
			Threads:             threads,
		},
		CollectedAt:     now.UTC().Format(time.RFC3339),
		CollectedAtUnix: now.UnixMilli(),
	}
	frontendRuntimeMetric := runtimeMetric{
		RSS:              float64(metric.Memory.RSSBytes),
		HeapUsed:         float64(metric.Memory.HeapAllocBytes),
		HeapTotal:        float64(metric.Memory.HeapSysBytes),
		External:         float64(metric.Memory.OtherSysBytes),
		ArrayBuffers:     float64(metric.Memory.StackInuseBytes),
		EventLoopDelayMs: metric.Scheduler.SchedulerDelayMs,
		EventLoopP99Ms:   metric.Scheduler.SchedulerP99Ms,
		ActiveHandles:    float64(metric.Process.OpenFileDescriptors),
		Uptime:           float64(uptimeSeconds),
		PID:              float64(metric.PID),
		Timestamp:        float64(metric.CollectedAtUnix),
		InstanceID:       strconv.Itoa(metric.InstanceID),
		InstanceType:     metric.InstanceType,
		Name:             metric.Name,
		StartedAt:        metric.StartedAt,
		UptimeSeconds:    metric.UptimeSeconds,
		Runtime:          metric.Runtime,
		CPU:              metric.CPU,
		Memory:           metric.Memory,
		GC:               metric.GC,
		Scheduler:        metric.Scheduler,
		Process:          metric.Process,
		CollectedAt:      metric.CollectedAt,
		CollectedAtUnix:  metric.CollectedAtUnix,
	}

	summary := goRuntimeSummary{
		TotalProcesses:          1,
		StartedAt:               startedAt,
		UptimeSeconds:           uptimeSeconds,
		TotalRSSBytes:           metric.Memory.RSSBytes,
		HeapAllocBytes:          metric.Memory.HeapAllocBytes,
		AverageCPUPercent:       metric.CPU.ProcessPercent,
		AverageGoroutines:       metric.Scheduler.Goroutines,
		AverageSchedulerDelayMs: metric.Scheduler.SchedulerDelayMs,
		AverageSchedulerP99Ms:   metric.Scheduler.SchedulerP99Ms,
	}

	return goHealthResponse{
		PM2Stats:       []pm2ProcessStat{},
		RuntimeMetrics: []runtimeMetric{frontendRuntimeMetric},
		RuntimeSummary: summary,
		RuntimeNotes: []string{
			"pm2Stats is kept empty for frontend compatibility; runtimeMetrics/runtimeSummary contain the Go runtime data.",
			"schedulerP99Ms is read from Go runtime/metrics /sched/latencies when available.",
			"RSS and CPU are read from /proc on Linux; fallback values are used when /proc is unavailable.",
		},
	}
}

func sampleProcessCPUPercent(now time.Time, processCPUSeconds float64, ok bool) (float64, string) {
	if !ok {
		return 0, "unavailable"
	}

	goHealthCPUState.mu.Lock()
	defer goHealthCPUState.mu.Unlock()

	if !goHealthCPUState.lastWall.IsZero() {
		wallDelta := now.Sub(goHealthCPUState.lastWall).Seconds()
		cpuDelta := processCPUSeconds - goHealthCPUState.lastProcessCPUSeconds
		goHealthCPUState.lastWall = now
		goHealthCPUState.lastProcessCPUSeconds = processCPUSeconds

		if wallDelta > 0 && cpuDelta >= 0 {
			return clampCPUPercent(cpuDelta / wallDelta / float64(maxInt(runtime.NumCPU(), 1)) * 100), "sampled"
		}
	}

	goHealthCPUState.lastWall = now
	goHealthCPUState.lastProcessCPUSeconds = processCPUSeconds

	uptime := now.Sub(goProcessStartedAt).Seconds()
	if uptime <= 0 {
		return 0, "first_sample"
	}
	return clampCPUPercent(processCPUSeconds / uptime / float64(maxInt(runtime.NumCPU(), 1)) * 100), "lifetime_average"
}

func readProcessCPUSeconds() (float64, bool) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}

	content := string(data)
	endComm := strings.LastIndex(content, ")")
	if endComm < 0 || endComm+2 >= len(content) {
		return 0, false
	}

	fields := strings.Fields(content[endComm+2:])
	if len(fields) < 13 {
		return 0, false
	}

	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, false
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, false
	}

	return float64(utime+stime) / linuxClockTicksPerSecond, true
}

func readProcessRSSBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}

	residentPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}

	return residentPages * uint64(os.Getpagesize())
}

func readProcessThreads() int {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Threads:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0
		}
		value, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0
		}
		return value
	}

	return 0
}

func countOpenFileDescriptors() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}

func readGCPauseP99Ms(mem *runtime.MemStats) (float64, string) {
	if value, source := readRuntimeHistogramP99Ms("/gc/pauses:seconds"); value > 0 {
		return value, source
	}

	pauses := make([]uint64, 0, len(mem.PauseNs))
	for _, pause := range mem.PauseNs {
		if pause > 0 {
			pauses = append(pauses, pause)
		}
	}
	if len(pauses) == 0 {
		return 0, "runtime.MemStats.PauseNs"
	}

	sort.Slice(pauses, func(i, j int) bool { return pauses[i] < pauses[j] })
	idx := int(math.Ceil(float64(len(pauses))*0.99)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(pauses) {
		idx = len(pauses) - 1
	}
	return float64(pauses[idx]) / float64(time.Millisecond), "runtime.MemStats.PauseNs"
}

func readRuntimeHistogramStatsMs(metricName string) (float64, float64, string) {
	meanMs, p99Ms, source := readRuntimeHistogramMeanAndP99Ms(metricName)
	return meanMs, p99Ms, source
}

func readRuntimeHistogramMeanAndP99Ms(metricName string) (float64, float64, string) {
	var samples [1]metrics.Sample
	samples[0].Name = metricName
	metrics.Read(samples[:])
	if samples[0].Value.Kind() != metrics.KindFloat64Histogram {
		return 0, 0, "unavailable"
	}

	histogram := samples[0].Value.Float64Histogram()
	if histogram == nil || len(histogram.Counts) == 0 || len(histogram.Buckets) < len(histogram.Counts)+1 {
		return 0, 0, "unavailable"
	}

	var total uint64
	var weightedSumSeconds float64
	for index, count := range histogram.Counts {
		total += count
		if count == 0 {
			continue
		}

		lowerBoundSeconds := histogram.Buckets[index]
		upperBoundSeconds := histogram.Buckets[index+1]
		if math.IsInf(lowerBoundSeconds, 0) || math.IsNaN(lowerBoundSeconds) {
			lowerBoundSeconds = 0
		}
		if math.IsInf(upperBoundSeconds, 0) || math.IsNaN(upperBoundSeconds) {
			upperBoundSeconds = lowerBoundSeconds
		}
		weightedSumSeconds += ((lowerBoundSeconds + upperBoundSeconds) / 2) * float64(count)
	}
	if total == 0 {
		return 0, 0, metricName
	}

	target := uint64(math.Ceil(float64(total) * 0.99))
	if target == 0 {
		target = 1
	}

	var cumulative uint64
	p99Seconds := 0.0
	for index, count := range histogram.Counts {
		cumulative += count
		if cumulative < target {
			continue
		}

		upperBoundSeconds := histogram.Buckets[index+1]
		if math.IsInf(upperBoundSeconds, 0) || math.IsNaN(upperBoundSeconds) {
			lowerBoundSeconds := histogram.Buckets[index]
			if math.IsInf(lowerBoundSeconds, 0) || math.IsNaN(lowerBoundSeconds) {
				upperBoundSeconds = 0
			} else {
				upperBoundSeconds = lowerBoundSeconds
			}
		}
		p99Seconds = upperBoundSeconds
		break
	}

	return weightedSumSeconds / float64(total) * float64(time.Second/time.Millisecond), p99Seconds * float64(time.Second/time.Millisecond), metricName
}

func readRuntimeHistogramP99Ms(metricName string) (float64, string) {
	var samples [1]metrics.Sample
	samples[0].Name = metricName
	metrics.Read(samples[:])
	if samples[0].Value.Kind() != metrics.KindFloat64Histogram {
		return 0, "unavailable"
	}

	histogram := samples[0].Value.Float64Histogram()
	if histogram == nil || len(histogram.Counts) == 0 || len(histogram.Buckets) < len(histogram.Counts)+1 {
		return 0, "unavailable"
	}

	var total uint64
	for _, count := range histogram.Counts {
		total += count
	}
	if total == 0 {
		return 0, metricName
	}

	target := uint64(math.Ceil(float64(total) * 0.99))
	if target == 0 {
		target = 1
	}

	var cumulative uint64
	for index, count := range histogram.Counts {
		cumulative += count
		if cumulative < target {
			continue
		}

		upperBoundSeconds := histogram.Buckets[index+1]
		if math.IsInf(upperBoundSeconds, 0) || math.IsNaN(upperBoundSeconds) {
			lowerBoundSeconds := histogram.Buckets[index]
			if math.IsInf(lowerBoundSeconds, 0) || math.IsNaN(lowerBoundSeconds) {
				return 0, metricName
			}
			upperBoundSeconds = lowerBoundSeconds
		}
		return upperBoundSeconds * float64(time.Second/time.Millisecond), metricName
	}

	return 0, metricName
}

func readGOGCSetting() int {
	raw := strings.TrimSpace(os.Getenv("GOGC"))
	if raw == "" {
		return 100
	}
	if strings.EqualFold(raw, "off") {
		return -1
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 100
	}
	return value
}

func lastGCPauseNs(mem *runtime.MemStats) uint64 {
	if mem.NumGC == 0 {
		return 0
	}
	return mem.PauseNs[(mem.NumGC+255)%256]
}

func percentOf(value, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) / float64(total) * 100
}

func clampCPUPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func roundFloat(value float64, precision int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	factor := math.Pow(10, float64(precision))
	return math.Round(value*factor) / factor
}
