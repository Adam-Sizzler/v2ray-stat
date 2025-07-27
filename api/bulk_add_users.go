package api

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"v2ray-stat/config"

	"github.com/google/uuid"
)

// generateRandomPassword generates a random 30-character password for the trojan protocol using A-Za-z0-9.
func generateRandomPassword(cfg *config.Config) (string, error) {
	cfg.Logger.Debug("Generating random password for trojan protocol")
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const length = 30
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		cfg.Logger.Error("Failed to generate random password", "error", err)
		return "", fmt.Errorf("failed to generate random password: %v", err)
	}
	for i, b := range bytes {
		bytes[i] = charset[b%byte(len(charset))]
	}
	password := string(bytes)
	cfg.Logger.Trace("Generated password", "password_length", len(password))
	return password, nil
}

// getProtocolByInboundTag determines the protocol (vless or trojan) by inboundTag.
func getProtocolByInboundTag(inboundTag string, cfg *config.Config) (string, error) {
	if inboundTag == "" {
		cfg.Logger.Warn("Empty inboundTag")
		return "", fmt.Errorf("inboundTag is empty")
	}

	cfg.Logger.Debug("Reading config file to determine protocol", "inboundTag", inboundTag)
	configPath := cfg.Core.Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		cfg.Logger.Error("Failed to read config file", "path", configPath, "error", err)
		return "", fmt.Errorf("failed to read config.json: %v", err)
	}

	switch cfg.V2rayStat.Type {
	case "xray":
		var cfgXray config.ConfigXray
		if err := json.Unmarshal(data, &cfgXray); err != nil {
			cfg.Logger.Error("Failed to parse JSON for Xray", "error", err)
			return "", fmt.Errorf("failed to parse JSON: %v", err)
		}
		for _, inbound := range cfgXray.Inbounds {
			if inbound.Tag == inboundTag {
				cfg.Logger.Trace("Found protocol for inboundTag", "inboundTag", inboundTag, "protocol", inbound.Protocol)
				return inbound.Protocol, nil
			}
		}
	case "singbox":
		var cfgSingBox config.ConfigSingbox
		if err := json.Unmarshal(data, &cfgSingBox); err != nil {
			cfg.Logger.Error("Failed to parse JSON for Singbox", "error", err)
			return "", fmt.Errorf("failed to parse JSON: %v", err)
		}
		for _, inbound := range cfgSingBox.Inbounds {
			if inbound.Tag == inboundTag {
				cfg.Logger.Trace("Found protocol for inboundTag", "inboundTag", inboundTag, "protocol", inbound.Type)
				return inbound.Type, nil
			}
		}
	}
	cfg.Logger.Warn("inboundTag not found in configuration", "inboundTag", inboundTag)
	return "", fmt.Errorf("inbound with tag %s not found", inboundTag)
}

// AddUsersFromFile adds users from a file with format: user,credential[,inboundTag].
func AddUsersFromFile(file io.Reader, cfg *config.Config) error {
	cfg.Logger.Debug("Starting processing of users file")
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	successCount := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			cfg.Logger.Warn("Skipping empty line", "line_number", lineNumber)
			continue
		}

		// Split line by first space, ignoring comments
		parts := strings.SplitN(line, " ", 2)
		data := strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			cfg.Logger.Trace("Found comment in line", "line_number", lineNumber, "comment", strings.TrimSpace(parts[1]))
		}

		if data == "" {
			cfg.Logger.Warn("Data before space is empty", "line_number", lineNumber)
			continue
		}

		// Split data by commas
		fields := strings.Split(data, ",")
		for i, field := range fields {
			fields[i] = strings.TrimSpace(field)
		}

		// Check for user name presence
		if len(fields) < 1 || fields[0] == "" {
			cfg.Logger.Warn("User name not specified", "line_number", lineNumber, "data", data)
			continue
		}

		user := fields[0]
		if len(user) > 40 {
			cfg.Logger.Warn("User name too long", "line_number", lineNumber, "user", user, "length", len(user))
			continue
		}

		credential := ""
		if len(fields) > 1 && fields[1] != "" {
			credential = fields[1]
			if len(credential) > 40 {
				cfg.Logger.Warn("Credential too long", "line_number", lineNumber, "user", user, "credential_length", len(credential))
				continue
			}
		}

		inboundTag := "vless-in" // Default value
		if len(fields) > 2 && fields[2] != "" {
			inboundTag = fields[2]
		}

		cfg.Logger.Trace("Processing line", "line_number", lineNumber, "user", user, "credential", credential, "inboundTag", inboundTag)

		// Determine protocol by inboundTag
		protocol, err := getProtocolByInboundTag(inboundTag, cfg)
		if err != nil {
			cfg.Logger.Error("Failed to determine protocol", "line_number", lineNumber, "inboundTag", inboundTag, "error", err)
			continue
		}

		// Generate credential based on protocol
		if credential == "" {
			if protocol == "vless" {
				credential = uuid.New().String()
				cfg.Logger.Debug("Generated UUID for vless", "user", user, "credential", credential)
			} else if protocol == "trojan" {
				credential, err = generateRandomPassword(cfg)
				if err != nil {
					cfg.Logger.Error("Failed to generate password", "line_number", lineNumber, "user", user, "error", err)
					continue
				}
				cfg.Logger.Debug("Generated password for trojan", "user", user)
			} else {
				cfg.Logger.Warn("Unsupported protocol", "line_number", lineNumber, "protocol", protocol, "inboundTag", inboundTag)
				continue
			}
		}

		cfg.Logger.Debug("Adding user to configuration", "user", user, "inboundTag", inboundTag)
		if err := AddUserToConfig(user, credential, inboundTag, cfg); err != nil {
			cfg.Logger.Error("Failed to add user", "line_number", lineNumber, "user", user, "error", err)
			continue
		}

		successCount++
		cfg.Logger.Trace("User added successfully", "line_number", lineNumber, "user", user)
	}

	if err := scanner.Err(); err != nil {
		cfg.Logger.Error("Failed to read file", "error", err)
		return fmt.Errorf("failed to read file: %v", err)
	}

	cfg.Logger.Info("File processing completed", "success_count", successCount, "total_lines", lineNumber)
	return nil
}

// BulkAddUsersHandler handles POST requests with a users file.
func BulkAddUsersHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Logger.Debug("Starting BulkAddUsersHandler request processing")

		if r.Method != http.MethodPost {
			cfg.Logger.Warn("Invalid HTTP method", "method", r.Method)
			http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
			return
		}

		// Retrieve file from request
		file, _, err := r.FormFile("users_file")
		if err != nil {
			cfg.Logger.Error("Failed to retrieve file", "error", err)
			http.Error(w, fmt.Sprintf("Failed to retrieve file: %v", err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Process file
		cfg.Logger.Debug("Processing users file")
		if err := AddUsersFromFile(file, cfg); err != nil {
			cfg.Logger.Error("Failed to process file", "error", err)
			http.Error(w, fmt.Sprintf("Failed to process file: %v", err), http.StatusInternalServerError)
			return
		}

		cfg.Logger.Info("API bulk_add_users: users added successfully")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Users added successfully")
	}
}
