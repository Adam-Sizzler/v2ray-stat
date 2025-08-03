package common

import (
	"time"
)

// TimeLocation holds the global timezone.
var TimeLocation *time.Location

// InitTimezone sets the application's timezone based on the provided timezone string.
func InitTimezone(timezone string) {
	if timezone != "" {
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			// Note: Logger is not available here, so we use a fallback to UTC
			TimeLocation = time.UTC
		} else {
			TimeLocation = loc
		}
	} else {
		TimeLocation = time.UTC
	}
}
