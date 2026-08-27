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
	"sing-box-webui/internal/proxychain"
	"sing-box-webui/internal/proxychannel"
	"sing-box-webui/internal/routing"
	"sing-box-webui/internal/singbox"
	"sing-box-webui/internal/subscription"
	"sing-box-webui/internal/supervisor"
)

var ErrRuntimeBusy = errors.New("runtime is busy")

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
	ChainID        string               `json:"chainId,omitempty"`
	ChainName      string               `json:"chainName,omitempty"`
	ChainEntryType proxychain.EntryType `json:"chainEntryType,omitempty"`
	ChainEntryName string               `json:"chainEntryName,omitempty"`
	ChainExitName  string               `json:"chainExitName,omitempty"`
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
	ChainID        string            `json:"chainId,omitempty"`
	Direct         bool              `json:"direct,omitempty"`
	Mode           singbox.ProxyMode `json:"mode"`
	AllowLan       bool              `json:"allowLan"`
}

type HealthProbeSource interface {
	HealthProbeURLs() []string
}

type Service struct {
	mu                       sync.RWMutex
	operationMu              sync.Mutex
	preferencesPath          string
	subscriptions            *subscription.Manager
	pools                    *nodepool.Manager
	chains                   *proxychain.Manager
	channels                 *proxychannel.Manager
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
	healthProbeSource        HealthProbeSource
	connmon                  *connmon.Monitor
	runtimeCatalog           map[string]runtimeCatalogTarget
	controllerAddress        string
	controllerSecret         string
	controlledStopGeneration uint64
	operationCancel          context.CancelFunc
}

type runtimeCatalogTarget struct {
	Tag       string
	Signature string
	Runtime   Runtime
	Health    *poolhealth.Config
}

type runtimeCatalogBuild struct {
	Content           []byte
	Targets           map[string]runtimeCatalogTarget
	Selected          runtimeCatalogTarget
	ControllerAddress string
	ControllerSecret  string
	Resolver          connmon.Resolver
}

type Config struct {
	Subscriptions   *subscription.Manager
	Pools           *nodepool.Manager
	Chains          *proxychain.Manager
	Channels        *proxychannel.Manager
	Rules           *routing.Manager
	DNS             *dnsprofile.Manager
	Client          *singbox.Client
	ConfigStore     *configstore.Store
	Supervisor      *supervisor.Manager
	SystemProxy     systemproxy.Controller
	Events          *events.Broker
	TUNEnabled      bool
	MixedPort       uint16
	PreferencesPath string
}

func New(config Config) (*Service, error) {
	preferences, err := loadRuntimePreferences(config.PreferencesPath)
	if err != nil {
		return nil, err
	}
	return &Service{
		subscriptions:   config.Subscriptions,
		pools:           config.Pools,
		chains:          config.Chains,
		channels:        config.Channels,
		rules:           config.Rules,
		dns:             config.DNS,
		client:          config.Client,
		configStore:     config.ConfigStore,
		supervisor:      config.Supervisor,
		systemProxy:     config.SystemProxy,
		events:          config.Events,
		tunEnabled:      config.TUNEnabled,
		mixedPort:       config.MixedPort,
		preferencesPath: config.PreferencesPath,
		runtime:         Runtime{State: supervisor.StateStopped, AllowLan: preferences.AllowLan},
		health:          poolhealth.NewManager(),
		connmon:         connmon.New(nil),
	}, nil
}

// SetHealthProbeSource connects the persisted quick-test targets after both
// managers have been constructed. It must be set before serving apply calls.
func (s *Service) SetHealthProbeSource(source HealthProbeSource) {
	s.mu.Lock()
	s.healthProbeSource = source
	s.mu.Unlock()
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
	if (runtime.TargetType == "pool" || (runtime.TargetType == "chain" && runtime.ChainEntryType == proxychain.EntryPool)) && s.health != nil {
		health := s.health.Snapshot()
		runtime.PoolHealth = &health
	}
	runtime.Capabilities = s.capabilities(ctx)
	return runtime
}

func (s *Service) Apply(ctx context.Context, input ApplyInput) (Runtime, error) {
	return s.runApplyOperation(ctx, func(operationCtx context.Context) (Runtime, error) {
		if s.canAttemptHotSwitch(input) {
			if catalogTarget, available := s.hotSwitchTarget(operationCtx, input); available {
				return s.hotSwitch(operationCtx, catalogTarget)
			}
		}
		return s.applyFull(operationCtx, input)
	})
}

