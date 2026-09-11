package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"exodus-node/config"
)

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

// RunSingboxCheck executes `sing-box check -c <configPath>` and returns the cleaned command output.
func RunSingboxCheck(ctx context.Context, configPath string) (string, error) {
	if configPath == "" {
		configPath = config.FixedSingboxConfigPath
	}

	commands := [][]string{
		{"/usr/local/bin/sing-box", "check", "-c", configPath},
		{"sing-box", "check", "-c", configPath},
	}

	var lastErr error
	for _, cmdArgs := range commands {
		cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		out, err := cmd.CombinedOutput()
		outputStr := strings.TrimSpace(stripANSI(string(out)))
		if err == nil {
			return outputStr, nil
		}
		if outputStr != "" {
			return outputStr, fmt.Errorf("%s", outputStr)
		}
		lastErr = fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("sing-box check command not found")
	}
	return "", lastErr
}

const (
	DefaultSingboxLogPath = "/var/log/singbox/current"
)

var (
	tai64nRegexp   = regexp.MustCompile(`^@[0-9a-fA-F]{16,24}\s*`)
	fatalPfxRegexp = regexp.MustCompile(`^(?:FATAL|ERROR)(?:\[[0-9]+\])?\s*`)
)

// TailSingboxLogLines reads the last N lines from the Sing-box log file using pure Go reverse file scanning.
func TailSingboxLogLines(logPath string, n int) []string {
	if logPath == "" {
		logPath = DefaultSingboxLogPath
	}
	if n <= 0 {
		n = 10
	}

	rawLines, err := readTailLines(logPath, n)
	if err != nil {
		return nil
	}

	cleaned := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = stripANSI(line)
		line = tai64nRegexp.ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return cleaned
}

func readTailLines(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return nil, nil
	}

	const bufSize = 4096
	buf := make([]byte, bufSize)
	var rawLines []string
	var lineBuf []byte

	offset := fileSize
	newlineCount := 0

	for offset > 0 && newlineCount <= n {
		readSize := int64(bufSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		_, err := file.Seek(offset, 0)
		if err != nil {
			break
		}
		nRead, err := file.Read(buf[:readSize])
		if err != nil || nRead == 0 {
			break
		}

		for i := nRead - 1; i >= 0; i-- {
			b := buf[i]
			if b == '\n' {
				if len(lineBuf) > 0 || newlineCount > 0 {
					for l, r := 0, len(lineBuf)-1; l < r; l, r = l+1, r-1 {
						lineBuf[l], lineBuf[r] = lineBuf[r], lineBuf[l]
					}
					rawLines = append(rawLines, string(lineBuf))
					lineBuf = lineBuf[:0]
					newlineCount++
					if newlineCount >= n {
						break
					}
				}
			} else if b != '\r' {
				lineBuf = append(lineBuf, b)
			}
		}
	}

	if len(lineBuf) > 0 && newlineCount < n {
		for l, r := 0, len(lineBuf)-1; l < r; l, r = l+1, r-1 {
			lineBuf[l], lineBuf[r] = lineBuf[r], lineBuf[l]
		}
		rawLines = append(rawLines, string(lineBuf))
	}

	for l, r := 0, len(rawLines)-1; l < r; l, r = l+1, r-1 {
		rawLines[l], rawLines[r] = rawLines[r], rawLines[l]
	}

	return rawLines, nil
}

// ExtractSingboxLogReason scans recent log lines to find the root-cause error reason.
func ExtractSingboxLogReason(logPath string, maxLines int) string {
	lines := TailSingboxLogLines(logPath, maxLines)
	if len(lines) == 0 {
		return ""
	}

	// Scan in reverse (newest to oldest) looking for fatal, error, or panic lines
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "FATAL") ||
			strings.Contains(upper, "ERROR") ||
			strings.Contains(upper, "PANIC:") ||
			strings.Contains(upper, "BIND: ADDRESS ALREADY IN USE") ||
			strings.Contains(upper, "CREATE SERVICE:") ||
			strings.Contains(upper, "START SERVICE") {
			cleaned := fatalPfxRegexp.ReplaceAllString(line, "")
			cleaned = strings.TrimSpace(cleaned)
			if len(cleaned) > 500 {
				cleaned = cleaned[:500]
			}
			return cleaned
		}
	}

	// Fallback to the very last non-empty line
	last := lines[len(lines)-1]
	last = fatalPfxRegexp.ReplaceAllString(last, "")
	last = strings.TrimSpace(last)
	if len(last) > 500 {
		last = last[:500]
	}
	return last
}

