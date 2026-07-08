package localdns

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"testing"
	"time"
)

// The dns-proxy must relay a query verbatim to the upstream and return the
// upstream's reply, over BOTH transports — UDP is the whole point (resolved's
// link-bound UDP can only reach an on-link address), TCP is what resolved
// falls back to for large replies.
func TestServeDNSProxyForwardsUDPAndTCP(t *testing.T) {
	// Fake upstream: answers every UDP packet and every TCP length-prefixed
	// message with reply = "R" + query.
	upstreamUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamUDP.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, addr, err := upstreamUDP.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = upstreamUDP.WriteToUDP(append([]byte("R"), buf[:n]...), addr)
		}
	}()
	upstreamTCP, err := net.Listen("tcp", upstreamUDP.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamTCP.Close()
	go func() {
		for {
			conn, err := upstreamTCP.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 4096)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				_, _ = conn.Write(append([]byte("R"), buf[:n]...))
			}()
		}
	}()
	upstream := upstreamUDP.LocalAddr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var listen string
	started := make(chan error, 1)
	for attempt := 0; attempt < 10; attempt++ {
		listen = fmt.Sprintf("127.0.0.1:%d", 20000+rand.Intn(20000))
		go func(addr string) { started <- ServeDNSProxy(ctx, addr, upstream) }(listen)
		// Give the listeners a moment; a bind failure surfaces on started.
		select {
		case err := <-started:
			t.Logf("port attempt %s: %v", listen, err)
			continue
		case <-time.After(150 * time.Millisecond):
		}
		break
	}

	// UDP round-trip through the proxy.
	client, err := net.Dial("udp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 64)
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("udp reply: %v", err)
	}
	if !bytes.Equal(reply[:n], []byte("Rhello")) {
		t.Fatalf("udp reply = %q, want %q", reply[:n], "Rhello")
	}

	// TCP round-trip through the proxy.
	tcpClient, err := net.DialTimeout("tcp", listen, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpClient.Close()
	_ = tcpClient.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := tcpClient.Write([]byte("query")); err != nil {
		t.Fatal(err)
	}
	n, err = tcpClient.Read(reply)
	if err != nil {
		t.Fatalf("tcp reply: %v", err)
	}
	if !bytes.Equal(reply[:n], []byte("Rquery")) {
		t.Fatalf("tcp reply = %q, want %q", reply[:n], "Rquery")
	}
}
