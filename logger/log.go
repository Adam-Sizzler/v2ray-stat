package logger

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

// Level represents a logging level.
type Level uint8

const (
	LevelPanic Level = iota
	LevelFatal
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
	LevelNone
)

// levelMap maps string representations to log levels and vice versa.
var levelMap = map[string]Level{
	"trace":   LevelTrace,
	"debug":   LevelDebug,
	"info":    LevelInfo,
	"warn":    LevelWarn,
	"warning": LevelWarn,
	"error":   LevelError,
	"fatal":   LevelFatal,
	"panic":   LevelPanic,
	"none":    LevelNone,
}

var levelNames = map[Level]string{
	LevelTrace: "TRACE",
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
	LevelFatal: "FATAL",
	LevelPanic: "PANIC",
	LevelNone:  "NONE",
}

// ParseLevel converts a string to a Level.
func ParseLevel(level string) (Level, error) {
	if lvl, ok := levelMap[strings.ToLower(level)]; ok {
		return lvl, nil
	}
	return LevelNone, fmt.Errorf("unknown log level: %s", level)
}

// Logger is a simple logger with level-based logging.
type Logger struct {
	writer   io.Writer
	level    Level
	logMode  string
	timezone *time.Location
}

// NewLogger creates a new Logger with the specified level, mode, timezone, and writer.
func NewLogger(level, logMode, timezone string, writer io.Writer) (*Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	var loc *time.Location
	if timezone != "" {
		loc, err = time.LoadLocation(timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone: %v", err)
		}
	} else {
		loc = time.UTC
	}
	return &Logger{
		writer:   writer,
		level:    lvl,
		logMode:  logMode,
		timezone: loc,
	}, nil
}

// Log writes a log message if the specified level is enabled.
func (l *Logger) Log(level Level, msg string, args ...any) {
	if l.level == LevelNone {
		return
	}
	// Проверка в зависимости от режима логирования
	if l.logMode == "inclusive" && level > l.level {
		return
	}
	if l.logMode == "exclusive" && level != l.level {
		return
	}
	// Формирование сообщения с датой в указанном часовом поясе
	logMsg := fmt.Sprintf("%s [%s] %s", time.Now().In(l.timezone).Format("2006/01/02 15:04:05"), levelNames[level], msg)
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			logMsg += fmt.Sprintf(", %v=%v", args[i], args[i+1])
		}
	}
	fmt.Fprintln(l.writer, logMsg)
	if level == LevelFatal {
		os.Exit(1)
	}
	if level == LevelPanic {
		panic(logMsg)
	}
}

// Trace logs a message at TRACE level.
func (l *Logger) Trace(msg string, args ...any) { l.Log(LevelTrace, msg, args...) }

// Debug logs a message at DEBUG level.
func (l *Logger) Debug(msg string, args ...any) { l.Log(LevelDebug, msg, args...) }

// Info logs a message at INFO level.
func (l *Logger) Info(msg string, args ...any) { l.Log(LevelInfo, msg, args...) }

// Warn logs a message at WARN level.
func (l *Logger) Warn(msg string, args ...any) { l.Log(LevelWarn, msg, args...) }

// Error logs a message at ERROR level.
func (l *Logger) Error(msg string, args ...any) { l.Log(LevelError, msg, args...) }

// Fatal logs a message at FATAL level and exits.
func (l *Logger) Fatal(msg string, args ...any) { l.Log(LevelFatal, msg, args...) }

// Panic logs a message at PANIC level and panics.
func (l *Logger) Panic(msg string, args ...any) { l.Log(LevelPanic, msg, args...) }

func NewLoggerWithValidation(level, mode, timezone string, writer io.Writer) (*Logger, error) {
	validLogLevels := []string{"trace", "debug", "info", "warn", "error", "fatal", "panic", "none"}
	validLogModes := []string{"inclusive", "exclusive"}

	validateLevel := strings.ToLower(level)
	if !contains(validLogLevels, validateLevel) {
		validateLevel = "warn"
	}

	validateMode := strings.ToLower(mode)
	if !contains(validLogModes, validateMode) {
		validateMode = "inclusive"
	}

	return NewLogger(validateLevel, validateMode, timezone, writer)
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}
