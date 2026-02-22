// Package dashboard provides a server-side rendered HTML dashboard for Hive.
// Self-contained: mount with dashboard.Mount(r, ...) and remove by deleting
// this package + the Mount call in server.go.
package dashboard

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"hive-mind/internal/dashboard/logbuf"
	"hive-mind/internal/domain"
	"hive-mind/internal/orchestrator"
	"hive-mind/internal/podmanager"
	"hive-mind/internal/registry"
)

//go:embed templates/* static/*
var assets embed.FS

// HeartbeatGetter is a minimal interface so the dashboard can read heartbeats
// without depending on the full api.Server.
type HeartbeatGetter interface {
	GetHeartbeat(deploymentID string) (*domain.Heartbeat, bool)
}

// CommLogGetter is a minimal interface so the dashboard can read comm logs.
type CommLogGetter interface {
	GetCommLogs() []*domain.CommLog
}

type Handler struct {
	store  registry.Store
	orch   *orchestrator.Orchestrator
	podMgr podmanager.PodManager
	hb     HeartbeatGetter
	comms  CommLogGetter
	log    *slog.Logger
	tmpl   *template.Template
}

// Mount registers all dashboard routes under /dashboard.
func Mount(r chi.Router, store registry.Store, orch *orchestrator.Orchestrator, podMgr podmanager.PodManager, hb HeartbeatGetter, comms CommLogGetter, log *slog.Logger) {
	h := &Handler{store: store, orch: orch, podMgr: podMgr, hb: hb, comms: comms, log: log}
	h.tmpl = h.parseTemplates()

	r.Route("/dashboard", func(r chi.Router) {
		staticFS, _ := fs.Sub(assets, "static")
		r.Handle("/static/*", http.StripPrefix("/dashboard/static/", http.FileServer(http.FS(staticFS))))

		// Pages
		r.Get("/", h.handleIndex)
		r.Get("/deployments/{id}", h.handleDeploymentDetail)
		r.Get("/comms", h.handleCommsPage)
		r.Get("/wizard", h.handleWizardPage)
		r.Post("/wizard", h.handleWizardSubmit)

		// HTMX partials
		r.Get("/partials/deployments", h.handlePartialDeployments)
		r.Get("/partials/deployment/{id}", h.handlePartialDeploymentRow)
		r.Get("/partials/deployment-meta/{id}", h.handlePartialDeploymentMeta)
		r.Get("/partials/running-count", h.handlePartialRunningCount)
		r.Get("/partials/deployment-status/{id}", h.handlePartialDeploymentStatus)
		r.Get("/partials/heartbeat/{id}", h.handlePartialHeartbeat)
		r.Get("/partials/commlogs", h.handlePartialCommLogs)

		// SSE
		r.Get("/deployments/{id}/logs/stream", h.handleLogStream)

		// Actions
		r.Post("/deploy", h.handleDeploy)
		r.Post("/deployments/{id}/stop", h.handleStopDeployment)
		r.Delete("/deployments/{id}", h.handleDeleteDeployment)
	})
}

// ── Template setup ────────────────────────────────────────────────────────────

var funcMap = template.FuncMap{
	"statusClass":      func(s domain.DeploymentStatus) string { return string(s) },
	"agentStatusClass": func(s domain.AgentStatus) string { return string(s) },
	"isActiveStatus": func(s domain.DeploymentStatus) bool {
		return s == domain.DeploymentStatusBuilding || s == domain.DeploymentStatusPending
	},
	"not": func(v interface{}) bool {
		if v == nil {
			return true
		}
		switch val := v.(type) {
		case bool:
			return !val
		case []string:
			return len(val) == 0
		}
		return false
	},
	"relTime": relTimeFn,
	"shortImage": func(ref string) string {
		p := strings.Split(ref, "/")
		return p[len(p)-1]
	},
	"repoShort": func(url string) string {
		url = strings.TrimPrefix(url, "https://github.com/")
		url = strings.TrimPrefix(url, "http://github.com/")
		return strings.TrimSuffix(url, ".git")
	},
	"slice": func(s string, i, j int) string {
		if j > len(s) {
			j = len(s)
		}
		return s[i:j]
	},
	"iter": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
	"truncate": func(s string, n int) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
}

func (h *Handler) parseTemplates() *template.Template {
	return template.Must(template.New("").Funcs(funcMap).ParseFS(assets, "templates/*.html"))
}

// ── Base wrapper ──────────────────────────────────────────────────────────────

