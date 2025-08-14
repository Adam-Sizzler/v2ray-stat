package logprocessor

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"time"

	"v2ray-stat/logger"
	"v2ray-stat/node/config"
	"v2ray-stat/node/proto"
)

// ProcessLogLine обрабатывает строку лога и возвращает данные для пользователя.
func ProcessLogLine(line string, dnsStats map[string]map[string]int, cfg *config.NodeConfig) (string, []string, bool) {
	matches := regexp.MustCompile(cfg.Core.AccessLogRegex).FindStringSubmatch(line)
	if len(matches) != 3 && len(matches) != 4 {
		return "", nil, false
	}

	var user, domain, ip string
	if len(matches) == 4 {
		ip = matches[1]
		domain = strings.TrimSpace(matches[2])
		user = strings.TrimSpace(matches[3])
	} else {
		user = strings.TrimSpace(matches[1])
		ip = strings.TrimSpace(matches[2])
		domain = ""
	}

	// Собираем IP без временных меток
	validIPs := []string{ip}

	if dnsStats[user] == nil {
		dnsStats[user] = make(map[string]int)
	}
	if domain != "" {
		dnsStats[user][domain]++
	}

	return user, validIPs, true
}

// LogProcessor управляет чтением и обработкой логов на ноде.
type LogProcessor struct {
	cfg           *config.NodeConfig
	file          *os.File
	offset        int64
	logger        *logger.Logger
	accessLogPath string
}

// NewLogProcessor создаёт новый процессор логов.
func NewLogProcessor(cfg *config.NodeConfig) (*LogProcessor, error) {
	accessLog, err := os.OpenFile(cfg.Core.AccessLog, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		cfg.Logger.Error("Failed to open log file", "file", cfg.Core.AccessLog, "error", err)
		return nil, err
	}
	offset, err := accessLog.Seek(0, 2) // Перейти в конец файла
	if err != nil {
		cfg.Logger.Error("Error getting log file position", "error", err)
		accessLog.Close()
		return nil, err
	}
	cfg.Logger.Debug("Initialized log processor", "file", cfg.Core.AccessLog, "offset", offset)

	processor := &LogProcessor{
		cfg:           cfg,
		file:          accessLog,
		offset:        offset,
		logger:        cfg.Logger,
		accessLogPath: cfg.Core.AccessLog,
	}

	// Запускаем ежедневную очистку лог-файла
	go processor.runDailyCleanup()

	return processor, nil
}

// ReadNewLines читает новые строки из лог-файла и возвращает обработанные данные.
func (lp *LogProcessor) ReadNewLines() (*proto.GetLogDataResponse, error) {
	lp.file.Seek(lp.offset, 0)
	scanner := bufio.NewScanner(lp.file)
	dnsStats := make(map[string]map[string]int)
	ipUpdates := make(map[string]map[string]struct{})

	for scanner.Scan() {
		line := scanner.Text()
		lp.logger.Debug("Processing log line", "line", line)
		user, validIPs, ok := ProcessLogLine(line, dnsStats, lp.cfg)
		if ok {
			if ipUpdates[user] == nil {
				ipUpdates[user] = make(map[string]struct{})
			}
			for _, ip := range validIPs {
				ipUpdates[user][ip] = struct{}{}
			}
			lp.logger.Trace("Retrieved data for user", "user", user, "ip_count", len(ipUpdates[user]))
		} else {
			lp.logger.Debug("Invalid regex for line", "line", line)
		}
	}

	if err := scanner.Err(); err != nil {
		lp.logger.Error("Error reading log file", "error", err)
		return nil, err
	}

	pos, err := lp.file.Seek(0, 1)
	if err != nil {
		lp.logger.Error("Error getting file position", "error", err)
		return nil, err
	}
	lp.offset = pos
	lp.logger.Debug("Finished processing new log lines", "offset", pos)

	response := &proto.GetLogDataResponse{UserLogData: make(map[string]*proto.UserLogData)}
	for user, domains := range dnsStats {
		response.UserLogData[user] = &proto.UserLogData{
			ValidIps: make([]string, 0, len(ipUpdates[user])),
			DnsStats: make(map[string]int32),
		}
		for ip := range ipUpdates[user] {
			response.UserLogData[user].ValidIps = append(response.UserLogData[user].ValidIps, ip)
		}
		for domain, count := range domains {
			response.UserLogData[user].DnsStats[domain] = int32(count)
		}
	}

	return response, nil
}

// runDailyCleanup очищает лог-файл раз в сутки.
func (lp *LogProcessor) runDailyCleanup() {
	dailyTicker := time.NewTicker(24 * time.Hour)
	defer dailyTicker.Stop()

	for range dailyTicker.C {
		if err := lp.file.Close(); err != nil {
			lp.logger.Error("Error closing log file", "file", lp.accessLogPath, "error", err)
		}
		accessLog, err := os.OpenFile(lp.accessLogPath, os.O_RDONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			lp.logger.Error("Error reopening log file after truncation", "file", lp.accessLogPath, "error", err)
			return
		}
		lp.file = accessLog
		lp.offset = 0
		lp.logger.Info("Log file successfully truncated", "file", lp.accessLogPath)
	}
}
