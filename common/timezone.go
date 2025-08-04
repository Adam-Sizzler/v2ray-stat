package common

import (
	"time"
	"v2ray-stat/logger"
)

// timeLocation holds the global timezone.
var TimeLocation *time.Location

// InitTimezone sets the application's timezone based on the provided timezone string.
func InitTimezone(timezone string, logger *logger.Logger) {
	if timezone != "" {
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			logger.Error("Failed to load timezone", "timezone", timezone, "error", err)
			logger.Info("Falling back to UTC")
			TimeLocation = time.UTC
		} else {
			logger.Info("Timezone set successfully", "timezone", timezone)
			TimeLocation = loc
		}
	} else {
		logger.Info("No timezone specified, using UTC")
		TimeLocation = time.UTC
	}
}