// baseData is the data passed to base.html. Body is pre-rendered page content.
type baseData struct {
	Title        string
	Page         string
	RunningCount int
	Body         template.HTML
}

// renderPage renders a named content template into a buffer, then wraps it
// with base.html. This avoids the {{define}} last-parse-wins problem.
func (h *Handler) renderPage(w http.ResponseWriter, title, page, contentTmpl string, data any, runningCount int) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, contentTmpl, data); err != nil {
		h.log.Error("template render content", "template", contentTmpl, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	bd := baseData{
		Title:        title,
		Page:         page,
		RunningCount: runningCount,
		Body:         template.HTML(buf.String()),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "base.html", bd); err != nil {
		h.log.Error("template render base", "err", err)
	}
}

// ── View models ───────────────────────────────────────────────────────────────

type deploymentRow struct {
	*domain.Deployment
	AgentName string
	RepoShort string
	NodeName  string // kubernetes node (machine) the pod is running on
}

type indexData struct {
	Page         string
	Deployments  []deploymentRow
	RunningCount int
	TotalCount   int
	AgentCount   int
}

type deploymentDetailData struct {
	Page         string
	Deployment   *domain.Deployment
	AgentName    string
	RepoURL      string
	RepoShort    string
	LogLines     []string
	RunningCount int
}

func (h *Handler) buildIndexData(ctx context.Context) (indexData, error) {
	deps, err := h.store.ListAllDeployments(ctx)
	if err != nil {
		return indexData{}, err
	}
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return indexData{}, err
	}
	agentMap := make(map[string]*domain.Agent, len(agents))
	for _, a := range agents {
		agentMap[a.ID] = a
	}
	rows := make([]deploymentRow, 0, len(deps))
	running := 0
	for _, d := range deps {
		row := deploymentRow{Deployment: d}
		if a, ok := agentMap[d.AgentID]; ok {
			row.AgentName = a.Name
			row.RepoShort = repoShortFn(a.RepoURL)
		}
		if d.Status == domain.DeploymentStatusRunning {
			running++
			if d.PodName != "" && h.podMgr != nil {
				if status, err := h.podMgr.GetStatus(ctx, d.PodName, d.Namespace); err == nil {
					row.NodeName = status.NodeName
				}
			}
		}
		rows = append(rows, row)
	}
	return indexData{Page: "dashboard", Deployments: rows, RunningCount: running, TotalCount: len(deps), AgentCount: len(agents)}, nil
}

func relTimeFn(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func repoShortFn(url string) string {
	url = strings.TrimPrefix(url, "https://github.com/")
	url = strings.TrimPrefix(url, "http://github.com/")
	return strings.TrimSuffix(url, ".git")
}

// ── Page handlers ─────────────────────────────────────────────────────────────

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := h.buildIndexData(r.Context())
	if err != nil {
		h.log.Error("dashboard index", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderPage(w, "Instances", "dashboard", "page-index", data, data.RunningCount)
}

func (h *Handler) handleDeploymentDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := h.store.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	agents, _ := h.store.ListAgents(r.Context())
	data := deploymentDetailData{Page: "dashboard", Deployment: dep}
	for _, a := range agents {
		if a.ID == dep.AgentID {
			data.AgentName = a.Name
			data.RepoURL = a.RepoURL
			data.RepoShort = repoShortFn(a.RepoURL)
			break
		}
	}
	// Pre-populate existing log lines (e.g. on page refresh)
	buf := h.orch.Logs().Get(dep.ID)
	data.LogLines = buf.Lines(0)

	// Count running deployments for nav
	allDeps, _ := h.store.ListAllDeployments(r.Context())
	running := 0
	for _, d := range allDeps {
		if d.Status == domain.DeploymentStatusRunning {
			running++
		}
	}
	data.RunningCount = running

	h.renderPage(w, dep.ID[:8]+" — Hive", "dashboard", "page-deployment", data, running)
}

// ── Partial handlers ──────────────────────────────────────────────────────────

func (h *Handler) handlePartialDeployments(w http.ResponseWriter, r *http.Request) {
	data, err := h.buildIndexData(r.Context())
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	h.execTmpl(w, "deployments-table", data)
}

func (h *Handler) handlePartialDeploymentRow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := h.store.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	agents, _ := h.store.ListAgents(r.Context())
	row := deploymentRow{Deployment: dep}
	for _, a := range agents {
		if a.ID == dep.AgentID {
			row.AgentName = a.Name
			row.RepoShort = repoShortFn(a.RepoURL)
			break
		}
	}
	w.Header().Set("Content-Type", "text/html")
	h.execTmpl(w, "deployment-row", row)
}

