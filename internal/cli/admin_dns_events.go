package cli

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

var (
	dnsReconcilerOnce sync.Once
	dnsReconciler     *incusx.V2DNSReconciler
)

// authAppDNSReconciler returns the process-wide stateful DNS reconciler, so the
// unchanged-zone cache is shared between the periodic loop and event-triggered
// passes (ADR-0018).
func authAppDNSReconciler(server incus.InstanceServer, store tenant.IncusTenantStore, prefix string) *incusx.V2DNSReconciler {
	dnsReconcilerOnce.Do(func() {
		dnsReconciler = &incusx.V2DNSReconciler{Server: server, Store: store, Prefix: prefix}
	})
	return dnsReconciler
}

// subscribeInstanceLifecycleEvents watches Incus lifecycle events across all
// projects and calls notify() on every instance event — the trigger half of
// ADR-0018's event-driven DNS registration. It blocks until ctx is done,
// reconnecting with backoff when the event socket drops. Filtering finer than
// "instance-*" is not worth it: the reconciler skips unchanged zones, so a
// spurious trigger costs one render.
func subscribeInstanceLifecycleEvents(ctx context.Context, server incus.InstanceServer, notify func()) {
	for ctx.Err() == nil {
		listener, err := server.GetEventsAllProjects()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		_, err = listener.AddHandler([]string{api.EventTypeLifecycle}, func(event api.Event) {
			var lifecycle api.EventLifecycle
			if json.Unmarshal(event.Metadata, &lifecycle) != nil {
				return
			}
			if strings.HasPrefix(lifecycle.Action, "instance-") {
				notify()
			}
		})
		if err != nil {
			listener.Disconnect()
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				listener.Disconnect()
			case <-done:
			}
		}()
		_ = listener.Wait()
		close(done)
	}
}
