package subscription

import (
	"context"
	"crypto/tls"
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
// proxy mixed inbound at proxyAddress (host:port). The subscription host is
// resolved and connected by the proxy itself, so unlike the direct fetcher no
// local DNS/public-address checks are applied to the dial.
func NewProxyFetcher(proxyAddress string) (*HTTPFetcher, error) {
	proxyURL, err := url.Parse("http://" + proxyAddress)
	if err != nil {
		return nil, fmt.Errorf("parse proxy address: %w", err)
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 12 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
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
