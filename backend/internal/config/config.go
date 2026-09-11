package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	DefaultSubAppPort     = 3010
	DefaultSubGRPCAddress = "0.0.0.0"
	DefaultSubGRPCPort    = 3011
	DefaultSubAppPath     = "/subscription"
	DefaultTrustProxy     = "1"
)

type BackendConfig struct {
	BasePath string
	AppPort  int
}

type Config struct {
	Backend                         BackendConfig
	AppPort                         int
	SubPath                         string
	CaddyAuthAPIToken               string
	CloudflareZeroTrustClientID     string
	CloudflareZeroTrustClientSecret string
	SessionSecret                   string
	NodeEnv                         string

	GRPCAddress      string
	GRPCPort         int
	GRPCPath         string
	GRPCToken        string
	RequireGRPCToken bool
	MTLSConfig       *MTLSConfig

	AppVersion string
	TrustProxy string
}

type MTLSConfig struct {
	Cert   string
	Key    string
	CACert string
}

// EnvError is a schema-like validation item rendered in the same style as
// Exodus subscription-page Zod validation errors.
type EnvError struct {
	Key     string
	Message string
}

// EnvErrors groups all .env validation failures so the logger can print a
// single grouped configuration error block instead of one fatal Go error.
type EnvErrors []EnvError

func (e EnvErrors) Error() string {
	if len(e) == 0 {
		return ".env configuration validation error"
	}
	parts := make([]string, 0, len(e))
	for _, item := range e {
		if strings.TrimSpace(item.Key) == "" {
			parts = append(parts, strings.TrimSpace(item.Message))
			continue
		}
		parts = append(parts, strings.TrimSpace(item.Key)+": "+strings.TrimSpace(item.Message))
	}
	return strings.Join(parts, "; ")
}

func NewEnvError(key, message string) EnvErrors {
	return EnvErrors{{Key: strings.TrimSpace(key), Message: strings.TrimSpace(message)}}
}

// FormatEnvironmentErrors renders configuration failures like Exodus
// subscription-page: a readable block with exact env names and setup hints.
func FormatEnvironmentErrors(err error) string {
	if err == nil {
		return ""
	}

	var envErrs EnvErrors
	if errors.As(err, &envErrs) {
		lines := make([]string, 0, len(envErrs))
		for _, item := range envErrs {
			key := strings.TrimSpace(item.Key)
			msg := strings.TrimSpace(item.Message)
			if key == "" {
				lines = append(lines, "❌ "+msg)
				continue
			}
			lines = append(lines, fmt.Sprintf("❌ %s: %s", key, msg))
		}
		return fmt.Sprintf(`🔧 Environment Configuration Errors:
%s

Please fix your .env file and restart the application.`, strings.Join(lines, "\n"))
	}

	return fmt.Sprintf(`🔧 Environment Configuration Errors:
❌ %s

Please fix your .env file and restart the application.`, err.Error())
}

