package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"hive-mind/internal/dashboard"
	"hive-mind/internal/domain"
	"hive-mind/internal/orchestrator"
	"hive-mind/internal/podmanager"
	"hive-mind/internal/registry"
)

type Server struct {
	cfg        ServerConfig
	store      registry.Store
	podMgr     podmanager.PodManager
	orch       *orchestrator.Orchestrator
	log        *slog.Logger
	http       *http.Server
	heartbeats map[string]*domain.Heartbeat
	hbMu       sync.RWMutex
	// routing table: self-declared agent name → pod address
	agentRoutes   map[string]*domain.AgentRoute
	agentRoutesMu sync.RWMutex
	// comm log: chronological list of routed messages
	commLogs   []*domain.CommLog
	commLogsMu sync.RWMutex
	// latest result per agent name (set when an agent sends endpoint="external")
	agentResults   map[string]json.RawMessage
	agentResultsMu sync.RWMutex
}

type ServerConfig struct {
	Host string
	Port int
}

func NewServer(
	cfg ServerConfig,
	store registry.Store,
	podMgr podmanager.PodManager,
	orch *orchestrator.Orchestrator,
	log *slog.Logger,
) *Server {
	s := &Server{
		cfg:        cfg,
		store:      store,
		podMgr:     podMgr,
		orch:       orch,
		log:        log,
		heartbeats:   make(map[string]*domain.Heartbeat),
		agentRoutes:  make(map[string]*domain.AgentRoute),
		agentResults: make(map[string]json.RawMessage),
	}
	s.http = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:      s.routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(s.log))

	r.Get("/healthz", s.handleHealthz)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	dashboard.Mount(r, s.store, s.orch, s.podMgr, s, s, s.log)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/agents", s.handleCreateAgent)
		r.Get("/agents", s.handleListAgents)
		r.Get("/agents/{id}", s.handleGetAgent)
		r.Post("/agents/{id}/deploy", s.handleDeploy)

		r.Get("/deployments/{id}", s.handleGetDeployment)
		r.Post("/deployments/{id}/stop", s.handleStopDeployment)
		r.Get("/deployments/{id}/heartbeat", s.handleGetHeartbeat)

		r.Post("/ingest/heartbeat", s.handleIngestHeartbeat)
		r.Post("/communicate", s.handleCommunicate)
		r.Get("/commlogs", s.handleGetCommLogs)

		// External message ingress / result polling
		r.Post("/message/{agent_name}", s.handleExternalMessage)
		r.Get("/result/{agent_name}", s.handleGetResult)

		// Phase 1 direct pod endpoints (useful for debugging)
		r.Post("/pods", s.handleSpawnPod)
		r.Get("/pods", s.handleListPods)
		r.Delete("/pods/{name}", s.handleTerminatePod)
	})

	return r
}

func (s *Server) Start() error {
	s.log.Info("HTTP server listening", "addr", s.http.Addr)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
