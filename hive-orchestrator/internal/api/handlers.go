package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
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

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func (s *Server) handleIngestHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb domain.Heartbeat
	if err := readJSON(r, &hb); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	hb.ReceivedAt = time.Now()

	s.hbMu.Lock()
	s.heartbeats[hb.DeploymentID] = &hb
	s.hbMu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	hb, ok := s.GetHeartbeat(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no heartbeat received yet")
		return
	}
	writeJSON(w, http.StatusOK, hb)
}

// GetHeartbeat satisfies dashboard.HeartbeatGetter.
func (s *Server) GetHeartbeat(deploymentID string) (*domain.Heartbeat, bool) {
	s.hbMu.RLock()
	hb, ok := s.heartbeats[deploymentID]
	s.hbMu.RUnlock()
	return hb, ok
}

// ── Agent routing & communication ────────────────────────────────────────────

// RegisterAgentRoute stores the self-declared name → pod address mapping.
func (s *Server) RegisterAgentRoute(name, podIP, port, podName, namespace string) {
	s.agentRoutesMu.Lock()
	defer s.agentRoutesMu.Unlock()
	s.agentRoutes[name] = &domain.AgentRoute{
		AgentName: name,
		PodIP:     podIP,
		Port:      port,
		PodName:   podName,
		Namespace: namespace,
	}
}

// GetCommLogs satisfies dashboard.CommLogGetter.
func (s *Server) GetCommLogs() []*domain.CommLog {
	s.commLogsMu.RLock()
	defer s.commLogsMu.RUnlock()
	out := make([]*domain.CommLog, len(s.commLogs))
	copy(out, s.commLogs)
	return out
}

type communicateRequest struct {
	DestinationAgentID       string         `json:"destination_agent_id"`
	DestinationAgentEndpoint string         `json:"destination_agent_endpoint"` // "data" or "start"
	Payload                  map[string]any `json:"payload"`
}

func (s *Server) handleCommunicate(w http.ResponseWriter, r *http.Request) {
	// Identify sender by reverse-lookup of the caller's pod IP.
	fromAgent := r.Header.Get("X-Hive-Agent-Name")
	if fromAgent == "" {
		callerIP := r.RemoteAddr
		// Strip port from "ip:port"
		if host, _, err := net.SplitHostPort(callerIP); err == nil {
			callerIP = host
		}
		s.agentRoutesMu.RLock()
		for name, route := range s.agentRoutes {
			if route.PodIP == callerIP {
				fromAgent = name
				break
			}
		}
		s.agentRoutesMu.RUnlock()
	}

	var req communicateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.DestinationAgentID == "" {
		writeError(w, http.StatusBadRequest, "destination_agent_id is required")
		return
	}
	endpoint := req.DestinationAgentEndpoint
	if endpoint == "" {
		endpoint = "data"
	}

	// Look up destination pod
	s.agentRoutesMu.RLock()
	route, ok := s.agentRoutes[req.DestinationAgentID]
	s.agentRoutesMu.RUnlock()

	entry := &domain.CommLog{
		ID:            uuid.New().String(),
		FromAgentName: fromAgent,
		ToAgentName:   req.DestinationAgentID,
		Endpoint:      endpoint,
		Timestamp:     time.Now(),
	}

	if payloadBytes, err := json.Marshal(req.Payload); err == nil {
		entry.Payload = string(payloadBytes)
	}

	payloadBytes, _ := json.Marshal(req.Payload)

	// "external" endpoint means this is a final result — store it and return.
	// Key by destination_agent_id (agent-controlled), fall back to fromAgent.
	if endpoint == "external" {
		key := req.DestinationAgentID
		if key == "" {
			key = fromAgent
		}
		s.agentResultsMu.Lock()
		s.agentResults[key] = json.RawMessage(payloadBytes)
		s.agentResultsMu.Unlock()
		entry.Status = "delivered"
		s.appendCommLog(entry)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !ok {
		entry.Status = "failed"
		entry.Error = "destination agent not found in routing table"
		s.appendCommLog(entry)
		writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", req.DestinationAgentID))
		return
	}

	// Forward only the payload to the destination pod via kubectl exec
	if err := forwardToAgent(route, endpoint, payloadBytes); err != nil {
		entry.Status = "failed"
		entry.Error = err.Error()
		s.appendCommLog(entry)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("forward failed: %s", err))
		return
	}

	entry.Status = "delivered"
	s.appendCommLog(entry)
	w.WriteHeader(http.StatusNoContent)
}

