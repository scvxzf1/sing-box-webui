package netresolve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const (
	dohEndpoint        = "https://1.1.1.1/dns-query"
	maxDNSResponseSize = 64 << 10
	resolveTimeout     = 3 * time.Second
)

var dohHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: resolveTimeout}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: resolveTimeout,
	},
	Timeout: resolveTimeout,
}

func PublicAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	return ResolveDoH(ctx, dohHTTPClient, dohEndpoint, host)
}

func ResolveDoH(ctx context.Context, client *http.Client, endpoint, host string) ([]netip.Addr, error) {
	addresses := make([]netip.Addr, 0, 4)
	seen := make(map[netip.Addr]struct{}, 4)
	var queryErrors []error
	for _, recordType := range []string{"A", "AAAA"} {
		requestURL, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse DNS endpoint: %w", err)
		}
		query := requestURL.Query()
		query.Set("name", host)
		query.Set("type", recordType)
		requestURL.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/dns-json")
		response, err := client.Do(request)
		if err != nil {
			queryErrors = append(queryErrors, err)
			continue
		}
		var payload struct {
			Status int `json:"Status"`
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, maxDNSResponseSize)).Decode(&payload)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			queryErrors = append(queryErrors, fmt.Errorf("DNS over HTTPS returned HTTP %d", response.StatusCode))
			continue
		}
		if decodeErr != nil {
			queryErrors = append(queryErrors, decodeErr)
			continue
		}
		if payload.Status != 0 {
			queryErrors = append(queryErrors, fmt.Errorf("DNS response status %d", payload.Status))
			continue
		}
		for _, answer := range payload.Answer {
			if answer.Type != 1 && answer.Type != 28 {
				continue
			}
			address, err := netip.ParseAddr(answer.Data)
			if err != nil {
				continue
			}
			address = address.Unmap()
			if _, exists := seen[address]; !exists {
				seen[address] = struct{}{}
				addresses = append(addresses, address)
			}
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve host with DNS over HTTPS: %w", errors.Join(queryErrors...))
	}
	return addresses, nil
}
