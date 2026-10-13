package incusx

import (
	incus "github.com/lxc/incus/v6/client"
)

// EventSource opens all-projects Incus event subscriptions.
type EventSource interface {
	GetEventsAllProjects() (*incus.EventListener, error)
}

// UnixEventSource subscribes to the event bus of the Incus daemon at path.
type UnixEventSource struct {
	path   string
	server incus.InstanceServer
}

func NewUnixEventSource(path string) (*UnixEventSource, error) {
	server, err := incus.ConnectIncusUnix(path, &incus.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return nil, err
	}
	return &UnixEventSource{path: path, server: server}, nil
}

func (s *UnixEventSource) GetEventsAllProjects() (*incus.EventListener, error) {
	return s.server.GetEventsAllProjects()
}
