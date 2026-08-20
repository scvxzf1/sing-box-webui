package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"sing-box-webui/internal/configstore"
	"sing-box-webui/internal/connmon"
	"sing-box-webui/internal/dnsprofile"
	"sing-box-webui/internal/events"
	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/platform/systemproxy"
	"sing-box-webui/internal/poolhealth"
	"sing-box-webui/internal/routing"
	"sing-box-webui/internal/singbox"
	"sing-box-webui/internal/subscription"
	"sing-box-webui/internal/supervisor"
)

type Capability struct {
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
}

type Capabilities struct {
	SingBox     Capability `json:"singBox"`
	SystemProxy Capability `json:"systemProxy"`
	TUN         Capability `json:"tun"`
}

type Runtime struct {
	State          supervisor.State     `json:"state"`
	Mode           singbox.ProxyMode    `json:"mode,omitempty"`
	TargetType     string               `json:"targetType,omitempty"`
	SubscriptionID string               `json:"subscriptionId,omitempty"`
	NodeID         string               `json:"nodeId,omitempty"`
	NodeName       string               `json:"nodeName,omitempty"`
	PoolID         string               `json:"poolId,omitempty"`
	PoolName       string               `json:"poolName,omitempty"`
	AllowLan       bool                 `json:"allowLan,omitempty"`
	StartedAt      time.Time            `json:"startedAt,omitempty"`
	LastError      string               `json:"lastError,omitempty"`
	Capabilities   Capabilities         `json:"capabilities"`
	PoolHealth     *poolhealth.Snapshot `json:"poolHealth,omitempty"`
}

type ApplyInput struct {
	SubscriptionID string            `json:"subscriptionId,omitempty"`
	NodeID         string            `json:"nodeId,omitempty"`
	PoolID         string            `json:"poolId,omitempty"`
	Mode           singbox.ProxyMode `json:"mode"`
	AllowLan       bool              `json:"allowLan"`
}

type Service struct {
	mu                       sync.RWMutex
	operationMu              sync.Mutex
	subscriptions            *subscription.Manager
	pools                    *nodepool.Manager
	rules                    *routing.Manager
	dns                      *dnsprofile.Manager
	client                   *singbox.Client
	configStore              *configstore.Store
	supervisor               *supervisor.Manager
	systemProxy              systemproxy.Controller
	events                   *events.Broker
	tunEnabled               bool
	mixedPort                uint16
	runtime                  Runtime
	health                   *poolhealth.Manager
	connmon                  *connmon.Monitor
	controlledStopGeneration uint64
}

type Config struct {
	Subscriptions *subscription.Manager
	Pools         *nodepool.Manager
	Rules         *routing.Manager
	DNS           *dnsprofile.Manager
	Client        *singbox.Client
	ConfigStore   *configstore.Store
	Supervisor    *supervisor.Manager
	SystemProxy   systemproxy.Controller
	Events        *events.Broker
	TUNEnabled    bool
	MixedPort     uint16
}

func New(config Config) *Service {
	return &Service{
		subscriptions: config.Subscriptions,
		pools:         config.Pools,
		rules:         config.Rules,
		dns:           config.DNS,
		client:        config.Client,
		configStore:   config.ConfigStore,
		supervisor:    config.Supervisor,
		systemProxy:   config.SystemProxy,
		events:        config.Events,
		tunEnabled:    config.TUNEnabled,
		mixedPort:     config.MixedPort,
		runtime:       Runtime{State: supervisor.StateStopped},
		health:        poolhealth.NewManager(),
		connmon:       connmon.New(nil),
	}
}

func (s *Service) Status(ctx context.Context) Runtime {
	s.mu.RLock()
	runtime := s.runtime
	s.mu.RUnlock()
	if s.supervisor != nil {
		snapshot := s.supervisor.Snapshot()
		if runtime.State != supervisor.StateFailed {
			runtime.State = snapshot.State
		}
		if snapshot.LastError != "" {
			runtime.LastError = snapshot.LastError
		}
	}
	if runtime.TargetType == "pool" && s.health != nil {
		health := s.health.Snapshot()
		runtime.PoolHealth = &health
	}
	runtime.Capabilities = s.capabilities(ctx)
	return runtime
}

