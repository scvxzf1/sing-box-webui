package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"sing-box-webui/internal/application"
	"sing-box-webui/internal/connectivity"
	"sing-box-webui/internal/control"
	"sing-box-webui/internal/core"
	"sing-box-webui/internal/dnsprofile"
	"sing-box-webui/internal/events"
	"sing-box-webui/internal/latency"
	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/routing"
	"sing-box-webui/internal/subscription"
	"sing-box-webui/internal/trafficpolicy"
)

type LatencyTester interface {
	Test(context.Context, string, []string) (latency.Response, error)
}

type CoreController interface {
	Info(context.Context) (core.Info, error)
	Update(context.Context, string) (core.Info, error)
	Rollback(context.Context) (core.Info, error)
}

type ServerConfig struct {
	Address       string
	DevOrigin     string
	Version       string
	Logger        *slog.Logger
	Events        *events.Broker
	Subscriptions *subscription.Manager
	Pools         *nodepool.Manager
	Rules         *routing.Manager
	DNS           *dnsprofile.Manager
	Latency       LatencyTester
	Control       *control.Service
	Core          CoreController
	TrafficPolicy *trafficpolicy.Manager
	Connectivity  *connectivity.Manager
	WebToken      string
}

type Server struct {
	address       string
	devOrigin     string
	version       string
	logger        *slog.Logger
	events        *events.Broker
	subscriptions *subscription.Manager
	pools         *nodepool.Manager
	rules         *routing.Manager
	dns           *dnsprofile.Manager
	latency       LatencyTester
	control       *control.Service
	core          CoreController
	trafficPolicy *trafficpolicy.Manager
	connectivity  *connectivity.Manager
	csrfToken     string
	webToken      string
	sessionSecret [32]byte
	handler       http.Handler
}

