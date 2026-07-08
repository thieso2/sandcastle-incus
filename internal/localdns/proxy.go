package localdns

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// ServeDNSProxy runs a minimal DNS forwarder: it listens on listen (UDP and
// TCP) and relays every query verbatim to upstream, returning the upstream's
// reply. It exists because systemd-resolved binds a link scope's UDP sockets
// to the scope's interface — and the Sandcastle per-suffix scope lives on a
// dummy link, so resolved's UDP queries to the tenant CoreDNS were transmitted
// into the dummy and dropped (resolved only worked after silently degrading
// the server to TCP, and it re-probes UDP after every idle period, failing one
// lookup each time — seen live on majestix). With the scope's DNS server set
// to an address ON the dummy link, bound-to-link delivery is local and works;
// this proxy answers there and forwards over normal routing.
//
// It blocks until ctx is cancelled or a listener fails.
func ServeDNSProxy(ctx context.Context, listen string, upstream string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		return fmt.Errorf("resolve listen address: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp %s: %w", listen, err)
	}
	tcpListener, err := net.Listen("tcp", listen)
	if err != nil {
		_ = udpConn.Close()
		return fmt.Errorf("listen tcp %s: %w", listen, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- serveUDPProxy(ctx, udpConn, upstream)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- serveTCPProxy(ctx, tcpListener, upstream)
	}()
	go func() {
		<-ctx.Done()
		_ = udpConn.Close()
		_ = tcpListener.Close()
	}()

	err = <-errs
	cancel()
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

const dnsProxyTimeout = 5 * time.Second

func serveUDPProxy(ctx context.Context, conn *net.UDPConn, upstream string) error {
	buf := make([]byte, 65535)
	for {
		n, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("udp read: %w", err)
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		go func() {
			up, err := net.DialTimeout("udp", upstream, dnsProxyTimeout)
			if err != nil {
				return
			}
			defer up.Close()
			_ = up.SetDeadline(time.Now().Add(dnsProxyTimeout))
			if _, err := up.Write(query); err != nil {
				return
			}
			reply := make([]byte, 65535)
			n, err := up.Read(reply)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(reply[:n], client)
		}()
	}
}

func serveTCPProxy(ctx context.Context, listener net.Listener, upstream string) error {
	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tcp accept: %w", err)
		}
		go func() {
			defer client.Close()
			up, err := net.DialTimeout("tcp", upstream, dnsProxyTimeout)
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, client); done <- struct{}{} }()
			go func() { _, _ = io.Copy(client, up); done <- struct{}{} }()
			select {
			case <-done:
			case <-ctx.Done():
			}
		}()
	}
}
