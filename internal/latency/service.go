package latency

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"sing-box-webui/internal/netsafety"
	"sing-box-webui/internal/singbox"
	"sing-box-webui/internal/subscription"
)

const (
	MaxTargets          = 128
	MaxConcurrentTests  = 4
	resolveConcurrency  = 8
	requestConcurrency  = 16
	resolveTimeout      = 3 * time.Second
	probeRequestTimeout = 6 * time.Second
	startupTimeout      = 4 * time.Second
	testURL             = "https://cp.cloudflare.com/generate_204"
)

var (
	ErrUnavailable = errors.New("real latency testing requires a configured sing-box binary")
	ErrBusy        = errors.New("a latency test is already running")
)

type Status string

const (
	StatusOK      Status = "ok"
	StatusTimeout Status = "timeout"
	StatusFailed  Status = "failed"
)

type Result struct {
	NodeID    string `json:"nodeId"`
	Name      string `json:"name"`
	Status    Status `json:"status"`
	LatencyMS *int64 `json:"latencyMs,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type Response struct {
	Items []Result `json:"items"`
}

type NodeSource interface {
	ProbeNodes(subscriptionID string, nodeIDs []string) ([]subscription.Node, error)
}

type Runner interface {
	Test(context.Context, []subscription.Node) ([]Result, error)
}

type Service struct {
	source    NodeSource
	runner    Runner
	slotsOnce sync.Once
	slots     chan struct{}
}

func NewService(source NodeSource, client *singbox.Client, workDirectory string) *Service {
	service := &Service{source: source}
	if client != nil {
		service.runner = &singBoxRunner{client: client, workDirectory: workDirectory}
	}
	return service
}

func (s *Service) Test(ctx context.Context, subscriptionID string, nodeIDs []string) (Response, error) {
	if s == nil || s.source == nil || s.runner == nil {
		return Response{}, ErrUnavailable
	}
	s.slotsOnce.Do(func() { s.slots = make(chan struct{}, MaxConcurrentTests) })
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Response{}, ErrBusy
	}

	nodes, err := s.source.ProbeNodes(subscriptionID, nodeIDs)
	if err != nil {
		return Response{}, err
	}
	if len(nodes) > MaxTargets {
		return Response{}, fmt.Errorf("a maximum of %d nodes can be tested at once", MaxTargets)
	}
	results, err := s.runner.Test(ctx, nodes)
	if err != nil {
		return Response{}, err
	}
	return Response{Items: results}, nil
}

type singBoxRunner struct {
	client        *singbox.Client
	workDirectory string
}

func (r *singBoxRunner) Test(ctx context.Context, nodes []subscription.Node) ([]Result, error) {
	results, prepared, indexes := prepareNodes(ctx, nodes)
	if len(prepared) == 0 {
		return results, nil
	}

	listeners, proxyAddresses, err := reserveProxyAddresses(len(prepared))
	if err != nil {
		return nil, err
	}
	closeListeners := func() {
		for _, listener := range listeners {
			if listener != nil {
				_ = listener.Close()
			}
		}
	}
	content, _, err := singbox.BuildLatencyConfig(prepared, proxyAddresses)
	if err != nil {
		closeListeners()
		return nil, err
	}

	workspace, configPath, err := r.writeConfig(content)
	if err != nil {
		closeListeners()
		return nil, err
	}
	defer os.RemoveAll(workspace)
	checkCtx, cancelCheck := context.WithTimeout(ctx, 8*time.Second)
	err = r.client.Check(checkCtx, configPath)
	cancelCheck()
	if err != nil {
		closeListeners()
		return nil, fmt.Errorf("validate temporary latency config: %w", err)
	}
	closeListeners()

	command := exec.Command(r.client.BinaryPath(), "run", "-c", configPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start temporary sing-box latency process: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	defer stopProcessGroup(command, done)

	if err := waitForProxy(ctx, done, proxyAddresses[0]); err != nil {
		return nil, err
	}

	semaphore := make(chan struct{}, requestConcurrency)
	var wait sync.WaitGroup
	for probeIndex := range prepared {
		probeIndex := probeIndex
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[indexes[probeIndex]] = timeoutResult(prepared[probeIndex])
				return
			}
			results[indexes[probeIndex]] = requestProxyDelay(ctx, proxyAddresses[probeIndex], testURL, prepared[probeIndex])
		}()
	}
	wait.Wait()
	return results, nil
}

func prepareNodes(ctx context.Context, nodes []subscription.Node) ([]Result, []subscription.Node, []int) {
	results := make([]Result, len(nodes))
	resolved := make([]subscription.Node, len(nodes))
	valid := make([]bool, len(nodes))
	semaphore := make(chan struct{}, resolveConcurrency)
	var wait sync.WaitGroup
	for index, node := range nodes {
		index, node := index, node
		results[index] = Result{NodeID: node.ID, Name: node.Name, Status: StatusFailed, Detail: "节点地址不可测试"}
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = timeoutResult(node)
				return
			}
			resolvedNode, result, ok := resolvePublicNode(ctx, node)
			resolved[index], results[index], valid[index] = resolvedNode, result, ok
		}()
	}
	wait.Wait()

	prepared := make([]subscription.Node, 0, len(nodes))
	indexes := make([]int, 0, len(nodes))
	for index := range nodes {
		if valid[index] {
			prepared = append(prepared, resolved[index])
			indexes = append(indexes, index)
		}
	}
	return results, prepared, indexes
}

func resolvePublicNode(parent context.Context, node subscription.Node) (subscription.Node, Result, bool) {
	ctx, cancel := context.WithTimeout(parent, resolveTimeout)
	defer cancel()
	addresses, err := resolveNodeAddresses(ctx, node.Server)
	if err != nil {
		if ctx.Err() != nil {
			return node, timeoutResult(node), false
		}
		return node, Result{NodeID: node.ID, Name: node.Name, Status: StatusFailed, Detail: "节点域名解析失败"}, false
	}
	for _, address := range addresses {
		if netsafety.AllowedPublicAddress(address) {
			return pinNodeAddress(node, address), Result{}, true
		}
	}
	return node, Result{NodeID: node.ID, Name: node.Name, Status: StatusFailed, Detail: "节点地址位于受保护的本机或私网网络"}, false
}

func pinNodeAddress(node subscription.Node, address netip.Addr) subscription.Node {
	if (node.Transport.Type == "ws" || node.Transport.Type == "http" || node.Transport.Type == "httpupgrade") && node.Transport.Headers["Host"] == "" {
		headers := make(map[string]string, len(node.Transport.Headers)+1)
		for key, value := range node.Transport.Headers {
			headers[key] = value
		}
		headers["Host"] = node.TLS.ServerName
		if headers["Host"] == "" {
			headers["Host"] = node.Server
		}
		node.Transport.Headers = headers
	}
	node.Server = address.Unmap().String()
	return node
}

func (r *singBoxRunner) writeConfig(content []byte) (string, string, error) {
	if err := ensurePrivateWorkDirectory(r.workDirectory); err != nil {
		return "", "", err
	}
	workspace, err := os.MkdirTemp(r.workDirectory, ".probe-*")
	if err != nil {
		return "", "", fmt.Errorf("create latency workspace: %w", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return "", "", err
	}
	configPath := filepath.Join(workspace, "config.json")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		_ = os.RemoveAll(workspace)
		return "", "", fmt.Errorf("write latency config: %w", err)
	}
	return workspace, configPath, nil
}

func ensurePrivateWorkDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("latency work path must be a real directory")
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure latency work directory: %w", err)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect latency work directory: %w", err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create latency work directory: %w", err)
	}
	return os.Chmod(directory, 0o700)
}

func reserveProxyAddresses(count int) ([]net.Listener, []string, error) {
	listeners := make([]net.Listener, 0, count)
	addresses := make([]string, 0, count)
	for range count {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			for _, reserved := range listeners {
				_ = reserved.Close()
			}
			return nil, nil, fmt.Errorf("reserve latency proxy: %w", err)
		}
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	return listeners, addresses, nil
}

func waitForProxy(ctx context.Context, done <-chan struct{}, address string) error {
	readyCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(readyCtx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("start temporary sing-box latency proxy: %w", readyCtx.Err())
		case <-done:
			return fmt.Errorf("temporary sing-box latency process exited during startup")
		case <-ticker.C:
		}
	}
}

func requestProxyDelay(parent context.Context, proxyAddress, targetURL string, node subscription.Node) Result {
	ctx, cancel := context.WithTimeout(parent, probeRequestTimeout)
	defer cancel()
	proxyURL := &url.URL{Scheme: "http", Host: proxyAddress}
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		DialContext:         (&net.Dialer{Timeout: probeRequestTimeout}).DialContext,
		TLSHandshakeTimeout: probeRequestTimeout,
	}
	client := &http.Client{Transport: transport, Timeout: probeRequestTimeout}
	defer client.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	startedAt := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return timeoutResult(node)
		}
		detail := "真实代理链路测试失败"
		if strings.Contains(err.Error(), "connection reset by peer") {
			detail = "节点拒绝代理握手"
		}
		return Result{NodeID: node.ID, Name: node.Name, Status: StatusFailed, Detail: detail}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{NodeID: node.ID, Name: node.Name, Status: StatusFailed, Detail: "代理握手或目标访问失败"}
	}
	delay := time.Since(startedAt).Milliseconds()
	if delay < 1 {
		delay = 1
	}
	return Result{NodeID: node.ID, Name: node.Name, Status: StatusOK, LatencyMS: &delay}
}

func timeoutResult(node subscription.Node) Result {
	return Result{NodeID: node.ID, Name: node.Name, Status: StatusTimeout, Detail: "真实代理链路测试超时"}
}

func stopProcessGroup(command *exec.Cmd, done <-chan struct{}) {
	if command == nil || command.Process == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-done
	}
}
