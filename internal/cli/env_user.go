package cli

import (
	"os"
	osuser "os/user"
	"strings"
)

// defaultLocalUnixUsername returns the invoking user's login name, used as the
// default Unix user when provisioning a tenant's machines.
func defaultLocalUnixUsername() string {
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if current, err := osuser.Current(); err == nil && current != nil {
		return strings.TrimSpace(current.Username)
	}
	return ""
}
