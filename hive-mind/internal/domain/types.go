package domain

import "time"

type AgentStatus string

const (
	AgentStatusActive   AgentStatus = "active"
	AgentStatusInactive AgentStatus = "inactive"
)

type DeploymentStatus string

const (
	DeploymentStatusPending  DeploymentStatus = "pending"
	DeploymentStatusBuilding DeploymentStatus = "building"
	DeploymentStatusRunning  DeploymentStatus = "running"
	DeploymentStatusStopped  DeploymentStatus = "stopped"
	DeploymentStatusFailed   DeploymentStatus = "failed"
)

// Agent is a user-registered GitHub repo.
type Agent struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	RepoURL   string      `json:"repo_url"`
	Branch    string      `json:"branch"`
	EnvVars   string      `json:"env_vars"` // JSON-encoded map[string]string
	Status    AgentStatus `json:"status"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// Deployment is one running instance of an agent at a specific commit SHA.
type Deployment struct {
	ID           string           `json:"id"`
	AgentID      string           `json:"agent_id"`
	CommitSHA    string           `json:"commit_sha"`
	ImageRef     string           `json:"image_ref"`
	PodName      string           `json:"pod_name"`
	Namespace    string           `json:"namespace"`
	Status       DeploymentStatus `json:"status"`
	ErrorMessage string           `json:"error_message,omitempty"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	FinishedAt   *time.Time       `json:"finished_at,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}