func (s *Service) Apply(ctx context.Context, input ApplyInput) (Runtime, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if input.Mode != singbox.ModeSystemProxy && input.Mode != singbox.ModeTUN {
		return s.Status(ctx), fmt.Errorf("unsupported proxy mode")
	}
	if s.client == nil || s.configStore == nil || s.supervisor == nil {
		return s.Status(ctx), fmt.Errorf("sing-box binary is not configured")
	}
	if input.Mode == singbox.ModeTUN && !s.tunEnabled {
		return s.Status(ctx), fmt.Errorf("TUN mode is disabled until the sing-box binary has the required Linux capabilities")
	}
	if input.Mode == singbox.ModeSystemProxy {
		available, detail := s.systemProxy.Available(ctx)
		if !available {
			return s.Status(ctx), fmt.Errorf("%s", detail)
		}
	}
	routeRules := []map[string]any(nil)
	if s.rules != nil {
		var err error
		routeRules, err = s.rules.Compiled()
		if err != nil {
			return s.Status(ctx), err
		}
	}
	dnsProfile := dnsprofile.DefaultProfile()
	if s.dns != nil {
		dnsProfile = s.dns.Get()
	}

	var content []byte
	var target Runtime
	var healthConfig *poolhealth.Config
	var connResolver connmon.Resolver
	var controllerAddress, controllerSecret string
	var err error
	if input.PoolID != "" {
		if input.SubscriptionID != "" || input.NodeID != "" {
			return s.Status(ctx), fmt.Errorf("poolId cannot be combined with subscriptionId or nodeId")
		}
		if s.pools == nil {
			return s.Status(ctx), fmt.Errorf("node pool control is unavailable")
		}
		if validateErr := s.pools.ValidateProbeURLs(ctx, input.PoolID); validateErr != nil {
			return s.Status(ctx), validateErr
		}
		pool, members, nodes, resolveErr := s.pools.ResolveWithMembers(input.PoolID)
		if resolveErr != nil {
			return s.Status(ctx), resolveErr
		}
		addr, secret, controllerErr := reserveHealthController()
		if controllerErr != nil {
			return s.Status(ctx), controllerErr
		}
		controllerAddress, controllerSecret = addr, secret
		content, err = singbox.BuildPoolConfigWithDNS(nodes, input.Mode, s.mixedPort, singbox.URLTestOptions{
			URL: pool.ProbeURL, Interval: time.Duration(pool.ProbeIntervalSeconds) * time.Second,
			Tolerance: pool.ToleranceMS, IdleTimeout: time.Duration(pool.IdleTimeoutSeconds) * time.Second,
			InterruptExistingConnections: pool.InterruptExistingConnections,
			ControllerAddress:            controllerAddress, ControllerSecret: controllerSecret,
		}, routeRules, dnsProfile, input.AllowLan)
		probeURLs := append([]string{pool.ProbeURL}, pool.FallbackProbeURLs...)
		targets := make([]poolhealth.Target, len(nodes))
		namesByTag := make(map[string]string, len(nodes))
		for index, node := range nodes {
			tag := singbox.PoolMemberTag(index)
			targets[index] = poolhealth.Target{
				Tag: tag, SubscriptionID: members[index].SubscriptionID,
				NodeID: members[index].NodeID, Name: node.Name,
			}
			namesByTag[tag] = node.Name
		}
		connResolver = poolChainResolver(namesByTag)
		healthConfig = &poolhealth.Config{
			Address: controllerAddress, Secret: controllerSecret, ProbeURLs: probeURLs,
			Interval:             time.Duration(pool.ProbeIntervalSeconds) * time.Second,
			Tolerance:            time.Duration(pool.ToleranceMS) * time.Millisecond,
			IdleTimeout:          time.Duration(pool.IdleTimeoutSeconds) * time.Second,
			HighLatencyThreshold: time.Duration(pool.HighLatencyThresholdMS) * time.Millisecond,
			ConsecutiveFailures:  pool.ConsecutiveFailures, RecoverySuccesses: pool.RecoverySuccesses,
			MaxBackoff: time.Duration(pool.MaxBackoffSeconds) * time.Second, Targets: targets,
		}
		target = Runtime{TargetType: "pool", PoolID: pool.ID, PoolName: pool.Name}
	} else {
		if input.SubscriptionID == "" || input.NodeID == "" {
			return s.Status(ctx), fmt.Errorf("subscriptionId and nodeId are required for a node target")
		}
		subscriptionValue, node, resolveErr := s.subscriptions.SelectedNode(input.SubscriptionID, input.NodeID)
		if resolveErr != nil {
			return s.Status(ctx), resolveErr
		}
		if _, err := s.subscriptions.Activate(subscriptionValue.ID); err != nil {
			return s.Status(ctx), err
		}
		if _, err := s.subscriptions.SelectNode(subscriptionValue.ID, node.ID); err != nil {
			return s.Status(ctx), err
		}
		addr, secret, controllerErr := reserveHealthController()
		if controllerErr != nil {
			return s.Status(ctx), controllerErr
		}
		controllerAddress, controllerSecret = addr, secret
		content, err = singbox.BuildConfigWithController(node, input.Mode, s.mixedPort, routeRules, singbox.ControllerOptions{
			Address: controllerAddress, Secret: controllerSecret,
		}, dnsProfile, input.AllowLan)
		connResolver = nodeChainResolver(node.Name)
		target = Runtime{TargetType: "node", SubscriptionID: subscriptionValue.ID, NodeID: node.ID, NodeName: node.Name}
	}
	if err != nil {
		return s.Status(ctx), err
	}
	expectedVersion := ""
	if current, readErr := s.configStore.Read(); readErr == nil {
		expectedVersion = current.Version
	} else if !errors.Is(readErr, configstore.ErrNotFound) {
		return s.Status(ctx), readErr
	}
	if _, err := s.configStore.Save(ctx, content, expectedVersion); err != nil {
		return s.Status(ctx), err
	}
	if s.health != nil {
		s.health.Stop()
	}
	if s.connmon != nil {
		s.connmon.Stop()
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if s.supervisor.Snapshot().State == supervisor.StateRunning {
		s.markControlledStop()
		_, err = s.supervisor.Stop(stopCtx)
	}
	cancel()
	if err != nil {
		return s.recordFailure(ctx, err), err
	}
	if input.Mode != singbox.ModeSystemProxy {
		if s.systemProxy != nil {
			if err := s.systemProxy.Restore(ctx); err != nil {
				return s.recordFailure(ctx, err), err
			}
		}
	}
	started, startErr := s.supervisor.Start(ctx, s.configStore.Path())
	if startErr != nil {
		return s.recordFailure(ctx, startErr), startErr
	}
	if input.Mode == singbox.ModeSystemProxy {
		proxyHost := "127.0.0.1"
		if input.AllowLan {
			proxyHost = "0.0.0.0"
		}
		if err := s.systemProxy.Apply(ctx, proxyHost, s.mixedPort); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.markControlledStop()
			_, _ = s.supervisor.Stop(stopCtx)
			cancel()
			return s.recordFailure(ctx, err), err
		}
	}
	if healthConfig != nil && s.health != nil {
		if err := s.health.Start(*healthConfig); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.markControlledStop()
			_, _ = s.supervisor.Stop(stopCtx)
			cancel()
			return s.recordFailure(ctx, err), err
		}
	}
	if s.connmon != nil && controllerAddress != "" {
		s.connmon.Start(controllerAddress, controllerSecret, connResolver)
	}

	s.mu.Lock()
	s.runtime = Runtime{
		State: supervisor.StateRunning, Mode: input.Mode, TargetType: target.TargetType,
		SubscriptionID: target.SubscriptionID, NodeID: target.NodeID, NodeName: target.NodeName,
		PoolID: target.PoolID, PoolName: target.PoolName, AllowLan: input.AllowLan, StartedAt: time.Now().UTC(),
	}
	s.mu.Unlock()
	go s.watchSupervisor(started.Generation)
	s.publish("runtime.applied", map[string]string{"mode": string(input.Mode), "targetType": target.TargetType, "targetId": firstTargetID(target)})
	return s.Status(ctx), nil
}

