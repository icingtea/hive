package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/google/uuid"

	"hive-mind/internal/builder"
	"hive-mind/internal/config"
	"hive-mind/internal/dashboard/logbuf"
	"hive-mind/internal/domain"
	githubpkg "hive-mind/internal/github"
	"hive-mind/internal/podmanager"
	"hive-mind/internal/registry"
)

// RouteRegistrar is satisfied by api.Server — lets orchestrator register pod routes
// without a circular import.
type RouteRegistrar interface {
	RegisterAgentRoute(name, podIP, port, podName, namespace string)
}

// Orchestrator drives the deploy lifecycle: build image (via Kaniko) → spawn pod.
type Orchestrator struct {
	cfg       *config.Config
	store     registry.Store
	podMgr    podmanager.PodManager
	bldr      *builder.Builder
	logs      *logbuf.Registry
	log       *slog.Logger
	registrar RouteRegistrar // optional; set after construction to avoid circular deps
}

func New(
	cfg *config.Config,
	store registry.Store,
	podMgr podmanager.PodManager,
	bldr *builder.Builder,
	logs *logbuf.Registry,
	log *slog.Logger,
) *Orchestrator {
	return &Orchestrator{
		cfg:    cfg,
		store:  store,
		podMgr: podMgr,
		bldr:   bldr,
		logs:   logs,
		log:    log,
	}
}

// Logs returns the log registry so other packages (dashboard) can read from it.
func (o *Orchestrator) Logs() *logbuf.Registry { return o.logs }

// SetRegistrar wires in the route registrar after construction (avoids circular import).
func (o *Orchestrator) SetRegistrar(r RouteRegistrar) { o.registrar = r }

// Deploy creates a deployment record and kicks off the build+spawn pipeline
// asynchronously. Returns the deployment immediately so the caller can 202.
func (o *Orchestrator) Deploy(ctx context.Context, agentID, commitSHA string) (*domain.Deployment, error) {
	agent, err := o.store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}

	now := time.Now()
	dep := &domain.Deployment{
		ID:        uuid.New().String(),
		AgentID:   agentID,
		CommitSHA: commitSHA,
		Namespace: o.cfg.Kubernetes.Namespace,
		Status:    domain.DeploymentStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := o.store.CreateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	go o.runDeploy(context.Background(), agent, dep)
	return dep, nil
}

func (o *Orchestrator) runDeploy(ctx context.Context, agent *domain.Agent, dep *domain.Deployment) {
	log := o.log.With("deployment_id", dep.ID, "agent_id", agent.ID)
	buf := o.logs.Get(dep.ID)

	emit := func(line string) {
		buf.Write(line)
		log.Info(line)
	}

	dep.Status = domain.DeploymentStatusBuilding
	if err := o.store.UpdateDeployment(ctx, dep); err != nil {
		log.Error("failed to update deployment status", "err", err)
	}

	emit("» Starting deployment " + dep.ID[:8])
	emit("» Cloning repository " + agent.RepoURL + " @ " + agent.Branch)

	cloneURL := githubpkg.NormaliseURL(agent.RepoURL)
	emit("» Building image via Kaniko…")

	imageRef, err := o.bldr.BuildAndPushWithLog(ctx, cloneURL, agent.Branch, agent.ID, dep.CommitSHA, buf)
	if err != nil {
		emit("✗ Build failed: " + err.Error())
		o.fail(ctx, dep, fmt.Sprintf("build: %s", err))
		buf.Close()
		return
	}
	dep.ImageRef = imageRef
	emit("✓ Image built: " + imageRef)

	podName := fmt.Sprintf("hive-%s-%s", agent.ID[:8], dep.ID[:8])
	dep.PodName = podName
	emit("» Spawning pod " + podName + "…")

	// Fetch node count for topology spread — fall back to 1 if unsupported.
	var nodeCount int32 = 1
	if nc, ok := o.podMgr.(podmanager.NodeCounter); ok {
		if n, err := nc.CountNodes(ctx); err == nil {
			nodeCount = n
		}
	}

	if err := o.podMgr.Spawn(ctx, podmanager.SpawnOptions{
		DeploymentID:    dep.ID,
		AgentID:         agent.ID,
		PodName:         podName,
		Namespace:       dep.Namespace,
		ImageRef:        imageRef,
		WSHost:          o.cfg.Hive.WSHost,
		OrchestratorURL: o.cfg.Hive.OrchestratorURL,
		NodeCount:       nodeCount,
	}); err != nil {
		emit("✗ Spawn failed: " + err.Error())
		o.fail(ctx, dep, fmt.Sprintf("spawn: %s", err))
		buf.Close()
		return
	}

	now := time.Now()
	dep.StartedAt = &now
	dep.Status = domain.DeploymentStatusRunning
	if err := o.store.UpdateDeployment(ctx, dep); err != nil {
		log.Error("failed to update deployment to running", "err", err)
	}
	emit("✓ Deployment running — pod " + podName)
	buf.Close()
	log.Info("deployment running", "pod", podName, "image", imageRef)

	// Probe the pod's /id endpoint to register its self-declared agent name.
	if o.registrar != nil {
		go o.probeAgentID(podName, dep.Namespace)
	}
}

// probeAgentID polls the pod's /id endpoint via kubectl exec until the SDK is up,
// then registers the route.
func (o *Orchestrator) probeAgentID(podName, namespace string) {
	const podPort = "8080"
	const maxAttempts = 24 // 2 minutes total (24 × 5s)

	for i := 0; i < maxAttempts; i++ {
		time.Sleep(5 * time.Second)

		status, err := o.podMgr.GetStatus(context.Background(), podName, namespace)
		if err != nil || status.PodIP == "" {
			continue
		}

		out, err := exec.Command("kubectl", "exec", podName,
			"-n", namespace,
			"-c", "agent",
			"--", "python3", "-c",
			fmt.Sprintf("import urllib.request,sys; r=urllib.request.urlopen('http://localhost:%s/id',timeout=3); sys.stdout.write(r.read().decode())", podPort),
		).Output()
		if err != nil {
			continue
		}

		var body struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(out, &body); err != nil || body.AgentID == "" {
			continue
		}

		o.registrar.RegisterAgentRoute(body.AgentID, status.PodIP, podPort, podName, namespace)
		o.log.Info("registered agent route", "agent_name", body.AgentID, "pod", podName, "ip", status.PodIP)
		return
	}
	o.log.Warn("failed to probe agent /id after max attempts", "pod", podName)
}

func (o *Orchestrator) fail(ctx context.Context, dep *domain.Deployment, reason string) {
	o.log.Error("deployment failed", "deployment_id", dep.ID, "reason", reason)
	now := time.Now()
	dep.Status = domain.DeploymentStatusFailed
	dep.ErrorMessage = reason
	dep.FinishedAt = &now
	if err := o.store.UpdateDeployment(ctx, dep); err != nil {
		o.log.Error("failed to persist deployment failure", "err", err)
	}
}

// StopDeployment terminates a running deployment's pod.
func (o *Orchestrator) StopDeployment(ctx context.Context, deploymentID string) error {
	dep, err := o.store.GetDeployment(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}
	if dep.PodName != "" {
		if err := o.podMgr.Terminate(ctx, dep.PodName, dep.Namespace); err != nil {
			return fmt.Errorf("terminate pod: %w", err)
		}
	}
	now := time.Now()
	dep.Status = domain.DeploymentStatusStopped
	dep.FinishedAt = &now
	return o.store.UpdateDeployment(ctx, dep)
}
