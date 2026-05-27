package incusx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

const (
	connectPlanCacheTTL = 24 * time.Hour
	keyscanCacheTTL     = 24 * time.Hour
)

type connectCacheEntry struct {
	Plan   machine.ConnectPlan `json:"plan"`
	Stored time.Time           `json:"stored"`
}

type connectCacheData struct {
	Plans    map[string]connectCacheEntry `json:"plans,omitempty"`
	Keyscans map[string]time.Time         `json:"keyscans,omitempty"`
}

// ConnectCache is a local disk cache for SSH connect metadata.
// It caches resolved ConnectPlans and keyscan timestamps to avoid repeated Incus API calls
// and SSH host key scans on rapid successive connects.
type ConnectCache struct {
	path string
}

func NewConnectCache(remote string) ConnectCache {
	dir := filepath.Join(config.DefaultConfigDir(), remote)
	return ConnectCache{path: filepath.Join(dir, "connect-cache.json")}
}

// LookupPlan returns a cached ConnectPlan for the exact key if it is still fresh.
func (c ConnectCache) LookupPlan(key string) (machine.ConnectPlan, bool) {
	data := c.readData()
	if data == nil || data.Plans == nil {
		return machine.ConnectPlan{}, false
	}
	entry, ok := data.Plans[key]
	if !ok || time.Since(entry.Stored) > connectPlanCacheTTL {
		return machine.ConnectPlan{}, false
	}
	return entry.Plan, true
}

// LookupPlanByName returns a cached ConnectPlan matching tenant+name across all projects,
// but only when there is exactly one such entry (unambiguous). Used when no default project
// is configured and the user references a machine by bare name.
func (c ConnectCache) LookupPlanByName(tenant, name string) (machine.ConnectPlan, bool) {
	data := c.readData()
	if data == nil || data.Plans == nil {
		return machine.ConnectPlan{}, false
	}
	prefix := tenant + ":"
	suffix := "/" + name
	var match *machine.ConnectPlan
	for key, entry := range data.Plans {
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		if time.Since(entry.Stored) > connectPlanCacheTTL {
			continue
		}
		if match != nil {
			return machine.ConnectPlan{}, false // ambiguous
		}
		plan := entry.Plan
		match = &plan
	}
	if match == nil {
		return machine.ConnectPlan{}, false
	}
	return *match, true
}

// StorePlan writes a ConnectPlan to the cache under key.
// Command and Interactive are not cached; callers re-apply them per invocation.
func (c ConnectCache) StorePlan(key string, plan machine.ConnectPlan) {
	data := c.readData()
	if data == nil {
		data = &connectCacheData{}
	}
	if data.Plans == nil {
		data.Plans = map[string]connectCacheEntry{}
	}
	plan.Command = nil
	plan.Interactive = false
	data.Plans[key] = connectCacheEntry{Plan: plan, Stored: time.Now()}
	_ = c.writeData(data)
}

// InvalidatePlansByNameExcept removes cached plans for tenant/name except the
// selected project. Use this after a live lookup proves a bare machine name is
// currently unambiguous.
func (c ConnectCache) InvalidatePlansByNameExcept(tenant, name, keepProject string) {
	data := c.readData()
	if data == nil || data.Plans == nil {
		return
	}
	prefix := strings.TrimSpace(tenant) + ":"
	suffix := "/" + strings.TrimSpace(name)
	keepKey := prefix + strings.TrimSpace(keepProject) + suffix
	if prefix == ":" || suffix == "/" || strings.TrimSpace(keepProject) == "" {
		return
	}
	var hostsToInvalidate []string
	for key, entry := range data.Plans {
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) || key == keepKey {
			continue
		}
		if h := strings.TrimSpace(entry.Plan.PrivateIP); h != "" {
			hostsToInvalidate = append(hostsToInvalidate, h)
		}
		if h := strings.TrimSpace(entry.Plan.Hostname); h != "" {
			hostsToInvalidate = append(hostsToInvalidate, h)
		}
		delete(data.Plans, key)
	}
	for _, host := range hostsToInvalidate {
		delete(data.Keyscans, host)
	}
	_ = c.writeData(data)
}

// IsKeyscanRecent returns true if hostname was successfully keyscanned within the TTL.
func (c ConnectCache) IsKeyscanRecent(hostname string) bool {
	data := c.readData()
	if data == nil || data.Keyscans == nil {
		return false
	}
	t, ok := data.Keyscans[hostname]
	return ok && time.Since(t) < keyscanCacheTTL
}

// InvalidateTenant removes all cached plans and associated keyscan entries for the given tenant.
// Called after tenant delete or tenant create to prevent stale entries from being used.
func (c ConnectCache) InvalidateTenant(tenant string) {
	data := c.readData()
	if data == nil {
		return
	}
	prefix := strings.TrimSpace(tenant) + ":"
	if prefix == ":" {
		return
	}
	var hostsToInvalidate []string
	for key, entry := range data.Plans {
		if strings.HasPrefix(key, prefix) {
			if h := strings.TrimSpace(entry.Plan.PrivateIP); h != "" {
				hostsToInvalidate = append(hostsToInvalidate, h)
			}
			if h := strings.TrimSpace(entry.Plan.Hostname); h != "" {
				hostsToInvalidate = append(hostsToInvalidate, h)
			}
			delete(data.Plans, key)
		}
	}
	for _, host := range hostsToInvalidate {
		delete(data.Keyscans, host)
	}
	_ = c.writeData(data)
}

// InvalidateAll removes all cached plans and keyscans.
func (c ConnectCache) InvalidateAll() {
	_ = c.writeData(&connectCacheData{})
}

// InvalidatePlan removes the cached plan for key, forcing a fresh Incus lookup next connect.
func (c ConnectCache) InvalidatePlan(key string) {
	data := c.readData()
	if data == nil || data.Plans == nil {
		return
	}
	delete(data.Plans, key)
	_ = c.writeData(data)
}

// InvalidateKeyscan removes the keyscan timestamp for hostname, forcing a fresh scan next connect.
func (c ConnectCache) InvalidateKeyscan(hostname string) {
	data := c.readData()
	if data == nil || data.Keyscans == nil {
		return
	}
	delete(data.Keyscans, hostname)
	_ = c.writeData(data)
}

// MarkKeyscanned records that hostname was just keyscanned.
func (c ConnectCache) MarkKeyscanned(hostname string) {
	data := c.readData()
	if data == nil {
		data = &connectCacheData{}
	}
	if data.Keyscans == nil {
		data.Keyscans = map[string]time.Time{}
	}
	data.Keyscans[hostname] = time.Now()
	_ = c.writeData(data)
}

func (c ConnectCache) readData() *connectCacheData {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return nil
	}
	var data connectCacheData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil
	}
	return &data
}

func (c ConnectCache) writeData(data *connectCacheData) error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o600)
}