// handleExternalMessage accepts {type: "start"|"data", payload: {...}} from any
// external service, strips the envelope, and forwards the inner payload to the
// agent's /start or /data endpoint accordingly.
func (s *Server) handleExternalMessage(w http.ResponseWriter, r *http.Request) {
	agentName := chi.URLParam(r, "agent_name")

	var envelope struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := readJSON(r, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON — expected {type, payload}")
		return
	}
	endpoint := strings.ToLower(envelope.Type)
	if endpoint != "start" && endpoint != "data" {
		writeError(w, http.StatusBadRequest, `type must be "start" or "data"`)
		return
	}

	payloadBytes, err := json.Marshal(envelope.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	s.agentRoutesMu.RLock()
	route, ok := s.agentRoutes[agentName]
	s.agentRoutesMu.RUnlock()

	entry := &domain.CommLog{
		ID:            uuid.New().String(),
		FromAgentName: "external",
		ToAgentName:   agentName,
		Endpoint:      endpoint,
		Payload:       string(payloadBytes),
		Timestamp:     time.Now(),
	}

	if !ok {
		entry.Status = "failed"
		entry.Error = "agent not found in routing table"
		s.appendCommLog(entry)
		writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentName))
		return
	}

	if err := forwardToAgent(route, endpoint, payloadBytes); err != nil {
		entry.Status = "failed"
		entry.Error = err.Error()
		s.appendCommLog(entry)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("forward failed: %s", err))
		return
	}

	entry.Status = "delivered"
	s.appendCommLog(entry)
	w.WriteHeader(http.StatusNoContent)
}

// forwardToAgent delivers payloadBytes to the agent pod's /<endpoint> using kubectl exec.
// This avoids the need for the orchestrator host to have direct network access to pod IPs.
func forwardToAgent(route *domain.AgentRoute, endpoint string, payloadBytes []byte) error {
	pyPayload := strings.ReplaceAll(string(payloadBytes), `'`, `'"'"'`)
	script := fmt.Sprintf(
		`import urllib.request,sys; req=urllib.request.Request('http://localhost:%s/%s',data=b'%s',headers={'Content-Type':'application/json'},method='POST'); r=urllib.request.urlopen(req,timeout=10); sys.exit(0 if r.status < 400 else 1)`,
		route.Port, endpoint, pyPayload,
	)
	out, err := exec.Command("kubectl", "exec", route.PodName,
		"-n", route.Namespace,
		"-c", "agent",
		"--", "python3", "-c", script,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl exec: %w — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *Server) appendCommLog(entry *domain.CommLog) {
	s.commLogsMu.Lock()
	defer s.commLogsMu.Unlock()
	s.commLogs = append(s.commLogs, entry)
	// Cap at 1000 entries
	if len(s.commLogs) > 1000 {
		s.commLogs = s.commLogs[len(s.commLogs)-1000:]
	}
}

func (s *Server) handleGetCommLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.GetCommLogs())
}

// handleGetResult returns the latest result payload pushed by an agent via
// endpoint="external". Returns 404 if no result has been received yet.
func (s *Server) handleGetResult(w http.ResponseWriter, r *http.Request) {
	agentName := chi.URLParam(r, "agent_name")
	s.agentResultsMu.RLock()
	result, ok := s.agentResults[agentName]
	s.agentResultsMu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no result yet for agent "+agentName)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}
