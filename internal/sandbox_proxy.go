package internal

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sandboxProxy is an HTTP CONNECT proxy that enforces a domain allowlist.
// It can listen on either a Unix domain socket (preferred — enables
// --network=none for strong network isolation) or a localhost TCP port
// (fallback for environments where --host-uds=open is unavailable).
// DNS resolution happens on the host side.
//
// Allowlist entries support two forms:
//   - "host:port"   — exact match (e.g. "github.com:443")
//   - "*.host:port" — matches any subdomain (e.g. "*.github.com:443"
//     matches "github.com" and all subdomains like "codeload.github.com:443")
type sandboxProxy struct {
	exactHosts    map[string]bool     // "host:port" -> true
	wildcardPorts map[string][]string // port -> list of suffix domains (e.g. ".github.com")
	listener      net.Listener
	addr          string // TCP: "127.0.0.1:<port>", UDS: socket file path
	sockDir       string // non-empty for UDS mode: directory containing the socket file
	server        *http.Server
	wg            sync.WaitGroup
}

func buildAllowlist(allowedHosts []SandboxHostPort) (map[string]bool, map[string][]string) {
	exact := make(map[string]bool, len(allowedHosts))
	wildcards := make(map[string][]string)
	for _, hp := range allowedHosts {
		host := strings.ToLower(hp.Host)
		port := fmt.Sprintf("%d", hp.Port)
		if strings.HasPrefix(host, "*.") {
			suffix := host[1:] // "*.github.com" → ".github.com"
			wildcards[port] = append(wildcards[port], suffix)
		} else {
			key := fmt.Sprintf("%s:%s", host, port)
			exact[key] = true
		}
	}
	return exact, wildcards
}

// newSandboxProxyUDS creates and starts an HTTP CONNECT proxy listening on
// a Unix domain socket. The socket is created in a temp directory; the
// directory path is returned via SockDir() for bind-mounting into the sandbox.
// Use with runsc --network=none --host-uds=open for strong network isolation.
func newSandboxProxyUDS(allowedHosts []SandboxHostPort) (*sandboxProxy, error) {
	exact, wildcards := buildAllowlist(allowedHosts)

	sockDir, err := os.MkdirTemp("", "temporal-proxy-")
	if err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	sockPath := filepath.Join(sockDir, "proxy.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		os.RemoveAll(sockDir)
		return nil, fmt.Errorf("listen unix: %w", err)
	}

	p := &sandboxProxy{
		exactHosts:    exact,
		wildcardPorts: wildcards,
		listener:      listener,
		addr:          sockPath,
		sockDir:       sockDir,
	}

	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = p.server.Serve(listener)
	}()

	return p, nil
}

// newSandboxProxy creates and starts an HTTP CONNECT proxy listening on
// a localhost TCP port. Use with runsc --network=host as a fallback when
// --host-uds=open is not available.
func newSandboxProxy(allowedHosts []SandboxHostPort) (*sandboxProxy, error) {
	exact, wildcards := buildAllowlist(allowedHosts)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	p := &sandboxProxy{
		exactHosts:    exact,
		wildcardPorts: wildcards,
		listener:      listener,
		addr:          listener.Addr().String(),
	}

	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = p.server.Serve(listener)
	}()

	return p, nil
}

// SockDir returns the directory containing the Unix socket, or empty for TCP mode.
// This directory should be bind-mounted into the sandbox.
func (p *sandboxProxy) SockDir() string {
	return p.sockDir
}

// ServeHTTP handles HTTP CONNECT requests (proxy protocol).
func (p *sandboxProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, fmt.Sprintf("%s not supported for request to %s, only CONNECT is supported", r.Method, r.Host), http.StatusMethodNotAllowed)
		return
	}

	// r.Host is "host:port" from the CONNECT request.
	target := strings.ToLower(r.Host)
	if !p.isAllowed(target) {
		http.Error(w, fmt.Sprintf("host %s not in allowlist", r.Host), http.StatusForbidden)
		return
	}

	// Dial the target on the host side. If an upstream HTTP(S) proxy is
	// configured via environment (HTTP_PROXY/HTTPS_PROXY), tunnel through
	// it using a CONNECT request. Otherwise dial directly.
	targetConn, err := p.dialTarget(r.Host)
	if err != nil {
		http.Error(w, fmt.Sprintf("dial %s: %v", r.Host, err), http.StatusBadGateway)
		return
	}

	// Hijack the client connection.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		targetConn.Close()
		http.Error(w, fmt.Sprintf("hijack not supported for request to %s", r.Host), http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		targetConn.Close()
		return
	}

	// Send 200 OK to client to signal tunnel established.
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Bidirectional copy. When one direction finishes (EOF), only half-close
	// that side so the other direction can complete. A full Close() would
	// kill the entire connection, truncating large responses (e.g. git packs).
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(targetConn, clientConn)
		if cw, ok := targetConn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, targetConn)
		if cw, ok := clientConn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
	targetConn.Close()
	clientConn.Close()
}

// dialTarget connects to the target host:port, optionally tunneling through an
// upstream HTTP proxy if HTTPS_PROXY or HTTP_PROXY is set in the environment.
func (p *sandboxProxy) dialTarget(target string) (net.Conn, error) {
	upstreamProxy := os.Getenv("HTTPS_PROXY")
	if upstreamProxy == "" {
		upstreamProxy = os.Getenv("https_proxy")
	}
	if upstreamProxy == "" {
		upstreamProxy = os.Getenv("HTTP_PROXY")
	}
	if upstreamProxy == "" {
		upstreamProxy = os.Getenv("http_proxy")
	}
	if upstreamProxy == "" {
		return net.DialTimeout("tcp", target, 10*time.Second)
	}

	proxyURL, err := url.Parse(upstreamProxy)
	if err != nil {
		return net.DialTimeout("tcp", target, 10*time.Second)
	}

	proxyAddr := proxyURL.Host
	if !strings.Contains(proxyAddr, ":") {
		proxyAddr += ":3128"
	}

	conn, err := net.DialTimeout("tcp", proxyAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial upstream proxy %s: %w", proxyAddr, err)
	}

	// Send CONNECT request to upstream proxy.
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write CONNECT to upstream proxy: %w", err)
	}

	// Read response.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read upstream proxy response: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("upstream proxy CONNECT returned %d", resp.StatusCode)
	}

	return conn, nil
}

// isAllowed checks whether the target "host:port" is permitted by the allowlist.
func (p *sandboxProxy) isAllowed(target string) bool {
	// Exact match.
	if p.exactHosts[target] {
		return true
	}
	// Wildcard match: "*.github.com" matches both "github.com" and
	// "codeload.github.com" (root + any subdomain), consistent with
	// TLS certificate and proxy PAC file conventions.
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	for _, suffix := range p.wildcardPorts[port] {
		// suffix is ".github.com" (from "*.github.com")
		// Match subdomains: "codeload.github.com" has suffix ".github.com"
		// Match root: "github.com" == suffix without leading dot
		if strings.HasSuffix(host, suffix) || host == suffix[1:] {
			return true
		}
	}
	return false
}

// Addr returns the proxy's listen address (e.g. "127.0.0.1:12345").
func (p *sandboxProxy) Addr() string {
	return p.addr
}

// Close shuts down the proxy and cleans up the socket directory (if UDS mode).
func (p *sandboxProxy) Close() error {
	err := p.server.Close()
	p.wg.Wait()
	if p.sockDir != "" {
		os.RemoveAll(p.sockDir)
	}
	return err
}