func Load() (Config, error) {
	rawPath := os.Getenv("SUB_APP_PATH")
	if err := validateBasePath(rawPath); err != nil {
		return Config{}, NewEnvError("SUB_APP_PATH", err.Error())
	}
	pathPrefix := normalizeBasePath(rawPath)
	grpcToken := strings.TrimSpace(os.Getenv("SUB_GRPC_TOKEN"))

	appPort, err := parsePort("SUB_APP_PORT", os.Getenv("SUB_APP_PORT"), DefaultSubAppPort)
	if err != nil {
		return Config{}, err
	}
	grpcPort, err := parsePort("SUB_GRPC_PORT", os.Getenv("SUB_GRPC_PORT"), DefaultSubGRPCPort)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Backend: BackendConfig{
			BasePath: pathPrefix,
			AppPort:  appPort,
		},
		AppPort:                         appPort,
		GRPCPort:                        grpcPort,
		SubPath:                         pathPrefix,
		CaddyAuthAPIToken:               os.Getenv("CADDY_AUTH_API_TOKEN"),
		CloudflareZeroTrustClientID:     os.Getenv("CLOUDFLARE_ZERO_TRUST_CLIENT_ID"),
		CloudflareZeroTrustClientSecret: os.Getenv("CLOUDFLARE_ZERO_TRUST_CLIENT_SECRET"),
		NodeEnv:                         strings.TrimSpace(os.Getenv("NODE_ENV")),
		GRPCAddress:                     getEnvOrDefault("SUB_GRPC_ADDRESS", DefaultSubGRPCAddress),
		GRPCPath:                        pathPrefix,
		GRPCToken:                       grpcToken,
		AppVersion:                      strings.TrimSpace(os.Getenv("SUB_APP_VERSION")),
		TrustProxy:                      getEnvOrDefault("TRUST_PROXY", DefaultTrustProxy),
	}

	payload, err := ParseNodePayloadFromSecret()
	if err == nil {
		cfg.MTLSConfig = &MTLSConfig{
			Cert:   payload.NodeCertPem,
			Key:    payload.NodeKeyPem,
			CACert: payload.CaCertPem,
		}
	} else if !errors.Is(err, ErrSecretKeyNotSet) {
		return cfg, err
	}

	cfg.RequireGRPCToken = cfg.MTLSConfig == nil
	if cfg.RequireGRPCToken && cfg.GRPCToken == "" {
		return cfg, NewEnvError("SUB_GRPC_TOKEN", "gRPC token is required when mTLS secret key is not configured.")
	}

	if cfg.CaddyAuthAPIToken != "" {
		cfg.SessionSecret = deriveSessionSecret(cfg.CaddyAuthAPIToken)
		return cfg, nil
	}

	if cfg.CloudflareZeroTrustClientID != "" && cfg.CloudflareZeroTrustClientSecret != "" {
		cfg.SessionSecret = deriveSessionSecret(cfg.CloudflareZeroTrustClientID + ":" + cfg.CloudflareZeroTrustClientSecret)
		return cfg, nil
	}

	secretKey := os.Getenv("INTERNAL_JWT_SECRET")
	if secretKey == "" {
		return cfg, NewEnvError("INTERNAL_JWT_SECRET", "Missing session authentication secret.")
	}
	cfg.SessionSecret = secretKey

	return cfg, nil
}

func (c Config) IsDevelopment() bool {
	return strings.EqualFold(c.NodeEnv, "development")
}

func (c Config) IsProduction() bool {
	return !c.IsDevelopment()
}

func DisplayPrefix(prefix string) string {
	if prefix == "" {
		return "/"
	}
	return prefix
}

func getEnvOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func parsePort(envKey, raw string, fallback int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	port, err := strconv.Atoi(trimmed)
	if err != nil || port < 1 || port > 65535 {
		return 0, NewEnvError(envKey, fmt.Sprintf("Invalid port value %q. Use a number from 1 to 65535.", raw))
	}
	return port, nil
}

func deriveSessionSecret(apiToken string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(apiToken)))
	return hex.EncodeToString(hash[:])
}

func parseBool(raw string, fallback bool) bool {
	val := strings.TrimSpace(raw)
	if val == "" {
		return fallback
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return fallback
	}
	return b
}

var validBasePathRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\/]*$`)

func validateBasePath(input string) error {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || trimmed == "/" {
		return nil
	}
	if strings.Contains(trimmed, "..") {
		return fmt.Errorf("invalid path prefix %q: directory traversal is not allowed", input)
	}
	if strings.ContainsAny(trimmed, "?#\\ \t\r\n") {
		return fmt.Errorf("invalid path prefix %q: contains illegal characters (spaces, query/fragment markers, or backslashes)", input)
	}
	if !validBasePathRegex.MatchString(trimmed) {
		return fmt.Errorf("invalid path prefix %q: must contain only alphanumeric characters, hyphens, underscores, and slashes", input)
	}
	return nil
}

// Trimmed returns BasePath without trailing slash (e.g. "/subscription" or "" for root "/").
func (b BackendConfig) Trimmed() string {
	normalized := normalizeBasePath(b.BasePath)
	if normalized == "/" {
		return ""
	}
	return strings.TrimSuffix(normalized, "/")
}

// IsCustom reports whether a custom non-root BasePath is configured.
func (b BackendConfig) IsCustom() bool {
	return b.Trimmed() != ""
}

// WithSlash returns BasePath with leading and trailing slashes (e.g. "/subscription/" or "/" for root).
func (b BackendConfig) WithSlash() string {
	return normalizeBasePath(b.BasePath)
}

func normalizeBasePath(input string) string {
	cleaned := strings.Trim(strings.TrimSpace(input), "/")
	if cleaned == "" {
		return "/"
	}
	return "/" + cleaned + "/"
}