func (s *Service) hotSwitchTarget(ctx context.Context, input ApplyInput) (runtimeCatalogTarget, bool) {
	// The running catalog was DNS-validated when it was built. A hot switch
	// only needs to confirm that the persisted target still has the same
	// signature; repeating external DNS resolution here makes an otherwise
	// local selector change depend on the currently selected proxy path.
	key, _, signature, err := s.resolveRequestedTarget(ctx, input, false)
	if err != nil {
		return runtimeCatalogTarget{}, false
	}
	s.mu.RLock()
	target, available := s.runtimeCatalog[key]
	s.mu.RUnlock()
	return target, available && target.Signature == signature
}

func (s *Service) SetAllowLan(ctx context.Context, allowLan bool) (Runtime, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	s.mu.RLock()
	running := s.runtime.State == supervisor.StateRunning || s.runtime.State == supervisor.StateStarting
	s.mu.RUnlock()
	if running {
		return s.Status(ctx), ErrRuntimeBusy
	}
	if err := saveRuntimePreferences(s.preferencesPath, runtimePreferences{AllowLan: allowLan}); err != nil {
		return s.Status(ctx), err
	}
	s.mu.Lock()
	s.runtime.AllowLan = allowLan
	s.mu.Unlock()
	return s.Status(ctx), nil
}

func (s *Service) runApplyOperation(ctx context.Context, operation func(context.Context) (Runtime, error)) (Runtime, error) {
	s.operationMu.Lock()
	operationCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.operationCancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.operationCancel = nil
		s.mu.Unlock()
		s.operationMu.Unlock()
	}()
	return operation(operationCtx)
}

