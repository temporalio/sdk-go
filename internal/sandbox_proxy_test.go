package internal

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxProxy_AllowedHost(t *testing.T) {
	// Start a test HTTP server to act as the upstream target.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	// Parse upstream address.
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	proxy, err := newSandboxProxy([]SandboxHostPort{
		{Host: host, Port: port},
	})
	if err != nil {
		t.Fatalf("newSandboxProxy: %v", err)
	}
	defer proxy.Close()

	// Connect to the proxy and send a CONNECT request.
	conn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Send CONNECT.
	fmt.Fprintf(conn, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port, host, port)

	// Read response.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Now the tunnel is established. Send an HTTP request through it.
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
	resp, err = http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read GET response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSandboxProxy_BlockedHost(t *testing.T) {
	proxy, err := newSandboxProxy([]SandboxHostPort{
		{Host: "allowed.example.com", Port: 443},
	})
	if err != nil {
		t.Fatalf("newSandboxProxy: %v", err)
	}
	defer proxy.Close()

	conn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Try to CONNECT to a host not in the allowlist.
	fmt.Fprintf(conn, "CONNECT evil.com:80 HTTP/1.1\r\nHost: evil.com:80\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}

func TestSandboxProxy_EmptyAllowlist(t *testing.T) {
	proxy, err := newSandboxProxy(nil)
	if err != nil {
		t.Fatalf("newSandboxProxy: %v", err)
	}
	defer proxy.Close()

	conn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// All hosts should be blocked.
	fmt.Fprintf(conn, "CONNECT anything.com:443 HTTP/1.1\r\nHost: anything.com:443\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}

func TestSandboxProxy_WildcardAllowlist(t *testing.T) {
	proxy, err := newSandboxProxy([]SandboxHostPort{
		{Host: "*.example.com", Port: 443},
		{Host: "exact.com", Port: 443},
	})
	if err != nil {
		t.Fatalf("newSandboxProxy: %v", err)
	}
	defer proxy.Close()

	tests := []struct {
		target string
		want   bool
	}{
		{"sub.example.com:443", true},
		{"deep.sub.example.com:443", true},
		{"example.com:443", true},         // wildcard also matches root domain
		{"exact.com:443", true},           // exact match
		{"sub.exact.com:443", false},      // no wildcard for exact.com
		{"evil.com:443", false},           // not in allowlist
		{"sub.example.com:80", false},     // wrong port
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := proxy.isAllowed(tt.target)
			if got != tt.want {
				t.Errorf("isAllowed(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestSandboxProxy_UDS(t *testing.T) {
	// Start a test HTTP server as upstream.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello via uds"))
	}))
	defer upstream.Close()

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	// Create UDS proxy allowing the upstream host.
	proxy, err := newSandboxProxyUDS([]SandboxHostPort{
		{Host: host, Port: port},
	})
	if err != nil {
		t.Fatalf("newSandboxProxyUDS: %v", err)
	}
	defer proxy.Close()

	if proxy.SockDir() == "" {
		t.Fatal("expected non-empty SockDir for UDS proxy")
	}

	// Connect via Unix socket.
	conn, err := net.Dial("unix", proxy.Addr())
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	// Send CONNECT.
	fmt.Fprintf(conn, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port, host, port)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Send HTTP request through tunnel.
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
	resp, err = http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read GET response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSandboxProxy_UDS_Blocked(t *testing.T) {
	proxy, err := newSandboxProxyUDS([]SandboxHostPort{
		{Host: "allowed.example.com", Port: 443},
	})
	if err != nil {
		t.Fatalf("newSandboxProxyUDS: %v", err)
	}
	defer proxy.Close()

	conn, err := net.Dial("unix", proxy.Addr())
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT evil.com:80 HTTP/1.1\r\nHost: evil.com:80\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}

func TestSandboxProxy_NonConnectMethod(t *testing.T) {
	proxy, err := newSandboxProxy(nil)
	if err != nil {
		t.Fatalf("newSandboxProxy: %v", err)
	}
	defer proxy.Close()

	conn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Send a GET instead of CONNECT.
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}
