package registry

import (
	"context"

	"hive-mind/internal/domain"
)

// Store is the persistence interface for Phase 1: agents and deployments.
type Store interface {
	CreateAgent(ctx context.Context, a *domain.Agent) error
	GetAgent(ctx context.Context, id string) (*domain.Agent, error)
	ListAgents(ctx context.Context) ([]*domain.Agent, error)
	UpdateAgent(ctx context.Context, a *domain.Agent) error
	DeleteAgent(ctx context.Context, id string) error

	CreateDeployment(ctx context.Context, d *domain.Deployment) error
	GetDeployment(ctx context.Context, id string) (*domain.Deployment, error)
	ListDeployments(ctx context.Context, agentID string) ([]*domain.Deployment, error)
	ListAllDeployments(ctx context.Context) ([]*domain.Deployment, error)
	UpdateDeployment(ctx context.Context, d *domain.Deployment) error
	DeleteDeployment(ctx context.Context, id string) error

	Close() error
}
