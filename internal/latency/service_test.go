package latency

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sing-box-webui/internal/proxychain"
	"sing-box-webui/internal/subscription"
)

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (runner blockingRunner) Test(context.Context, []subscription.Node) ([]Result, error) {
	runner.started <- struct{}{}
	<-runner.release
	return []Result{}, nil
}

type fakeSource struct {
	nodes []subscription.Node
	err   error
}

func (source fakeSource) ProbeNodes(string, []string) ([]subscription.Node, error) {
	return source.nodes, source.err
}

type fakeRunner struct {
	results []Result
	err     error
}

func (runner fakeRunner) Test(context.Context, []subscription.Node) ([]Result, error) {
	return runner.results, runner.err
}

type chainRunner struct {
	results []Result
}

func (runner chainRunner) Test(context.Context, []subscription.Node) ([]Result, error) {
	return nil, nil
}

func (runner chainRunner) TestChain(context.Context, []subscription.Node, subscription.Node) ([]Result, error) {
	return runner.results, nil
}

func TestServiceReturnsRunnerResults(t *testing.T) {
	t.Parallel()
	results := []Result{{NodeID: "first", Name: "First", Status: StatusOK}, {NodeID: "second", Name: "Second", Status: StatusTimeout}}
	service := &Service{
		source: fakeSource{nodes: []subscription.Node{{ID: "first"}, {ID: "second"}}},
		runner: fakeRunner{results: results},
	}

	response, err := service.Test(context.Background(), "subscription", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].NodeID != "first" || response.Items[1].NodeID != "second" {
		t.Fatalf("unexpected results: %+v", response.Items)
	}
}

func TestServiceAddsFullPathToChainResults(t *testing.T) {
	t.Parallel()
	entry := subscription.Node{ID: "entry", Name: "Entry"}
	exit := subscription.Node{ID: "exit", Name: "Exit"}
	service := &Service{runner: chainRunner{results: []Result{{NodeID: entry.ID, Name: entry.Name, Status: StatusOK}}}}
	response, err := service.TestChain(context.Background(), proxychain.Resolved{EntryNode: &entry, EntryNodes: []subscription.Node{entry}, ExitNode: exit})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || len(response.Items[0].Path) != 2 || response.Items[0].Path[0] != "Entry" || response.Items[0].Path[1] != "Exit" {
		t.Fatalf("chain result path = %+v", response.Items)
	}
}

func TestServiceRequiresSingBox(t *testing.T) {
	t.Parallel()
	service := NewService(fakeSource{}, nil, t.TempDir())
	if _, err := service.Test(context.Background(), "subscription", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Test() error = %v, want ErrUnavailable", err)
	}
}

func TestServiceLimitsBatchSize(t *testing.T) {
	t.Parallel()
	nodes := make([]subscription.Node, MaxTargets+1)
	for index := range nodes {
		nodes[index].ID = fmt.Sprintf("node-%d", index)
	}
	service := &Service{source: fakeSource{nodes: nodes}, runner: fakeRunner{}}
	if _, err := service.Test(context.Background(), "subscription", nil); err == nil {
		t.Fatal("oversized batch was accepted")
	}
}

func TestServiceAllowsFourConcurrentTests(t *testing.T) {
	t.Parallel()
	runner := blockingRunner{started: make(chan struct{}, MaxConcurrentTests), release: make(chan struct{})}
	service := &Service{
		source: fakeSource{nodes: []subscription.Node{{ID: "node"}}},
		runner: runner,
	}
	done := make(chan error, MaxConcurrentTests)
	for range MaxConcurrentTests {
		go func() {
			_, err := service.Test(context.Background(), "subscription", []string{"node"})
			done <- err
		}()
	}
	for range MaxConcurrentTests {
		select {
		case <-runner.started:
		case <-time.After(time.Second):
			t.Fatal("concurrent latency test did not start")
		}
	}
	if _, err := service.Test(context.Background(), "subscription", []string{"node"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("fifth concurrent Test() error = %v, want ErrBusy", err)
	}
	close(runner.release)
	for range MaxConcurrentTests {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolvePublicNodeRejectsProtectedAddress(t *testing.T) {
	t.Parallel()
	node := subscription.Node{ID: "blocked", Name: "Blocked", Server: "127.0.0.1", Port: 80}
	_, result, ok := resolvePublicNode(context.Background(), node)
	if ok || result.Status != StatusFailed {
		t.Fatalf("resolvePublicNode() = ok %v, result %+v", ok, result)
	}
}

func TestPinNodeAddressPreservesWebSocketRoutingName(t *testing.T) {
	t.Parallel()
	node := subscription.Node{
		Server: "edge.example.com", TLS: subscription.TLS{ServerName: "origin.example.com"},
		Transport: subscription.Transport{Type: "ws"},
	}
	pinned := pinNodeAddress(node, netip.MustParseAddr("203.0.113.10"))
	if pinned.Server != "203.0.113.10" || pinned.Transport.Headers["Host"] != "origin.example.com" {
		t.Fatalf("pinNodeAddress() = %+v", pinned)
	}
}

func TestRequestProxyDelayMeasuresSuccessfulRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Host != "target.example" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	node := subscription.Node{ID: "node", Name: "Node"}
	result := requestProxyDelay(context.Background(), server.Listener.Addr().String(), "http://target.example/generate_204", node)
	if result.Status != StatusOK || result.LatencyMS == nil || *result.LatencyMS < 1 {
		t.Fatalf("requestProxyDelay() = %+v", result)
	}
}

func TestEnsurePrivateWorkDirectoryRejectsSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateWorkDirectory(link); err == nil {
		t.Fatal("symlink work directory was accepted")
	}
}
