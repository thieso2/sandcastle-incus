package incusx

import (
	"os"
	"strings"

	"github.com/lxc/incus/v6/shared/cliconfig"
	"gopkg.in/yaml.v2"
)

// LoadCLIConfig wraps cliconfig.LoadConfig and normalizes multi-address
// remotes. Newer incus CLIs enroll a multi-address token as a comma-joined
// addr ("https://a:8443,https://b:8443,…") plus a last_working_address field;
// the vendored SDK treats that joined string as ONE URL and every connection
// fails with `lookup a:8443,https: no such host` (the long-standing
// "multi-address remote mangling"). Prefer last_working_address, else the
// first address in the list.
func LoadCLIConfig(path string) (*cliconfig.Config, error) {
	loaded, err := cliconfig.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	lastWorking := readLastWorkingAddresses(loaded.ConfigPath("config.yml"))
	for name, remote := range loaded.Remotes {
		if !strings.Contains(remote.Addr, ",") {
			continue
		}
		addr := strings.TrimSpace(lastWorking[name])
		if addr == "" {
			addr = strings.TrimSpace(strings.SplitN(remote.Addr, ",", 2)[0])
		}
		remote.Addr = addr
		loaded.Remotes[name] = remote
	}
	return loaded, nil
}

// readLastWorkingAddresses reads the per-remote last_working_address fields
// straight from the config file — the vendored cliconfig struct predates the
// field, so the parsed Config drops it.
func readLastWorkingAddresses(configFile string) map[string]string {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil
	}
	var raw struct {
		Remotes map[string]struct {
			LastWorkingAddress string `yaml:"last_working_address"`
		} `yaml:"remotes"`
	}
	if yaml.Unmarshal(data, &raw) != nil {
		return nil
	}
	addresses := make(map[string]string, len(raw.Remotes))
	for name, remote := range raw.Remotes {
		if strings.TrimSpace(remote.LastWorkingAddress) != "" {
			addresses[name] = remote.LastWorkingAddress
		}
	}
	return addresses
}
