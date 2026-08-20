package subscription

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
)

func TestProxyFetcherPinsPublicTargetInSOCKSRequest(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	targets := make(chan netip.AddrPort, 1)
	errors := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			err = serveFakeSOCKSSubscription(conn, targets)
		}
		errors <- err
	}()

	fetcher, err := NewProxyFetcher(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	content, metadata, err := fetcher.FetchVia(context.Background(), "http://1.1.1.1/sub", "", "", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "subscription-body" || metadata.Path != "proxy" {
		t.Fatalf("fetch result = %q, %+v", content, metadata)
	}
	if target := <-targets; target != netip.MustParseAddrPort("1.1.1.1:80") {
		t.Fatalf("SOCKS target = %s, want 1.1.1.1:80", target)
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
}

func TestDialSOCKS5RejectsNonzeroReservedByte(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		var greeting [3]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil {
			serverErr <- err
			return
		}
		if _, err := conn.Write([]byte{5, 0}); err != nil {
			serverErr <- err
			return
		}
		request := make([]byte, 10)
		if _, err := io.ReadFull(conn, request); err != nil {
			serverErr <- err
			return
		}
		_, err = conn.Write([]byte{5, 0, 1, 1, 127, 0, 0, 1, 0, 80})
		serverErr <- err
	}()

	conn, err := dialSOCKS5(context.Background(), &net.Dialer{}, listener.Addr().String(), netip.MustParseAddrPort("1.1.1.1:80"))
	if err == nil {
		conn.Close()
		t.Fatal("dialSOCKS5 accepted a nonzero SOCKS5 RSV byte")
	}
	if serverErr := <-serverErr; serverErr != nil {
		t.Fatal(serverErr)
	}
}

func serveFakeSOCKSSubscription(conn net.Conn, targets chan<- netip.AddrPort) error {
	defer conn.Close()
	var greeting [3]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return err
	}
	if greeting != [3]byte{5, 1, 0} {
		return fmt.Errorf("unexpected greeting %v", greeting)
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return err
	}
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header != [4]byte{5, 1, 0, 1} {
		return fmt.Errorf("unexpected request header %v", header)
	}
	var address [4]byte
	var port [2]byte
	if _, err := io.ReadFull(conn, address[:]); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return err
	}
	targets <- netip.AddrPortFrom(netip.AddrFrom4(address), binary.BigEndian.Uint16(port[:]))
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return err
	}
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return err
	}
	request.Body.Close()
	_, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 17\r\nConnection: close\r\n\r\nsubscription-body")
	return err
}