func (h *Handler) handlePartialRunningCount(w http.ResponseWriter, r *http.Request) {
	deps, err := h.store.ListAllDeployments(r.Context())
	running := 0
	if err == nil {
		for _, d := range deps {
			if d.Status == domain.DeploymentStatusRunning {
				running++
			}
		}
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<span class="mono text-xs text-muted nav-run-count" id="nav-run-count" hx-get="/dashboard/partials/running-count" hx-trigger="every 5s" hx-swap="outerHTML">%d running</span>`, running)
}

func (h *Handler) handlePartialDeploymentStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := h.store.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	active := dep.Status == domain.DeploymentStatusBuilding || dep.Status == domain.DeploymentStatusPending
	pulse := ""
	if active {
		pulse = " badge-dot--pulse"
	}
	// Stop polling once terminal
	trigger := `hx-trigger="every 3s"`
	if !active && dep.Status != domain.DeploymentStatusRunning {
		trigger = "" // terminal state — no more polling
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<span id="title-status-badge" class="badge badge--%s badge--lg" hx-get="/dashboard/partials/deployment-status/%s" %s hx-swap="outerHTML"><span class="badge-dot%s"></span>%s</span>`,
		dep.Status, dep.ID, trigger, pulse, dep.Status)
}

func (h *Handler) handlePartialDeploymentMeta(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := h.store.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="meta-card" id="status-badge">
  <span class="meta-label">Status</span>
  <span class="badge badge--%s"><span class="badge-dot"></span>%s</span>
</div>`, dep.Status, dep.Status)
}

func (h *Handler) handlePartialHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "text/html")
	hb, ok := h.hb.GetHeartbeat(id)
	if !ok {
		fmt.Fprintf(w, `<div class="hb-grid" id="hb-grid" hx-get="/dashboard/partials/heartbeat/%s" hx-trigger="every 5s" hx-swap="outerHTML"><p class="hb-waiting">Waiting for heartbeat…</p></div>`, id)
		return
	}
	fmt.Fprintf(w, `<div class="hb-grid" id="hb-grid" hx-get="/dashboard/partials/heartbeat/%s" hx-trigger="every 5s" hx-swap="outerHTML">`, id)
	// Memory
	memVal := "—"
	if hb.MemoryBytes != nil {
		memVal = fmtBytes(*hb.MemoryBytes)
	}
	memLimit := ""
	if hb.MemoryLimitBytes != nil {
		memLimit = fmt.Sprintf(" / %s", fmtBytes(*hb.MemoryLimitBytes))
	}
	fmt.Fprintf(w, `<div class="hb-card"><span class="hb-label">Memory</span><span class="hb-value mono">%s%s</span></div>`, memVal, memLimit)
	// PID
	pidVal := "—"
	if hb.PID != nil {
		pidVal = fmt.Sprintf("%d", *hb.PID)
	}
	fmt.Fprintf(w, `<div class="hb-card"><span class="hb-label">Agent PID</span><span class="hb-value mono">%s</span></div>`, pidVal)
	// Kernel
	kernelVal := "—"
	if hb.Kernel != nil {
		kernelVal = *hb.Kernel
	}
	fmt.Fprintf(w, `<div class="hb-card"><span class="hb-label">Kernel</span><span class="hb-value mono">%s</span></div>`, kernelVal)
	// Uptime
	uptimeVal := "—"
	if hb.Uptime != nil {
		// /proc/uptime gives "seconds idle\n", show just the first field humanised
		uptimeVal = fmtUptime(*hb.Uptime)
	}
	fmt.Fprintf(w, `<div class="hb-card"><span class="hb-label">Uptime</span><span class="hb-value mono">%s</span></div>`, uptimeVal)
	// Last seen
	fmt.Fprintf(w, `<div class="hb-card"><span class="hb-label">Last Heartbeat</span><span class="hb-value">%s</span></div>`, relTimeFn(hb.ReceivedAt))
	fmt.Fprintf(w, `</div>`)
}

func fmtBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func fmtUptime(raw string) string {
	// /proc/uptime: "12345.67 98765.43"
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return raw
	}
	secs, err := fmt.Sscanf(parts[0], "%f", new(float64))
	_ = secs
	if err != nil {
		return parts[0]
	}
	var f float64
	fmt.Sscanf(parts[0], "%f", &f)
	d := int(f)
	h := d / 3600
	m := (d % 3600) / 60
	s := d % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// ── SSE log stream ────────────────────────────────────────────────────────────

func (h *Handler) handleLogStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	buf := h.orch.Logs().Get(id)

	// Send any existing lines first (catch-up for page reloads)
	offset := 0
	existing := buf.Lines(0)
	for _, line := range existing {
		fmt.Fprintf(w, "event: line\ndata: %s\n\n", jsonEscape(line))
	}
	offset = len(existing)
	flusher.Flush()

	// Check if already done
	select {
	case <-buf.Done():
		fmt.Fprintf(w, "event: done\ndata: done\n\n")
		flusher.Flush()
		return
	default:
	}

	notify, cancel := buf.Subscribe()
	defer cancel()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-buf.Done():
			// Drain any final lines
			newLines := buf.Lines(offset)
			for _, line := range newLines {
				fmt.Fprintf(w, "event: line\ndata: %s\n\n", jsonEscape(line))
			}
			fmt.Fprintf(w, "event: done\ndata: done\n\n")
			flusher.Flush()
			return
		case <-notify:
			newLines := buf.Lines(offset)
			for _, line := range newLines {
				fmt.Fprintf(w, "event: line\ndata: %s\n\n", jsonEscape(line))
			}
			offset += len(newLines)
			flusher.Flush()
		}
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// strip surrounding quotes
	return string(b[1 : len(b)-1])
}

// ── Action handlers ───────────────────────────────────────────────────────────

func (h *Handler) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.flash(w, "error", "Invalid form data.")
		return
	}
	repoURL := strings.TrimSpace(r.FormValue("repo_url"))
	name := strings.TrimSpace(r.FormValue("name"))
	branch := strings.TrimSpace(r.FormValue("branch"))
	if branch == "" {
		branch = "main"
	}
	if repoURL == "" || name == "" {
		h.flash(w, "error", "Repository URL and name are required.")
		return
	}
	if err := validateGitHubRepo(repoURL, branch); err != nil {
		h.flash(w, "error", err.Error())
		return
	}
	now := time.Now()
	agent := &domain.Agent{
		ID:        uuid.New().String(),
		Name:      name,
		RepoURL:   repoURL,
		Branch:    branch,
		EnvVars:   "{}",
		Status:    domain.AgentStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.store.CreateAgent(r.Context(), agent); err != nil {
		h.flash(w, "error", "Failed to register agent: "+err.Error())
		return
	}
	dep, err := h.orch.Deploy(r.Context(), agent.ID, "")
	if err != nil {
		h.flash(w, "error", "Deploy failed: "+err.Error())
		return
	}
	// Redirect to the deployment detail page via HX-Redirect
	w.Header().Set("HX-Redirect", "/dashboard/deployments/"+dep.ID)
	w.Header().Set("Content-Type", "text/html")
	h.flash(w, "success", fmt.Sprintf("Deployment %s started — building image…", dep.ID[:8]))
}

func (h *Handler) handleStopDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.orch.StopDeployment(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<tr class="table-row" id="dep-row-%s"><td colspan="6" style="padding:12px 14px;font-size:12px;color:var(--red);">Stop failed: %s</td></tr>`,
			id, template.HTMLEscapeString(err.Error()))
		return
	}
	dep, err := h.store.GetDeployment(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	agents, _ := h.store.ListAgents(r.Context())
	row := deploymentRow{Deployment: dep}
	for _, a := range agents {
		if a.ID == dep.AgentID {
			row.AgentName = a.Name
			row.RepoShort = repoShortFn(a.RepoURL)
			break
		}
	}

	// If the request came from the detail page, return a meta-card update
	if strings.Contains(r.Header.Get("HX-Current-URL"), "/deployments/") {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<div class="meta-card" id="status-badge">
  <span class="meta-label">Status</span>
  <span class="badge badge--stopped"><span class="badge-dot"></span>stopped</span>
</div>`)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	h.execTmpl(w, "deployment-row", row)
}

func (h *Handler) handleDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteDeployment(r.Context(), id); err != nil {
		h.flash(w, "error", "Delete failed: "+err.Error())
		return
	}
	h.orch.Logs().Delete(id)
	// Return empty swap to remove the row; if from detail page the JS redirects
	w.WriteHeader(http.StatusOK)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (h *Handler) execTmpl(w http.ResponseWriter, name string, data any) {
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		h.log.Error("template exec", "template", name, "err", err)
	}
}

func (h *Handler) flash(w http.ResponseWriter, kind, msg string) {
	icons := map[string]string{
		"success": `<svg width="13" height="13" viewBox="0 0 13 13" fill="none"><path d="M2 6.5L5 9.5L11 3.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
		"error":   `<svg width="13" height="13" viewBox="0 0 13 13" fill="none"><path d="M6.5 2V7.5M6.5 10V10.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>`,
		"info":    `<svg width="13" height="13" viewBox="0 0 13 13" fill="none"><circle cx="6.5" cy="6.5" r="5" stroke="currentColor" stroke-width="1.3"/><path d="M6.5 6V9.5M6.5 4V4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>`,
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="flash flash--%s">%s<span>%s</span></div>`,
		kind, icons[kind], template.HTMLEscapeString(msg))
}

// ── GitHub validation ─────────────────────────────────────────────────────────

func validateGitHubRepo(repoURL, branch string) error {
	clean := strings.TrimPrefix(repoURL, "https://github.com/")
	clean = strings.TrimPrefix(clean, "http://github.com/")
	clean = strings.TrimSuffix(clean, ".git")
	parts := strings.SplitN(clean, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid GitHub URL — expected https://github.com/owner/repo")
	}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents?ref=%s", parts[0], parts[1], branch)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return fmt.Errorf("repository not found or is private")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var contents []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&contents); err != nil {
		return fmt.Errorf("could not parse GitHub response")
	}
	hasEntry, hasReqs := false, false
	for _, f := range contents {
		if f.Type != "file" {
			continue
		}
		if strings.HasSuffix(f.Name, ".py") {
			hasEntry = true
		}
		if f.Name == "requirements.txt" {
			hasReqs = true
		}
	}
	if !hasEntry {
		return fmt.Errorf("repo is missing a .py entry point at the root")
	}
	if !hasReqs {
		return fmt.Errorf("repo is missing requirements.txt at the root")
	}
	return nil
}

// ── Wizard ────────────────────────────────────────────────────────────────────

func (h *Handler) handleWizardPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "Wizard", "wizard", "page-wizard", nil, 0)
}

func (h *Handler) handleWizardSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fmt.Fprintf(w, `<div class="flash flash--error">Invalid form data.</div>`)
		return
	}

	payload := map[string]any{
		"ip":       r.FormValue("ip"),
		"username": r.FormValue("username"),
		"password": r.FormValue("password"),
		"port":     r.FormValue("port"),
		"git":      "https://github.com/Samsyet/identity-agent",
		"pyfile":   "agent.py",
		"workerid": "worker-67",
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post("http://142.93.222.37:8000/api/bootstrap", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(w, `<div class="flash flash--error">Could not reach hive-pollinate: %s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != http.StatusOK {
		detail, _ := result["detail"].(string)
		fmt.Fprintf(w, `<div class="flash flash--error">Bootstrap failed: %s</div>`, template.HTMLEscapeString(detail))
		return
	}

	stdout, _ := result["stdout"].(string)
	stderr, _ := result["stderr"].(string)

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="flash flash--success">Node bootstrapped successfully.</div>`)
	if stdout != "" {
		fmt.Fprintf(w, `<div class="card" style="margin-top:12px;"><pre class="mono text-xs" style="padding:16px;white-space:pre-wrap;overflow:auto;max-height:300px;">%s</pre></div>`, template.HTMLEscapeString(stdout))
	}
	if stderr != "" {
		fmt.Fprintf(w, `<div class="card" style="margin-top:8px;border-color:var(--red-muted);"><pre class="mono text-xs" style="padding:16px;white-space:pre-wrap;overflow:auto;max-height:200px;color:var(--red);">%s</pre></div>`, template.HTMLEscapeString(stderr))
	}
}

// ensure logbuf is referenced
var _ *logbuf.Registry

// ── Comms page ────────────────────────────────────────────────────────────────

type commsData struct {
	Logs []*domain.CommLog
}

func (h *Handler) handleCommsPage(w http.ResponseWriter, r *http.Request) {
	logs := h.comms.GetCommLogs()
	// Reverse so newest is first
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}
	h.renderPage(w, "Communications", "comms", "page-comms", commsData{Logs: logs}, 0)
}

func (h *Handler) handlePartialCommLogs(w http.ResponseWriter, r *http.Request) {
	logs := h.comms.GetCommLogs()
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}
	w.Header().Set("Content-Type", "text/html")
	h.execTmpl(w, "comms-table", commsData{Logs: logs})
}
