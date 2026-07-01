package incusx

import (
	"fmt"
	"time"
)

// formatVerboseDuration renders a duration for verbose step logging.
func formatVerboseDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return fmt.Sprintf("%dus", duration.Microseconds())
	}
	return duration.Round(time.Millisecond).String()
}