func (s *Service) ReapplyRules(ctx context.Context) (Runtime, error) {
	runtime := s.Status(ctx)
	if runtime.State != supervisor.StateRunning {
		return runtime, nil
	}
	input := ApplyInput{Mode: runtime.Mode, AllowLan: runtime.AllowLan}
	if runtime.TargetType == "pool" {
		input.PoolID = runtime.PoolID
	} else {
		input.SubscriptionID = runtime.SubscriptionID
		input.NodeID = runtime.NodeID
	}
	return s.Apply(ctx, input)
}

func (s *Service) Stop(ctx context.Context) (Runtime, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if s.health != nil {
		s.health.Stop()
	}
	if s.connmon != nil {
		s.connmon.Stop()
	}
	var restoreErr error
	if s.systemProxy != nil {
		restoreErr = s.systemProxy.Restore(ctx)
	}
	var stopErr error
	if s.supervisor != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		s.markControlledStop()
		_, stopErr = s.supervisor.Stop(stopCtx)
		cancel()
	}
	if err := errors.Join(restoreErr, stopErr); err != nil {
		return s.recordFailure(ctx, err), err
	}
	s.mu.Lock()
	s.runtime = Runtime{State: supervisor.StateStopped}
	s.mu.Unlock()
	s.publish("runtime.stopped", map[string]string{"state": "stopped"})
	return s.Status(ctx), nil
}

