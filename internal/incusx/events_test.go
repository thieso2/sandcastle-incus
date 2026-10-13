package incusx

import (
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeEventBus serves Incus's /1.0/events websocket on a unix socket and can
// drop every open stream, as incusd does when it restarts or sheds load.
type fakeEventBus struct {
	path  string
	mu    sync.Mutex
	conns []*websocket.Conn
}

func newFakeEventBus(t *testing.T) *fakeEventBus {
	t.Helper()
	bus := &fakeEventBus{path: filepath.Join(t.TempDir(), "unix.socket")}
	listener, err := net.Listen("unix", bus.path)
	if err != nil {
		t.Fatal(err)
	}
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.0/events" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		bus.mu.Lock()
		bus.conns = append(bus.conns, conn)
		bus.mu.Unlock()
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return bus
}

func (b *fakeEventBus) dropAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, conn := range b.conns {
		_ = conn.Close()
	}
	b.conns = nil
}

// eventWatchers counts the Incus client's per-connection watcher goroutines
// (the first closure in getEvents).
func eventWatchers() int {
	buf := make([]byte, 1<<22)
	stacks := string(buf[:runtime.Stack(buf, true)])
	return strings.Count(stacks, "client.(*ProtocolIncus).getEvents.func1(")
}

func (b *fakeEventBus) streams() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.conns)
}

// Subscribers re-subscribe the moment the stream drops, as the auth app's
// event loops do. On a shared connection that re-subscription can keep the
// dropped stream's watcher alive, and with its stop channel closed it
// busy-loops forever.
func TestUnixEventSourceResubscribeAfterDropLeavesNoWatcherBehind(t *testing.T) {
	bus := newFakeEventBus(t)
	source := NewUnixEventSource(bus.path)

	const subscribers, drops = 4, 50
	stop := make(chan struct{})
	var loops sync.WaitGroup
	for range subscribers {
		loops.Add(1)
		go func() {
			defer loops.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				listener, err := source.GetEventsAllProjects()
				if err != nil {
					t.Error(err)
					return
				}
				_ = listener.Wait()
			}
		}()
	}
	for range drops {
		time.Sleep(10 * time.Millisecond)
		bus.dropAll()
	}
	time.Sleep(100 * time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for eventWatchers() > bus.streams() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	watchers, streams := eventWatchers(), bus.streams()
	close(stop)
	bus.dropAll()
	loops.Wait()
	if watchers > streams {
		t.Fatalf("%d event watcher goroutines for %d live streams after %d drops: %d leaked", watchers, streams, drops, watchers-streams)
	}
}
