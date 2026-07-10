package cli

import (
	"fmt"
	"os"
)

// verboseCLI prints a [verbose] diagnostic when VERBOSE=1. It lived in the
// deleted v1 `sc workload` command file; cloud-identity is its consumer now.
func verboseCLI(config commandConfig, format string, values ...any) {
	if os.Getenv("VERBOSE") != "1" || config.stderr == nil {
		return
	}
	fmt.Fprintf(config.stderr, "[verbose] "+format+"\n", values...)
}
