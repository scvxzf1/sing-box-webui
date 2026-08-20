package subscription

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sing-box-webui/internal/netresolve"
	"sing-box-webui/internal/netsafety"
)

const maxSubscriptionBytes = 4 << 20

type FetchMetadata struct {
	ETag         string
	LastModified string
	NotModified  bool
	// Path reports how the subscription content was retrieved: "direct" or
	// "proxy". Empty for a 304 Not-Modified response (no body fetched).
	Path string
}

type FetchClient interface {
	Fetch(context.Context, string, string, string) ([]byte, FetchMetadata, error)
}

type HTTPFetcher struct {
	client *http.Client
}

func NewFetcher() *HTTPFetcher {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 12 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := netresolve.PublicAddresses(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve subscription host: %w", err)
		}
		for _, address := range addresses {
			if !netsafety.AllowedPublicAddress(address) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		}
		return nil, fmt.Errorf("subscription host resolved only to blocked addresses")
	}

	return &HTTPFetcher{client: &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validateSubscriptionURL(request.URL)
		},
	}}
}

// NewProxyFetcher builds a fetcher whose requests are routed through the local
// mixed inbound at proxyAddress. Targets are resolved by the controlled DoH
// resolver and passed to SOCKS5 as IP literals so the proxy cannot re-resolve
// an attacker-controlled hostname to a private address.
func NewProxyFetcher(proxyAddress string) (*HTTPFetcher, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(proxyAddress))
	if err != nil || host == "" || port == "" {
		return nil, fmt.Errorf("proxy address must be host:port")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("proxy address must be a loopback address")
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return nil, fmt.Errorf("proxy address port is invalid")
	}
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 12 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	transport.DialContext = pinnedSOCKS5Dialer(net.JoinHostPort(host, strconv.FormatUint(parsedPort, 10)))
	return &HTTPFetcher{client: &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if err := validateSubscriptionURL(request.URL); err != nil {
				return err
			}
			return nil
		},
	}}, nil
}

func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL, etag, lastModified string) ([]byte, FetchMetadata, error) {
	return f.fetch(ctx, rawURL, etag, lastModified, "")
}

// FetchVia behaves like Fetch but reports the given path label in metadata,
// used when the request was routed through a proxy fallback.
func (f *HTTPFetcher) FetchVia(ctx context.Context, rawURL, etag, lastModified, path string) ([]byte, FetchMetadata, error) {
	return f.fetch(ctx, rawURL, etag, lastModified, path)
}

func (f *HTTPFetcher) fetch(ctx context.Context, rawURL, etag, lastModified, path string) ([]byte, FetchMetadata, error) {
	if path == "" {
		path = "direct"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, FetchMetadata{}, fmt.Errorf("parse subscription URL: %w", err)
	}
	if err := validateSubscriptionURL(parsed); err != nil {
		return nil, FetchMetadata{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, FetchMetadata{}, fmt.Errorf("create subscription request: %w", err)
	}
	request.Header.Set("Accept", "text/plain, application/json;q=0.9, */*;q=0.5")
	request.Header.Set("User-Agent", "sing-box-webui/0.1")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		request.Header.Set("If-Modified-Since", lastModified)
	}

	response, err := f.client.Do(request)
	if err != nil {
		return nil, FetchMetadata{}, fmt.Errorf("fetch subscription: %w", err)
	}
	defer response.Body.Close()

	metadata := FetchMetadata{
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
	}
	if response.StatusCode == http.StatusNotModified {
		metadata.NotModified = true
		return nil, metadata, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, FetchMetadata{}, fmt.Errorf("subscription server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxSubscriptionBytes {
		return nil, FetchMetadata{}, fmt.Errorf("subscription exceeds %d bytes", maxSubscriptionBytes)
	}

	content, err := io.ReadAll(io.LimitReader(response.Body, maxSubscriptionBytes+1))
	if err != nil {
		return nil, FetchMetadata{}, fmt.Errorf("read subscription: %w", err)
	}
	if len(content) > maxSubscriptionBytes {
		return nil, FetchMetadata{}, fmt.Errorf("subscription exceeds %d bytes", maxSubscriptionBytes)
	}
	metadata.Path = path
	return content, metadata, nil
}

func pinnedSOCKS5Dialer(proxyAddress string) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, _ string, target string) (net.Conn, error) {
		host, portText, err := net.SplitHostPort(target)
		if err != nil {
			return nil, err
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("subscription target port is invalid")
		}
		addresses, err := netresolve.PublicAddresses(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve subscription host: %w", err)
		}
		var dialErrors []error
		for _, address := range addresses {
			address = address.Unmap()
			if !netsafety.AllowedPublicAddress(address) {
				dialErrors = append(dialErrors, fmt.Errorf("blocked address %s", address))
				continue
			}
			conn, err := dialSOCKS5(ctx, dialer, proxyAddress, netip.AddrPortFrom(address, uint16(port)))
			if err == nil {
				return conn, nil
			}
			dialErrors = append(dialErrors, err)
		}
		if len(dialErrors) == 0 {
			return nil, fmt.Errorf("subscription host has no addresses")
		}
		return nil, fmt.Errorf("connect to subscription host through proxy: %w", errors.Join(dialErrors...))
	}
}

func dialSOCKS5(ctx context.Context, dialer *net.Dialer, proxyAddress string, target netip.AddrPort) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}
	completed := false
	defer func() {
		if !completed {
			_ = conn.Close()
		}
	}()
	deadline := time.Now().Add(8 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if err := writeAll(conn, []byte{5, 1, 0}); err != nil {
		return nil, err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return nil, err
	}
	if greeting != [2]byte{5, 0} {
		return nil, fmt.Errorf("SOCKS5 proxy rejected unauthenticated access")
	}
	request := []byte{5, 1, 0}
	if target.Addr().Is4() {
		request = append(request, 1)
		value := target.Addr().As4()
		request = append(request, value[:]...)
	} else {
		request = append(request, 4)
		value := target.Addr().As16()
		request = append(request, value[:]...)
	}
	request = binary.BigEndian.AppendUint16(request, target.Port())
	if err := writeAll(conn, request); err != nil {
		return nil, err
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 {
		return nil, fmt.Errorf("SOCKS5 proxy connection failed with status %d", reply[1])
	}
	addressBytes := 0
	switch reply[3] {
	case 1:
		addressBytes = 4
	case 4:
		addressBytes = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return nil, err
		}
		addressBytes = int(length[0])
	default:
		return nil, fmt.Errorf("SOCKS5 proxy returned an invalid address type")
	}
	if _, err := io.CopyN(io.Discard, conn, int64(addressBytes+2)); err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	completed = true
	return conn, nil
}

func writeAll(writer io.Writer, content []byte) error {
	for len(content) > 0 {
		written, err := writer.Write(content)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return nil
}

func validateSubscriptionURL(parsed *url.URL) error {
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("subscription URL must use HTTP or HTTPS")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("subscription URL host is required")
	}
	if parsed.User != nil {
		return fmt.Errorf("subscription URL userinfo is not allowed")
	}
	if parsed.Port() != "" {
		port, err := strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 {
			return fmt.Errorf("subscription URL port is invalid")
		}
	}
	if ip, err := netip.ParseAddr(strings.Trim(parsed.Hostname(), "[]")); err == nil && !netsafety.AllowedPublicAddress(ip) {
		return fmt.Errorf("subscription URL points to a blocked address")
	}
	return nil
}