func (s *Service) watchSupervisor(generation uint64) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		snapshot := s.supervisor.Snapshot()
		if snapshot.Generation != generation {
			return
		}
		if snapshot.State == supervisor.StateRunning || snapshot.State == supervisor.StateStarting {
			continue
		}
		s.operationMu.Lock()
		snapshot = s.supervisor.Snapshot()
		s.mu.RLock()
		alreadyStopped := s.runtime.State == supervisor.StateStopped
		controlledStop := s.controlledStopGeneration == generation
		s.mu.RUnlock()
		if snapshot.Generation != generation || alreadyStopped || controlledStop {
			s.operationMu.Unlock()
			return
		}
		if s.health != nil {
			s.health.Stop()
		}
		if s.connmon != nil {
			s.connmon.Stop()
		}
		lastError := snapshot.LastError
		if lastError == "" {
			lastError = "sing-box stopped unexpectedly"
		}
		if s.systemProxy != nil {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.systemProxy.Restore(restoreCtx); err != nil {
				lastError = errors.Join(errors.New(lastError), fmt.Errorf("restore system proxy: %w", err)).Error()
			}
			cancel()
		}
		s.mu.Lock()
		s.runtime.State = supervisor.StateFailed
		s.runtime.LastError = lastError
		s.mu.Unlock()
		s.publish("runtime.failed", map[string]string{"state": "failed"})
		s.operationMu.Unlock()
		return
	}
}

func (s *Service) markControlledStop() {
	if s.supervisor == nil {
		return
	}
	generation := s.supervisor.Snapshot().Generation
	s.mu.Lock()
	s.controlledStopGeneration = generation
	s.mu.Unlock()
}

// Links returns the live connection monitor so the API layer can query it.
func (s *Service) Links() *connmon.Monitor {
	return s.connmon
}