func NewServer(config ServerConfig) (*Server, error) {
	if err := application.ValidateLoopbackAddress(config.Address); err != nil {
		return nil, fmt.Errorf("validate server address: %w", err)
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Events == nil {
		config.Events = events.NewBroker(128, 16)
	}

	server := &Server{
		address:       config.Address,
		devOrigin:     config.DevOrigin,
		version:       config.Version,
		logger:        config.Logger,
		events:        config.Events,
		subscriptions: config.Subscriptions,
		pools:         config.Pools,
		rules:         config.Rules,
		dns:           config.DNS,
		latency:       config.Latency,
		control:       config.Control,
		core:          config.Core,
		trafficPolicy: config.TrafficPolicy,
		connectivity:  config.Connectivity,
		csrfToken:     newCSRFToken(),
		webToken:      config.WebToken,
	}
	if _, err := rand.Read(server.sessionSecret[:]); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	server.handler = server.securityMiddleware(server.routes())
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.address, err)
	}

	httpServer := &http.Server{
		Addr:              s.address,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown Web API: %w", err)
		}
		err := <-serveResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/api/v1/auth/login", s.login)
	mux.HandleFunc("/api/v1/auth/logout", s.logout)
	mux.HandleFunc("/api/v1/status", s.status)
	mux.HandleFunc("/api/v1/events", s.eventStream)
	mux.HandleFunc("/api/v1/session", s.session)
	mux.HandleFunc("/api/v1/subscriptions", s.subscriptionsCollection)
	mux.HandleFunc("/api/v1/subscriptions/order", s.subscriptionsOrder)
	mux.HandleFunc("/api/v1/subscriptions/{id}", s.subscriptionItem)
	mux.HandleFunc("/api/v1/subscriptions/{id}/refresh", s.refreshSubscription)
	mux.HandleFunc("/api/v1/subscriptions/{id}/activate", s.activateSubscription)
	mux.HandleFunc("/api/v1/subscriptions/{id}/selection", s.selectNode)
	mux.HandleFunc("/api/v1/subscriptions/{id}/latency", s.testNodeLatency)
	mux.HandleFunc("/api/v1/pools", s.poolsCollection)
	mux.HandleFunc("/api/v1/pools/order", s.poolsOrder)
	mux.HandleFunc("/api/v1/pools/{id}", s.poolItem)
	mux.HandleFunc("/api/v1/rules", s.rulesCollection)
	mux.HandleFunc("/api/v1/rules/order", s.rulesOrder)
	mux.HandleFunc("/api/v1/rules/{id}", s.ruleItem)
	mux.HandleFunc("/api/v1/rule-pools", s.rulePoolsCollection)
	mux.HandleFunc("/api/v1/rule-pools/order", s.rulePoolsOrder)
	mux.HandleFunc("/api/v1/rule-pools/{id}", s.rulePoolItem)
	mux.HandleFunc("/api/v1/runtime", s.runtimeStatus)
	mux.HandleFunc("/api/v1/runtime/apply", s.applyRuntime)
	mux.HandleFunc("/api/v1/runtime/stop", s.stopRuntime)
	mux.HandleFunc("/api/v1/links", s.links)
	mux.HandleFunc("/api/v1/links/clear", s.clearLinks)
	mux.HandleFunc("/api/v1/traffic-policy", s.trafficPolicyStatus)
	mux.HandleFunc("/api/v1/dns/profile", s.dnsProfileResource)
	mux.HandleFunc("/api/v1/connectivity", s.connectivityCollection)
	mux.HandleFunc("/api/v1/connectivity/test", s.connectivityTest)
	mux.HandleFunc("/api/v1/connectivity/diagnostic", s.connectivityDiagnostic)
	mux.HandleFunc("/api/v1/connectivity/{id}", s.connectivityItem)
	mux.HandleFunc("/api/v1/connectivity/{id}/test", s.connectivityTest)
	mux.HandleFunc("/api/v1/core", s.coreStatus)
	mux.HandleFunc("/api/v1/core/update", s.updateCore)
	mux.HandleFunc("/api/v1/core/rollback", s.rollbackCore)
	mux.HandleFunc("/", s.notFound)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}

	coreStatus := componentStatus{State: "unavailable", Detail: "Core supervisor is not configured"}
	singBoxStatus := componentStatus{State: "unavailable", Detail: "sing-box is not attached"}
	if s.control != nil {
		runtime := s.control.Status(r.Context())
		if runtime.Capabilities.SingBox.Available {
			coreStatus = componentStatus{State: "healthy", Detail: "Core supervisor is ready"}
		}
		if runtime.State == "running" {
			detail := runtime.NodeName
			if detail == "" {
				detail = runtime.PoolName
			}
			singBoxStatus = componentStatus{State: "healthy", Detail: detail}
		} else if runtime.State == "failed" {
			singBoxStatus = componentStatus{State: "failed", Detail: runtime.LastError}
		}
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Service: "sing-box-webui",
		Version: s.version,
		Components: map[string]componentStatus{
			"web":     {State: "healthy"},
			"core":    coreStatus,
			"singBox": singBoxStatus,
		},
		Timestamp: time.Now().UTC(),
	})
}

type statusResponse struct {
	Service    string                     `json:"service"`
	Version    string                     `json:"version"`
	Components map[string]componentStatus `json:"components"`
	Timestamp  time.Time                  `json:"timestamp"`
}

type componentStatus struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "Streaming is not supported")
		return
	}

	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")

	snapshotPayload, _ := json.Marshal(map[string]string{"web": "healthy"})
	snapshot := events.Event{
		ID:        0,
		Type:      "snapshot",
		Timestamp: time.Now().UTC(),
		Payload:   snapshotPayload,
	}
	if err := writeSSE(w, snapshot); err != nil {
		return
	}
	flusher.Flush()

	stream, unsubscribe := s.events.Subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-stream:
			if !ok {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event events.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", strconv.FormatUint(event.ID, 10), event.Type, data)
	return err
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found")
}
