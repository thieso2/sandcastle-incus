package incusx

import (
	incus "github.com/lxc/incus/v6/client"
)

// EventSource opens all-projects Incus event subscriptions.
type EventSource interface {
	GetEventsAllProjects() (*incus.EventListener, error)
}

// UnixEventSource subscribes to the event bus of the Incus daemon at path,
// over a new connection per subscription. The Incus client's per-connection
// watcher goroutine busy-loops forever when a stream drops and a listener
// re-subscribes on the same connection before the watcher runs.
type UnixEventSource struct {
	path string
}

func NewUnixEventSource(path string) UnixEventSource {
	return UnixEventSource{path: path}
}

func (s UnixEventSource) GetEventsAllProjects() (*incus.EventListener, error) {
	server, err := incus.ConnectIncusUnix(s.path, &incus.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return nil, err
	}
	return server.GetEventsAllProjects()
}
