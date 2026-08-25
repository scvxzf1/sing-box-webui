package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"sing-box-webui/internal/api"
	"sing-box-webui/internal/application"
	"sing-box-webui/internal/configstore"
	"sing-box-webui/internal/connectivity"
	"sing-box-webui/internal/control"
	"sing-box-webui/internal/core"
	"sing-box-webui/internal/dnsprofile"
	"sing-box-webui/internal/events"
	"sing-box-webui/internal/latency"
	"sing-box-webui/internal/netresolve"
	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/platform/privilege"
	"sing-box-webui/internal/platform/systemproxy"
	"sing-box-webui/internal/proxychain"
	"sing-box-webui/internal/proxychannel"
	"sing-box-webui/internal/routing"
	"sing-box-webui/internal/singbox"
	"sing-box-webui/internal/subscription"
	"sing-box-webui/internal/supervisor"
	"sing-box-webui/internal/trafficpolicy"
)

var version = "dev"

func webServerAuthentication(config application.Config) (string, bool) {
	return config.WebToken, !config.WebAuthEnabled
}

func channelReservedPorts(config application.Config) []uint16 {
	ports := []uint16{config.MixedPort}
	if _, port, err := net.SplitHostPort(config.Address); err == nil {
		if value, parseErr := strconv.ParseUint(port, 10, 16); parseErr == nil && value != 0 {
			ports = append(ports, uint16(value))
		}
	}
	if origin, err := url.Parse(config.DevOrigin); err == nil {
		if value, parseErr := strconv.ParseUint(origin.Port(), 10, 16); parseErr == nil && value != 0 {
			ports = append(ports, uint16(value))
		}
	}
	return ports
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if privilege.IsElevated() {
		logger.Error("refusing to run the Web API with elevated privileges")
		os.Exit(1)
	}

	config, err := application.LoadConfig()
	if err != nil {
		logger.Error("invalid runtime configuration", "error", err)
		os.Exit(1)
	}

	netresolve.DefaultDohEndpoint = config.DohEndpoint
	singbox.DefaultTUNAddress = config.TUNAddress

	broker := events.NewBroker(128, 16)
	subscriptions, err := subscription.OpenManager(filepath.Join(config.DataDir, "subscriptions"), broker)
	if err != nil {
		logger.Error("open subscription store", "error", err)
		os.Exit(1)
	}
	rules, err := routing.OpenManager(filepath.Join(config.DataDir, "routing"), broker)
	if err != nil {
		logger.Error("open routing rule store", "error", err)
		os.Exit(1)
	}
	dnsProfiles, err := dnsprofile.OpenManager(filepath.Join(config.DataDir, "dns"), broker)
	if err != nil {
		logger.Error("open DNS profile store", "error", err)
		os.Exit(1)
	}
	subscriptions.SetRuleSink(rules)
	pools, err := nodepool.OpenManager(filepath.Join(config.DataDir, "node-pools"), subscriptions)
	if err != nil {
		logger.Error("open node pool store", "error", err)
		os.Exit(1)
	}
	subscriptions.SetPoolSink(pools)
	if err := pools.ReconcileReferences(); err != nil {
		logger.Error("reconcile node pool references", "error", err)
		os.Exit(1)
	}
	chains, err := proxychain.OpenManager(filepath.Join(config.DataDir, "proxy-chains"), subscriptions, pools)
	if err != nil {
		logger.Error("open proxy chain store", "error", err)
		os.Exit(1)
	}
	channels, err := proxychannel.OpenManager(filepath.Join(config.DataDir, "proxy-channels"), subscriptions, channelReservedPorts(config)...)
	if err != nil {
		logger.Error("open proxy channel store", "error", err)
		os.Exit(1)
	}
	subscriptionIDs := make([]string, 0, len(subscriptions.List()))
	for _, item := range subscriptions.List() {
		subscriptionIDs = append(subscriptionIDs, item.ID)
	}
	if err := rules.ReconcileSubscriptionRules(subscriptionIDs); err != nil {
		logger.Error("reconcile subscription rules", "error", err)
		os.Exit(1)
	}

	bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), 30*time.Second)
	coreManager, err := core.Open(bootstrapCtx, config.DataDir, config.SingBoxBinary)
	cancelBootstrap()
	if err != nil {
		logger.Error("prepare sing-box core", "error", err)
		os.Exit(1)
	}
	singBoxClient, err := singbox.NewClient(coreManager.BinaryPath())
	if err != nil {
		logger.Error("open sing-box core", "error", err)
		os.Exit(1)
	}
	configStore, err := configstore.Open(filepath.Join(config.DataDir, "runtime"), configstore.ValidatorFunc(singBoxClient.Check))
	if err != nil {
		logger.Error("open runtime config store", "error", err)
		os.Exit(1)
	}
	processSupervisor := supervisor.NewManager(coreManager.BinaryPath())
	proxyController := systemproxy.NewGNOMEController(config.DataDir)
	controlService, err := control.New(control.Config{
		Subscriptions:   subscriptions,
		Pools:           pools,
		Chains:          chains,
		Channels:        channels,
		Rules:           rules,
		DNS:             dnsProfiles,
		Client:          singBoxClient,
		ConfigStore:     configStore,
		Supervisor:      processSupervisor,
		SystemProxy:     proxyController,
		Events:          broker,
		TUNEnabled:      config.EnableTUN,
		MixedPort:       config.MixedPort,
		PreferencesPath: filepath.Join(config.DataDir, "runtime", "preferences.json"),
	})
	if err != nil {
		logger.Error("open runtime preferences", "error", err)
		os.Exit(1)
	}
	// Subscriptions fetch direct-first, then fall back to the running proxy
	// (system-proxy mode); empty in TUN mode where the direct path is tunneled.
	subscriptions.SetProxyResolver(controlService.ProxyAddress)
	rules.SetReload(func() error {
		reloadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := controlService.ReapplyRules(reloadCtx)
		return err
	})
	dnsProfiles.SetReload(func() error {
		reloadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := controlService.ReapplyRules(reloadCtx)
		return err
	})
	channels.SetReload(func() error {
		reloadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := controlService.ReapplyRules(reloadCtx)
		return err
	})
	trafficPolicy, err := trafficpolicy.Open(filepath.Join(config.DataDir, "traffic-policy"), controlService, pools, broker)
	if err != nil {
		logger.Error("open traffic policy store", "error", err)
		os.Exit(1)
	}
	connectivityManager, err := connectivity.Open(filepath.Join(config.DataDir, "connectivity"), controlService)
	if err != nil {
		logger.Error("open connectivity store", "error", err)
		os.Exit(1)
	}
	controlService.SetHealthProbeSource(connectivityManager)
	webToken, allowUnauthenticated := webServerAuthentication(config)

	server, err := api.NewServer(api.ServerConfig{
		Address:              config.Address,
		DevOrigin:            config.DevOrigin,
		Version:              version,
		Logger:               logger,
		Events:               broker,
		Subscriptions:        subscriptions,
		Pools:                pools,
		Chains:               chains,
		Channels:             channels,
		Rules:                rules,
		DNS:                  dnsProfiles,
		Latency:              latency.NewService(subscriptions, singBoxClient, filepath.Join(config.DataDir, "latency")),
		Control:              controlService,
		Core:                 coreManager,
		TrafficPolicy:        trafficPolicy,
		Connectivity:         connectivityManager,
		WebToken:             webToken,
		AllowUnauthenticated: allowUnauthenticated,
	})
	if err != nil {
		logger.Error("create Web API server", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go subscriptions.RunAutoUpdate(ctx)
	go trafficPolicy.Run(ctx)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if _, err := controlService.Stop(cleanupCtx); err != nil {
			logger.Error("restore proxy state during shutdown", "error", err)
		}
	}()

	logger.Info("starting Web API", "address", config.Address, "version", version)
	if err := server.ListenAndServe(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Web API stopped", "error", err)
		os.Exit(1)
	}
}
