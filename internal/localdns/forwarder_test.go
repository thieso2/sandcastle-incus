package localdns

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestForwarderRoutesByStateAndReloads(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "dns.yaml")
	upstreamOne := startUDPResponder(t, []byte{0x01, 0x01})
	upstreamTwo := startUDPResponder(t, []byte{0x02, 0x02})
	writeForwarderState(t, statePath, "myproject.project-tld", upstreamOne)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listen := localUDPAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- Forwarder{StatePath: statePath, Listen: listen, Timeout: time.Second}.Serve(ctx)
	}()
	waitForUDP(t, listen)

	response := queryForwarder(t, listen, dnsQuery("codex.myproject.project-tld"))
	if string(response) != string([]byte{0x01, 0x01}) {
		t.Fatalf("response = %#v", response)
	}

	writeForwarderState(t, statePath, "myproject.project-tld", upstreamTwo)
	response = queryForwarder(t, listen, dnsQuery("codex.myproject.project-tld"))
	if string(response) != string([]byte{0x02, 0x02}) {
		t.Fatalf("response after reload = %#v", response)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("forwarder did not stop")
	}
}

func TestQuestionNameParsesDNSQuestion(t *testing.T) {
	name, err := questionName(dnsQuery("codex.myproject.project-tld"))
	if err != nil {
		t.Fatal(err)
	}
	if name != "codex.myproject.project-tld" {
		t.Fatalf("name = %q", name)
	}
}

func startUDPResponder(t *testing.T, response []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			_, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo(response, addr)
		}
	}()
	return conn.LocalAddr().String()
}

func localUDPAddr(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := conn.LocalAddr().String()
	conn.Close()
	return addr
}

func waitForUDP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("udp", addr)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("UDP listener %s did not start", addr)
}

func queryForwarder(t *testing.T, addr string, packet []byte) []byte {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil {
		t.Fatal(err)
	}
	return response[:n]
}

func writeForwarderState(t *testing.T, path string, domain string, endpoint string) {
	t.Helper()
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	content := "projects:\n" +
		"- owner: alice\n" +
		"  project: myproject\n" +
		"  domain: " + domain + "\n" +
		"  dnsEndpoint:\n" +
		"    ip: " + host + "\n" +
		"    port: " + port + "\n" +
		"  resolver:\n" +
		"    listen: 127.0.0.1:53541\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dnsQuery(name string) []byte {
	packet := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	for _, label := range splitLabels(name) {
		packet = append(packet, byte(len(label)))
		packet = append(packet, []byte(label)...)
	}
	packet = append(packet, 0x00, 0x00, 0x01, 0x00, 0x01)
	return packet
}

func splitLabels(name string) []string {
	labels := []string{}
	start := 0
	for index := range name {
		if name[index] == '.' {
			labels = append(labels, name[start:index])
			start = index + 1
		}
	}
	return append(labels, name[start:])
}
