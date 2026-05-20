package localdns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type Forwarder struct {
	StatePath string
	Listen    string
	Timeout   time.Duration
}

func (f Forwarder) Serve(ctx context.Context) error {
	listen := f.Listen
	if listen == "" {
		listen = net.JoinHostPort(DefaultListenIP, fmt.Sprint(DefaultListenPort))
	}
	if f.StatePath == "" {
		f.StatePath = DefaultStatePath()
	}
	addr, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buffer := make([]byte, 4096)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		packet := append([]byte(nil), buffer[:n]...)
		go f.handle(ctx, conn, clientAddr, packet)
	}
}

func (f Forwarder) handle(ctx context.Context, conn *net.UDPConn, clientAddr *net.UDPAddr, packet []byte) {
	upstream, err := f.upstreamFor(packet)
	if err != nil {
		return
	}
	response, err := exchange(ctx, upstream, packet, timeoutOrDefault(f.Timeout))
	if err != nil {
		return
	}
	_, _ = conn.WriteToUDP(response, clientAddr)
}

func (f Forwarder) upstreamFor(packet []byte) (string, error) {
	state, err := readState(f.StatePath)
	if err != nil {
		return "", err
	}
	qname, err := questionName(packet)
	if err != nil {
		return "", err
	}
	qname = strings.TrimSuffix(strings.ToLower(qname), ".")
	matchDomain := ""
	matchEndpoint := ""
	for _, project := range state.Projects {
		domain := strings.TrimSuffix(strings.ToLower(project.Domain), ".")
		endpoint, ok := projectUpstreamEndpoint(project)
		if !ok {
			continue
		}
		if qname == domain || strings.HasSuffix(qname, "."+domain) {
			if len(domain) <= len(matchDomain) {
				continue
			}
			matchDomain = domain
			matchEndpoint = endpoint
		}
	}
	if matchEndpoint != "" {
		return matchEndpoint, nil
	}
	return "", fmt.Errorf("no local DNS project for %q", qname)
}

func projectUpstreamEndpoint(project ProjectState) (string, bool) {
	domain := strings.TrimSuffix(strings.ToLower(project.Domain), ".")
	if domain == "" {
		return "", false
	}
	if net.ParseIP(project.DNSEndpoint.IP) == nil {
		return "", false
	}
	if project.DNSEndpoint.Port <= 0 || project.DNSEndpoint.Port > 65535 {
		return "", false
	}
	return net.JoinHostPort(project.DNSEndpoint.IP, fmt.Sprint(project.DNSEndpoint.Port)), true
}

func exchange(ctx context.Context, upstream string, packet []byte, timeout time.Duration) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp", upstream)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil {
		return nil, err
	}
	return response[:n], nil
}

func questionName(packet []byte) (string, error) {
	if len(packet) < 13 {
		return "", fmt.Errorf("DNS packet too short")
	}
	offset := 12
	labels := []string{}
	for {
		if offset >= len(packet) {
			return "", fmt.Errorf("truncated DNS question")
		}
		length := int(packet[offset])
		offset++
		if length == 0 {
			break
		}
		if length&0xc0 != 0 {
			return "", fmt.Errorf("compressed DNS question names are not supported")
		}
		if offset+length > len(packet) {
			return "", fmt.Errorf("truncated DNS label")
		}
		labels = append(labels, string(packet[offset:offset+length]))
		offset += length
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("empty DNS question")
	}
	return strings.Join(labels, "."), nil
}

func timeoutOrDefault(timeout time.Duration) time.Duration {
	if timeout == 0 {
		return 2 * time.Second
	}
	return timeout
}
