package domain

import "time"

// Heartbeat is the telemetry payload pushed by the Zig sidecar every 5s.
type Heartbeat struct {
	DeploymentID      string    `json:"deployment_id"`
	AgentID           string    `json:"agent_id"`
	PID               *uint32   `json:"pid"`
	Uptime            *string   `json:"uptime"`
	Meminfo           *string   `json:"meminfo"`
	Kernel            *string   `json:"kernel"`
	MemoryBytes       *uint64   `json:"memory_bytes"`
	MemoryLimitBytes  *uint64   `json:"memory_limit_bytes"`
	ReceivedAt        time.Time `json:"received_at"`
}

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
