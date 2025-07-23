package lua

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"v2ray-stat/config"
)

// AddUserToAuthLua adds a new user to the beginning of the users table in auth.lua.
func AddUserToAuthLua(cfg *config.Config, user, uuid string) error {
	cfg.Logger.Debug("Starting AddUserToAuthLua", "user", user, "filePath", cfg.Paths.AuthLua)

	// Open file for reading
	file, err := os.Open(cfg.Paths.AuthLua)
	if err != nil {
		cfg.Logger.Error("Failed to open auth.lua", "error", err)
		return fmt.Errorf("failed to open auth.lua: %w", err)
	}
	defer file.Close()

	// Read file line by line
	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		cfg.Logger.Error("Error reading auth.lua", "error", err)
		return fmt.Errorf("error reading auth.lua: %w", err)
	}

	// Find start and end of users table
	usersTableStart, usersTableEnd := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "local users") && strings.Contains(trimmed, "=") && strings.Contains(trimmed, "{") {
			usersTableStart = i
			cfg.Logger.Debug("Found users table start", "line", i)
			break
		}
	}
	if usersTableStart < 0 {
		cfg.Logger.Error("Users table not found in auth.lua")
		return fmt.Errorf("users table not found in auth.lua")
	}
	for i := usersTableStart + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			usersTableEnd = i
			cfg.Logger.Debug("Found users table end", "line", i)
			break
		}
	}
	if usersTableEnd < 0 {
		cfg.Logger.Error("Closing brace for users table not found")
		return fmt.Errorf("closing brace for users table not found")
	}

	// Check if user already exists
	for i := usersTableStart + 1; i < usersTableEnd; i++ {
		if strings.Contains(lines[i], `["`+user+`"]`) {
			cfg.Logger.Warn("User already exists in auth.lua", "user", user)
			return fmt.Errorf("user %s already exists in auth.lua", user)
		}
	}

	// Create new user entry
	newEntry := fmt.Sprintf(`  ["%s"] = "%s",`, user, uuid)
	cfg.Logger.Trace("Created new user entry", "entry", newEntry)

	// Insert new entry at the beginning of the users table
	insertPos := usersTableStart + 1
	lines = append(
		lines[:insertPos],
		append([]string{newEntry}, lines[insertPos:]...)...,
	)

	// Rewrite file
	out, err := os.OpenFile(cfg.Paths.AuthLua, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		cfg.Logger.Error("Failed to create auth.lua", "error", err)
		return fmt.Errorf("failed to create auth.lua: %w", err)
	}
	defer out.Close()

	writer := bufio.NewWriter(out)
	for _, line := range lines {
		cfg.Logger.Trace("Writing line to auth.lua", "line", line)
		if _, err := writer.WriteString(line + "\n"); err != nil {
			cfg.Logger.Error("Error writing to auth.lua", "error", err)
			return fmt.Errorf("error writing to auth.lua: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		cfg.Logger.Error("Error flushing writer", "error", err)
		return fmt.Errorf("error flushing writer: %w", err)
	}

	cfg.Logger.Debug("User added to auth.lua successfully", "user", user)
	return nil
}

// DeleteUserFromAuthLua removes a user from the users table in auth.lua.
func DeleteUserFromAuthLua(cfg *config.Config, user string) error {
	cfg.Logger.Debug("Starting DeleteUserFromAuthLua", "user", user, "filePath", cfg.Paths.AuthLua)

	// Open file for reading
	file, err := os.Open(cfg.Paths.AuthLua)
	if err != nil {
		cfg.Logger.Error("Failed to open auth.lua", "error", err)
		return fmt.Errorf("failed to open auth.lua: %w", err)
	}
	defer file.Close()

	// Read file line by line
	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		cfg.Logger.Error("Error reading auth.lua", "error", err)
		return fmt.Errorf("error reading auth.lua: %w", err)
	}

	// Find start and end of users table
	usersTableStart, usersTableEnd := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "local users") && strings.Contains(trimmed, "=") && strings.Contains(trimmed, "{") {
			usersTableStart = i
			cfg.Logger.Debug("Found users table start", "line", i)
			break
		}
	}
	if usersTableStart < 0 {
		cfg.Logger.Error("Users table not found in auth.lua")
		return fmt.Errorf("users table not found in auth.lua")
	}
	for i := usersTableStart + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			usersTableEnd = i
			cfg.Logger.Debug("Found users table end", "line", i)
			break
		}
	}
	if usersTableEnd < 0 {
		cfg.Logger.Error("Closing brace for users table not found")
		return fmt.Errorf("closing brace for users table not found")
	}

	// Find and remove user entry
	userPattern := fmt.Sprintf(`["%s"]`, user)
	removed := false
	for i := usersTableStart + 1; i < usersTableEnd; i++ {
		if strings.Contains(lines[i], userPattern) {
			lines = append(lines[:i], lines[i+1:]...)
			removed = true
			usersTableEnd--
			cfg.Logger.Debug("User entry removed", "user", user, "line", i)
			break
		}
	}
	if !removed {
		cfg.Logger.Warn("User not found in auth.lua", "user", user)
		return fmt.Errorf("user %s not found in auth.lua", user)
	}

	// Rewrite file
	out, err := os.OpenFile(cfg.Paths.AuthLua, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		cfg.Logger.Error("Failed to create auth.lua", "error", err)
		return fmt.Errorf("failed to create auth.lua: %w", err)
	}
	defer out.Close()

	writer := bufio.NewWriter(out)
	for _, line := range lines {
		cfg.Logger.Trace("Writing line to auth.lua", "line", line)
		if _, err := writer.WriteString(line + "\n"); err != nil {
			cfg.Logger.Error("Error writing to auth.lua", "error", err)
			return fmt.Errorf("error writing to auth.lua: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		cfg.Logger.Error("Error flushing writer", "error", err)
		return fmt.Errorf("error flushing writer: %w", err)
	}

	cfg.Logger.Debug("User removed from auth.lua successfully", "user", user)
	return nil
}
