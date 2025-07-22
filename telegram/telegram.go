package telegram

import (
	"fmt"
	"net/http"
	"net/url"
	"os"

	"v2ray-stat/config"
)

// SendNotification sends a notification to a Telegram chat using the provided configuration.
func SendNotification(cfg *config.Config, message string) error {
	// Retrieve hostname for including in the message
	hostname, err := os.Hostname()
	if err != nil {
		cfg.Logger.Error("Failed to retrieve hostname", "error", err)
		hostname = "unknown"
	}

	// Format message with hostname and content
	formattedMessage := fmt.Sprintf("💻 Host: *%s*\n\n%s", hostname, message)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?parse_mode=markdown", cfg.Telegram.BotToken)
	data := url.Values{
		"chat_id": {cfg.Telegram.ChatID},
		"text":    {formattedMessage},
	}

	// Send HTTP POST request to Telegram API
	resp, err := http.PostForm(apiURL, data)
	if err != nil {
		cfg.Logger.Error("Failed to send Telegram notification", "error", err)
		return fmt.Errorf("failed to send notification: %v", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		cfg.Logger.Error("Failed to send Telegram notification", "status_code", resp.StatusCode)
		return fmt.Errorf("failed to send notification, status: %d", resp.StatusCode)
	}

	// Log successful notification
	cfg.Logger.Debug("Telegram notification sent successfully", "chat_id", cfg.Telegram.ChatID)
	return nil
}