// ProxyAddress reports the loopback mixed-inbound address (host:port) that
// connectivity checks can route through, or an empty string when the proxy is
// not listening on a local port (stopped, or running in TUN mode where the
// tunnel already intercepts all traffic system-wide).
func (s *Service) ProxyAddress() string {
	s.mu.RLock()
	running := s.runtime.State == supervisor.StateRunning
	mode := s.runtime.Mode
	s.mu.RUnlock()
	if s.supervisor != nil && s.supervisor.Snapshot().State != supervisor.StateRunning {
		running = false
	}
	if !running || mode != singbox.ModeSystemProxy || s.mixedPort == 0 {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", s.mixedPort))
}

// ProxyRunning reports whether diagnostics can be attributed to the current
// runtime. System-proxy mode uses ProxyAddress; TUN mode intercepts the direct
// diagnostic request system-wide.
func (s *Service) ProxyRunning() bool {
	s.mu.RLock()
	running := s.runtime.State == supervisor.StateRunning
	s.mu.RUnlock()
	return running && (s.supervisor == nil || s.supervisor.Snapshot().State == supervisor.StateRunning)
}

// poolChainResolver resolves an expanded connection chain to the pool member
// that carried it. The monitor expands group tags (auto/proxy) to the selected
// member tag, so the first member tag present wins.
func poolChainResolver(namesByTag map[string]string) connmon.Resolver {
	return func(chains []string) string {
		for _, chain := range chains {
			if name, ok := namesByTag[chain]; ok {
				return name
			}
		}
		return ""
	}
}

// nodeChainResolver maps every proxied connection to the single selected node.
// Direct and blocked outbounds are surfaced separately for clarity.
func nodeChainResolver(nodeName string) connmon.Resolver {
	return func(chains []string) string {
		for _, chain := range chains {
			switch chain {
			case "direct":
				return "direct"
			case "block":
				return "block"
			case "proxy":
				return nodeName
			}
		}
		return ""
	}
}

func reserveHealthController() (string, string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("reserve health controller: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", "", fmt.Errorf("release health controller reservation: %w", err)
	}
	var secretBytes [24]byte
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return "", "", fmt.Errorf("generate health controller secret: %w", err)
	}
	return address, hex.EncodeToString(secretBytes[:]), nil
}

func firstTargetID(runtime Runtime) string {
	if runtime.PoolID != "" {
		return runtime.PoolID
	}
	return runtime.NodeID
}

func (s *Service) capabilities(ctx context.Context) Capabilities {
	capabilities := Capabilities{
		SingBox: Capability{Available: s.client != nil, Detail: "sing-box 核心不可用"},
		TUN:     Capability{Available: s.client != nil && s.tunEnabled, Detail: "设置 SING_BOX_WEBUI_ENABLE_TUN=1 后启用"},
	}
	if capabilities.SingBox.Available {
		capabilities.SingBox.Detail = "sing-box 可执行文件可用"
	}
	if capabilities.TUN.Available {
		capabilities.TUN.Detail = "TUN 已显式启用；运行仍取决于 sing-box 文件能力"
	}
	if s.systemProxy != nil {
		capabilities.SystemProxy.Available, capabilities.SystemProxy.Detail = s.systemProxy.Available(ctx)
	} else {
		capabilities.SystemProxy.Detail = "系统代理适配器不可用"
	}
	return capabilities
}

func (s *Service) recordFailure(ctx context.Context, err error) Runtime {
	s.mu.Lock()
	s.runtime.State = supervisor.StateFailed
	s.runtime.LastError = err.Error()
	s.mu.Unlock()
	s.publish("runtime.failed", map[string]string{"error": err.Error()})
	return s.Status(ctx)
}

func (s *Service) publish(eventType string, payload any) {
	if s.events != nil {
		_, _ = s.events.Publish(eventType, payload)
	}
}
