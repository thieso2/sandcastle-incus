package incusx

import (
	"context"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

type fakeOperation struct{}

func (fakeOperation) AddHandler(func(api.Operation)) (*incus.EventTarget, error) { return nil, nil }
func (fakeOperation) Cancel() error                                              { return nil }
func (fakeOperation) Get() api.Operation                                         { return api.Operation{} }
func (fakeOperation) GetWebsocket(string) (*websocket.Conn, error)               { return nil, nil }
func (fakeOperation) RemoveHandler(*incus.EventTarget) error                     { return nil }
func (fakeOperation) Refresh() error                                             { return nil }
func (fakeOperation) Wait() error                                                { return nil }
func (fakeOperation) WaitContext(context.Context) error                          { return nil }

type fakeRemoteOperation struct{}

func (fakeRemoteOperation) AddHandler(func(api.Operation)) (*incus.EventTarget, error) {
	return nil, nil
}
func (fakeRemoteOperation) CancelTarget() error                { return nil }
func (fakeRemoteOperation) GetTarget() (*api.Operation, error) { return &api.Operation{}, nil }
func (fakeRemoteOperation) Wait() error                        { return nil }
