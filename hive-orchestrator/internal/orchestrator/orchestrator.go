package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
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

// Orchestrator drives the deploy lifecycle: build image (via Kaniko) → spawn pod.
type Orchestrator struct {
	cfg    *config.Config
	store  registry.Store
	podMgr podmanager.PodManager
	bldr   *builder.Builder
	logs   *logbuf.Registry
	log    *slog.Logger
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

	if err := o.podMgr.Spawn(ctx, podmanager.SpawnOptions{
		DeploymentID:    dep.ID,
		AgentID:         agent.ID,
		PodName:         podName,
		Namespace:       dep.Namespace,
		ImageRef:        imageRef,
		WSHost:          o.cfg.Hive.WSHost,
		OrchestratorURL: o.cfg.Hive.OrchestratorURL,
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