func (s *Service) applyFull(ctx context.Context, input ApplyInput) (Runtime, error) {
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
	if err := saveRuntimePreferences(s.preferencesPath, runtimePreferences{AllowLan: input.AllowLan}); err != nil {
		return s.Status(ctx), err
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

	build, err := s.buildRuntimeCatalog(ctx, input, routeRules, dnsProfile)
	if err != nil {
		return s.Status(ctx), err
	}
	content := build.Content
	target := build.Selected.Runtime
	healthConfig := build.Selected.Health
	controllerAddress, controllerSecret := build.ControllerAddress, build.ControllerSecret
	connResolver := build.Resolver
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
	supervisorState := s.supervisor.Snapshot().State
	if supervisorState == supervisor.StateRunning || supervisorState == supervisor.StateStarting {
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
	if healthConfig != nil && s.health != nil {
		if err := s.health.StartContext(ctx, *healthConfig); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.markControlledStop()
			_, _ = s.supervisor.Stop(stopCtx)
			cancel()
			return s.recordFailure(ctx, err), err
		}
	}
	if input.Mode == singbox.ModeSystemProxy {
		proxyHost := "127.0.0.1"
		if input.AllowLan {
			proxyHost = "0.0.0.0"
		}
		if err := s.systemProxy.Apply(ctx, proxyHost, s.mixedPort); err != nil {
			if s.health != nil {
				s.health.Stop()
			}
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
		PoolID: target.PoolID, PoolName: target.PoolName,
		ChainID: target.ChainID, ChainName: target.ChainName, ChainEntryType: target.ChainEntryType,
		ChainEntryName: target.ChainEntryName, ChainExitName: target.ChainExitName,
		AllowLan: input.AllowLan, StartedAt: time.Now().UTC(),
	}
	s.runtimeCatalog = build.Targets
	s.controllerAddress = build.ControllerAddress
	s.controllerSecret = build.ControllerSecret
	s.mu.Unlock()
	go s.watchSupervisor(started.Generation)
	s.publish("runtime.applied", map[string]string{"mode": string(input.Mode), "targetType": target.TargetType, "targetId": firstTargetID(target)})
	return s.Status(ctx), nil
}

func (s *Service) canAttemptHotSwitch(input ApplyInput) bool {
	s.mu.RLock()
	runtime := s.runtime
	hasController := s.controllerAddress != "" && s.controllerSecret != "" && len(s.runtimeCatalog) > 0
	s.mu.RUnlock()
	if !hasController || runtime.State != supervisor.StateRunning || runtime.Mode != input.Mode || runtime.AllowLan != input.AllowLan {
		return false
	}
	return s.supervisor != nil && s.supervisor.Snapshot().State == supervisor.StateRunning
}

func (s *Service) hotSwitch(ctx context.Context, target runtimeCatalogTarget) (Runtime, error) {
	s.mu.RLock()
	current := s.runtime
	address, secret := s.controllerAddress, s.controllerSecret
	previous, hasPrevious := s.runtimeCatalog[currentTargetKey(current)]
	s.mu.RUnlock()
	if currentTargetKey(current) == currentTargetKey(target.Runtime) {
		return s.Status(ctx), nil
	}

	if target.Health != nil && s.health != nil {
		if err := s.health.StartContext(ctx, *target.Health); err != nil {
			s.restoreCatalogHealthAfterFailure(ctx, previous, hasPrevious)
			return s.Status(ctx), err
		}
	}
	if err := poolhealth.SelectOutbound(ctx, address, secret, "proxy", target.Tag); err != nil {
		if target.Health != nil && s.health != nil {
			s.restoreCatalogHealthAfterFailure(ctx, previous, hasPrevious)
		}
		return s.Status(ctx), fmt.Errorf("hot switch runtime target: %w", err)
	}
	if target.Health == nil && s.health != nil {
		s.health.Stop()
	}

	s.mu.Lock()
	startedAt := s.runtime.StartedAt
	s.runtime = target.Runtime
	s.runtime.State = supervisor.StateRunning
	s.runtime.Mode = current.Mode
	s.runtime.AllowLan = current.AllowLan
	s.runtime.StartedAt = startedAt
	s.mu.Unlock()
	s.publish("runtime.switched", map[string]string{"targetType": target.Runtime.TargetType, "targetId": firstTargetID(target.Runtime)})
	return s.Status(ctx), nil
}

func (s *Service) restoreCatalogHealthAfterFailure(ctx context.Context, previous runtimeCatalogTarget, available bool) {
	if ctx.Err() != nil {
		s.health.Stop()
		return
	}
	s.restoreCatalogHealth(previous, available)
}

func (s *Service) restoreCatalogHealth(previous runtimeCatalogTarget, available bool) {
	if s.health == nil {
		return
	}
	if !available || previous.Health == nil {
		s.health.Stop()
		return
	}
	restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.health.StartContext(restoreCtx, *previous.Health)
}

func (s *Service) buildRuntimeCatalog(ctx context.Context, input ApplyInput, routeRules []map[string]any, dnsProfile dnsprofile.Profile) (runtimeCatalogBuild, error) {
	selectedKey, _, selectedSignature, err := s.resolveRequestedTarget(ctx, input, true)
	if err != nil {
		return runtimeCatalogBuild{}, err
	}
	controllerAddress, controllerSecret, err := reserveHealthController()
	if err != nil {
		return runtimeCatalogBuild{}, err
	}

	s.mu.RLock()
	probeSource := s.healthProbeSource
	s.mu.RUnlock()
	var probeURLs []string
	if probeSource != nil {
		probeURLs = probeSource.HealthProbeURLs()
	}

	nodeTargets := make([]singbox.RuntimeNodeTarget, 0)
	targets := map[string]runtimeCatalogTarget{
		directTargetKey(): {
			Tag: "direct", Signature: directTargetKey(),
			Runtime: Runtime{TargetType: "direct"},
		},
	}
	nodeTags := make(map[string]string)
	namesByTag := make(map[string]string)
	var subscriptionViews []subscription.View
	if s.subscriptions != nil {
		subscriptionViews = s.subscriptions.List()
	}
	for _, subscriptionView := range subscriptionViews {
		nodes, probeErr := s.subscriptions.ProbeNodes(subscriptionView.ID, nil)
		if probeErr != nil {
			continue
		}
		for _, node := range nodes {
			key := nodeTargetKey(subscriptionView.ID, node.ID)
			if validateErr := subscription.ValidateNode(node); validateErr != nil {
				if key == selectedKey {
					return runtimeCatalogBuild{}, validateErr
				}
				continue
			}
			tag := fmt.Sprintf("runtime-node-%03d", len(nodeTargets))
			nodeTargets = append(nodeTargets, singbox.RuntimeNodeTarget{Tag: tag, Node: node})
			nodeTags[key] = tag
			namesByTag[tag] = node.Name
			targets[key] = runtimeCatalogTarget{
				Tag: tag, Signature: nodeSignature(node),
				Runtime: Runtime{TargetType: "node", SubscriptionID: subscriptionView.ID, NodeID: node.ID, NodeName: node.Name},
			}
		}
	}
	if len(nodeTargets) == 0 && selectedKey != directTargetKey() {
		return runtimeCatalogBuild{}, fmt.Errorf("no valid runtime nodes are available")
	}

	poolTargets := make([]singbox.RuntimePoolTarget, 0)
	if s.pools != nil && len(probeURLs) > 0 {
		for _, poolView := range s.pools.List() {
			key := poolTargetKey(poolView.ID)
			if validateErr := s.pools.ValidateProbeURLs(ctx, poolView.ID); validateErr != nil {
				if key == selectedKey {
					return runtimeCatalogBuild{}, validateErr
				}
				continue
			}
			pool, members, nodes, resolveErr := s.pools.ResolveWithMembers(poolView.ID)
			if resolveErr != nil {
				if key == selectedKey {
					return runtimeCatalogBuild{}, resolveErr
				}
				continue
			}
			memberTags := make([]string, 0, len(members))
			healthTargets := make([]poolhealth.Target, 0, len(members))
			for index, member := range members {
				tag, available := nodeTags[nodeTargetKey(member.SubscriptionID, member.NodeID)]
				if !available {
					continue
				}
				memberTags = append(memberTags, tag)
				healthTargets = append(healthTargets, poolhealth.Target{
					Tag: tag, SubscriptionID: member.SubscriptionID, NodeID: member.NodeID, Name: nodes[index].Name,
				})
			}
			if len(memberTags) < 2 {
				if key == selectedKey {
					return runtimeCatalogBuild{}, fmt.Errorf("node pool requires at least 2 valid runtime members")
				}
				continue
			}
			poolTag := fmt.Sprintf("runtime-pool-%03d", len(poolTargets))
			autoTag := fmt.Sprintf("runtime-pool-auto-%03d", len(poolTargets))
			options := poolURLTestOptions(pool)
			poolTargets = append(poolTargets, singbox.RuntimePoolTarget{Tag: poolTag, AutoTag: autoTag, NodeTags: memberTags, Options: options})
			health := &poolhealth.Config{
				Address: controllerAddress, Secret: controllerSecret, SelectorTag: poolTag, ProbeURLs: append([]string(nil), probeURLs...),
				Interval: time.Duration(pool.ProbeIntervalSeconds) * time.Second, Tolerance: time.Duration(pool.ToleranceMS) * time.Millisecond,
				IdleTimeout: time.Duration(pool.IdleTimeoutSeconds) * time.Second, HighLatencyThreshold: time.Duration(pool.HighLatencyThresholdMS) * time.Millisecond,
				ConsecutiveFailures: pool.ConsecutiveFailures, RecoverySuccesses: pool.RecoverySuccesses,
				MaxBackoff: time.Duration(pool.MaxBackoffSeconds) * time.Second, Targets: healthTargets,
			}
			targets[key] = runtimeCatalogTarget{
				Tag: poolTag, Signature: poolSignature(pool, members), Health: health,
				Runtime: Runtime{TargetType: "pool", PoolID: pool.ID, PoolName: pool.Name},
			}
		}
	}

	chainTargets := make([]singbox.RuntimeChainTarget, 0)
	if s.chains != nil {
		for _, chainView := range s.chains.List() {
			key := chainTargetKey(chainView.ID)
			resolved, resolveErr := s.chains.Resolve(chainView.ID)
			if resolveErr != nil {
				if key == selectedKey {
					return runtimeCatalogBuild{}, resolveErr
				}
				continue
			}
			exitTag, exitAvailable := nodeTags[nodeTargetKey(resolved.Chain.ExitNode.SubscriptionID, resolved.Chain.ExitNode.NodeID)]
			if !exitAvailable {
				if key == selectedKey {
					return runtimeCatalogBuild{}, fmt.Errorf("proxy chain exit node is unavailable")
				}
				continue
			}
			chainIndex := len(chainTargets)
			chainTag := fmt.Sprintf("runtime-chain-%03d", chainIndex)
			chainTarget := singbox.RuntimeChainTarget{Tag: chainTag, ExitTag: exitTag}
			var health *poolhealth.Config
			if resolved.EntryNode != nil {
				chainTarget.Members = []singbox.RuntimeNodeTarget{{Tag: chainTag, Node: *resolved.EntryNode}}
				namesByTag[chainTag] = resolved.EntryNode.Name + " → " + resolved.ExitNode.Name
			} else {
				if len(probeURLs) == 0 {
					if key == selectedKey {
						return runtimeCatalogBuild{}, fmt.Errorf("at least one quick-test target is required for node-pool chain selection")
					}
					continue
				}
				if validateErr := s.pools.ValidateProbeURLs(ctx, resolved.Chain.EntryPoolID); validateErr != nil {
					if key == selectedKey {
						return runtimeCatalogBuild{}, validateErr
					}
					continue
				}
				chainTarget.AutoTag = fmt.Sprintf("runtime-chain-auto-%03d", chainIndex)
				options := poolURLTestOptions(*resolved.EntryPool)
				chainTarget.Options = &options
				healthTargets := make([]poolhealth.Target, 0, len(resolved.EntryNodes))
				for index, node := range resolved.EntryNodes {
					memberTag := fmt.Sprintf("runtime-chain-%03d-member-%03d", chainIndex, index)
					chainTarget.Members = append(chainTarget.Members, singbox.RuntimeNodeTarget{Tag: memberTag, Node: node})
					member := resolved.EntryMembers[index]
					healthTargets = append(healthTargets, poolhealth.Target{
						Tag: memberTag, SubscriptionID: member.SubscriptionID, NodeID: member.NodeID, Name: node.Name,
					})
					namesByTag[memberTag] = node.Name + " → " + resolved.ExitNode.Name
				}
				pool := *resolved.EntryPool
				health = &poolhealth.Config{
					Address: controllerAddress, Secret: controllerSecret, SelectorTag: chainTag, ProbeURLs: append([]string(nil), probeURLs...),
					Interval: time.Duration(pool.ProbeIntervalSeconds) * time.Second, Tolerance: time.Duration(pool.ToleranceMS) * time.Millisecond,
					IdleTimeout: time.Duration(pool.IdleTimeoutSeconds) * time.Second, HighLatencyThreshold: time.Duration(pool.HighLatencyThresholdMS) * time.Millisecond,
					ConsecutiveFailures: pool.ConsecutiveFailures, RecoverySuccesses: pool.RecoverySuccesses,
					MaxBackoff: time.Duration(pool.MaxBackoffSeconds) * time.Second, Targets: healthTargets,
				}
			}
			chainTargets = append(chainTargets, chainTarget)
			targets[key] = runtimeCatalogTarget{
				Tag: chainTag, Signature: chainSignature(resolved), Health: health,
				Runtime: Runtime{
					TargetType: "chain", ChainID: resolved.Chain.ID, ChainName: resolved.Chain.Name,
					ChainEntryType: resolved.Chain.EntryType, ChainEntryName: chainView.EntryName, ChainExitName: resolved.ExitNode.Name,
				},
			}
		}
	}
	selected, available := targets[selectedKey]
	if !available || selected.Signature != selectedSignature {
		if input.PoolID != "" && len(probeURLs) == 0 {
			return runtimeCatalogBuild{}, fmt.Errorf("at least one quick-test target is required for node-pool selection")
		}
		return runtimeCatalogBuild{}, fmt.Errorf("selected runtime target is unavailable")
	}
	channelTargets := make([]singbox.RuntimeChannelTarget, 0)
	if s.channels != nil {
		certificatePath, keyPath := s.channels.TLSPaths()
		for index, resolved := range s.channels.ResolveEnabled() {
			outboundTag, available := nodeTags[nodeTargetKey(resolved.Channel.Node.SubscriptionID, resolved.Channel.Node.NodeID)]
			if !available {
				continue
			}
			listen := "127.0.0.1"
			if resolved.Channel.Direction == proxychannel.DirectionReverse {
				listen = "0.0.0.0"
			}
			channelTargets = append(channelTargets, singbox.RuntimeChannelTarget{
				Tag: fmt.Sprintf("runtime-channel-%03d", index), Protocol: string(resolved.Channel.Protocol),
				Listen: listen, Port: resolved.Channel.Port, Username: resolved.Channel.Username, Password: resolved.Channel.Password,
				OutboundTag: outboundTag, CertificatePath: certificatePath, KeyPath: keyPath,
			})
		}
	}
	content, err := singbox.BuildSelectableConfig(nodeTargets, poolTargets, chainTargets, channelTargets, selected.Tag, input.Mode, s.mixedPort, routeRules,
		singbox.ControllerOptions{Address: controllerAddress, Secret: controllerSecret}, dnsProfile, input.AllowLan)
	if err != nil {
		return runtimeCatalogBuild{}, err
	}
	return runtimeCatalogBuild{
		Content: content, Targets: targets, Selected: selected,
		ControllerAddress: controllerAddress, ControllerSecret: controllerSecret,
		Resolver: catalogChainResolver(namesByTag),
	}, nil
}

func (s *Service) resolveRequestedTarget(ctx context.Context, input ApplyInput, validateProbeURLs bool) (string, Runtime, string, error) {
	if input.Direct {
		if input.ChainID != "" || input.PoolID != "" || input.SubscriptionID != "" || input.NodeID != "" {
			return "", Runtime{}, "", fmt.Errorf("direct cannot be combined with another runtime target")
		}
		return directTargetKey(), Runtime{TargetType: "direct"}, directTargetKey(), nil
	}
	if input.ChainID != "" {
		if input.PoolID != "" || input.SubscriptionID != "" || input.NodeID != "" {
			return "", Runtime{}, "", fmt.Errorf("chainId cannot be combined with another runtime target")
		}
		if s.chains == nil {
			return "", Runtime{}, "", fmt.Errorf("proxy chain control is unavailable")
		}
		resolved, err := s.chains.Resolve(input.ChainID)
		if err != nil {
			return "", Runtime{}, "", err
		}
		if validateProbeURLs && resolved.EntryPool != nil {
			if err := s.pools.ValidateProbeURLs(ctx, resolved.Chain.EntryPoolID); err != nil {
				return "", Runtime{}, "", err
			}
		}
		entryName := ""
		if resolved.EntryNode != nil {
			entryName = resolved.EntryNode.Name
		} else if resolved.EntryPool != nil {
			entryName = resolved.EntryPool.Name
		}
		runtime := Runtime{
			TargetType: "chain", ChainID: resolved.Chain.ID, ChainName: resolved.Chain.Name,
			ChainEntryType: resolved.Chain.EntryType, ChainEntryName: entryName, ChainExitName: resolved.ExitNode.Name,
		}
		return chainTargetKey(resolved.Chain.ID), runtime, chainSignature(resolved), nil
	}
	if input.PoolID != "" {
		if input.SubscriptionID != "" || input.NodeID != "" {
			return "", Runtime{}, "", fmt.Errorf("poolId cannot be combined with subscriptionId or nodeId")
		}
		if s.pools == nil {
			return "", Runtime{}, "", fmt.Errorf("node pool control is unavailable")
		}
		if validateProbeURLs {
			if err := s.pools.ValidateProbeURLs(ctx, input.PoolID); err != nil {
				return "", Runtime{}, "", err
			}
		}
		pool, members, _, err := s.pools.ResolveWithMembers(input.PoolID)
		if err != nil {
			return "", Runtime{}, "", err
		}
		return poolTargetKey(pool.ID), Runtime{TargetType: "pool", PoolID: pool.ID, PoolName: pool.Name}, poolSignature(pool, members), nil
	}
	if input.SubscriptionID == "" || input.NodeID == "" {
		return "", Runtime{}, "", fmt.Errorf("subscriptionId and nodeId are required for a node target")
	}
	subscriptionValue, node, err := s.subscriptions.SelectedNode(input.SubscriptionID, input.NodeID)
	if err != nil {
		return "", Runtime{}, "", err
	}
	if _, err := s.subscriptions.Activate(subscriptionValue.ID); err != nil {
		return "", Runtime{}, "", err
	}
	if _, err := s.subscriptions.SelectNode(subscriptionValue.ID, node.ID); err != nil {
		return "", Runtime{}, "", err
	}
	runtime := Runtime{TargetType: "node", SubscriptionID: subscriptionValue.ID, NodeID: node.ID, NodeName: node.Name}
	return nodeTargetKey(subscriptionValue.ID, node.ID), runtime, nodeSignature(node), nil
}

func poolURLTestOptions(pool nodepool.Pool) singbox.URLTestOptions {
	return singbox.URLTestOptions{
		URL: pool.ProbeURL, Interval: time.Duration(pool.ProbeIntervalSeconds) * time.Second,
		Tolerance: pool.ToleranceMS, IdleTimeout: time.Duration(pool.IdleTimeoutSeconds) * time.Second,
		InterruptExistingConnections: pool.InterruptExistingConnections,
	}
}

func nodeTargetKey(subscriptionID, nodeID string) string {
	return "node\x00" + subscriptionID + "\x00" + nodeID
}
func poolTargetKey(poolID string) string   { return "pool\x00" + poolID }
func chainTargetKey(chainID string) string { return "chain\x00" + chainID }
func directTargetKey() string              { return "direct" }

func currentTargetKey(runtime Runtime) string {
	if runtime.TargetType == "direct" {
		return directTargetKey()
	}
	if runtime.TargetType == "pool" {
		return poolTargetKey(runtime.PoolID)
	}
	if runtime.TargetType == "chain" {
		return chainTargetKey(runtime.ChainID)
	}
	return nodeTargetKey(runtime.SubscriptionID, runtime.NodeID)
}

func nodeSignature(node subscription.Node) string {
	return fmt.Sprintf("%#v", node)
}

func poolSignature(pool nodepool.Pool, members []nodepool.Member) string {
	return fmt.Sprintf("%#v|%#v", pool, members)
}

func chainSignature(resolved proxychain.Resolved) string {
	return fmt.Sprintf("%#v|%#v|%#v|%#v|%#v", resolved.Chain, resolved.EntryNode, resolved.EntryPool, resolved.EntryNodes, resolved.ExitNode)
}

func catalogChainResolver(namesByTag map[string]string) connmon.Resolver {
	return func(chains []string) string {
		for _, chain := range chains {
			if name, ok := namesByTag[chain]; ok {
				return name
			}
			switch chain {
			case "direct", "block":
				return chain
			}
		}
		return ""
	}
}

func (s *Service) ReapplyRules(ctx context.Context) (Runtime, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	runtime := s.Status(ctx)
	if runtime.State != supervisor.StateRunning {
		return runtime, nil
	}
	input := ApplyInput{Mode: runtime.Mode, AllowLan: runtime.AllowLan}
	if runtime.TargetType == "direct" {
		input.Direct = true
	} else if runtime.TargetType == "pool" {
		input.PoolID = runtime.PoolID
	} else if runtime.TargetType == "chain" {
		input.ChainID = runtime.ChainID
	} else {
		input.SubscriptionID = runtime.SubscriptionID
		input.NodeID = runtime.NodeID
	}
	return s.applyFull(ctx, input)
}

func (s *Service) Stop(ctx context.Context) (Runtime, error) {
	s.cancelApplyOperation()
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
	allowLan := s.runtime.AllowLan
	s.runtime = Runtime{State: supervisor.StateStopped, AllowLan: allowLan}
	s.runtimeCatalog = nil
	s.controllerAddress = ""
	s.controllerSecret = ""
	s.mu.Unlock()
	s.publish("runtime.stopped", map[string]string{"state": "stopped"})
	return s.Status(ctx), nil
}

func (s *Service) cancelApplyOperation() {
	s.mu.RLock()
	cancel := s.operationCancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
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
		s.runtimeCatalog = nil
		s.controllerAddress = ""
		s.controllerSecret = ""
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
	if runtime.TargetType == "direct" {
		return directTargetKey()
	}
	if runtime.ChainID != "" {
		return runtime.ChainID
	}
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
	s.runtimeCatalog = nil
	s.controllerAddress = ""
	s.controllerSecret = ""
	s.mu.Unlock()
	s.publish("runtime.failed", map[string]string{"error": err.Error()})
	return s.Status(ctx)
}

func (s *Service) publish(eventType string, payload any) {
	if s.events != nil {
		_, _ = s.events.Publish(eventType, payload)
	}
}
