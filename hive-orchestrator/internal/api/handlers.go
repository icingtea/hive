package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"hive-mind/internal/domain"
	"hive-mind/internal/podmanager"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ── Health ────────────────────────────────────────────────────────────────────

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Pod management (direct k8s, Phase 1 only) ────────────────────────────────

type spawnPodRequest struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Image     string            `json:"image"`
	EnvVars   map[string]string `json:"env_vars"`
}

func (s *Server) handleSpawnPod(w http.ResponseWriter, r *http.Request) {
	var req spawnPodRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Image == "" {
		writeError(w, http.StatusBadRequest, "name and image are required")
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}

	opts := podmanager.SpawnOptions{
		DeploymentID: uuid.New().String(),
		AgentID:      uuid.New().String(),
		PodName:      req.Name,
		Namespace:    req.Namespace,
		ImageRef:     req.Image,
		EnvVars:      req.EnvVars,
	}
	if err := s.podMgr.Spawn(r.Context(), opts); err != nil {
		s.log.Error("spawn pod failed", "err", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("spawn failed: %s", err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"pod_name": req.Name, "namespace": req.Namespace})
}

func (s *Server) handleListPods(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "default"
	}
	pods, err := s.podMgr.List(r.Context(), ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pods)
}

func (s *Server) handleTerminatePod(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "default"
	}
	if err := s.podMgr.Terminate(r.Context(), name, ns); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Agents ────────────────────────────────────────────────────────────────────

type createAgentRequest struct {
	Name    string `json:"name"`
	RepoURL string `json:"repo_url"`
	Branch  string `json:"branch"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req createAgentRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "name and repo_url are required")
		return
	}
	if req.Branch == "" {
		req.Branch = "main"
	}

	now := time.Now()
	agent := &domain.Agent{
		ID:        uuid.New().String(),
		Name:      req.Name,
		RepoURL:   req.RepoURL,
		Branch:    req.Branch,
		EnvVars:   "{}",
		Status:    domain.AgentStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateAgent(r.Context(), agent); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, err := s.store.GetAgent(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// ── Deployments ───────────────────────────────────────────────────────────────

type deployRequest struct {
	CommitSHA string `json:"commit_sha"` // optional; defaults to empty (builder uses "latest")
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")

	var req deployRequest
	// body is optional — ignore decode errors
	_ = readJSON(r, &req)

	dep, err := s.orch.Deploy(r.Context(), agentID, req.CommitSHA)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 202 — build is running async
	writeJSON(w, http.StatusAccepted, dep)
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := s.store.GetDeployment(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "deployment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

func (s *Server) handleStopDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.orch.StopDeployment(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
