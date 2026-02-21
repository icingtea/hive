package podmanager

import "context"

// SpawnOptions carries parameters for creating a new pod.
type SpawnOptions struct {
	DeploymentID string
	AgentID      string
	PodName      string
	Namespace    string
	ImageRef     string
	EnvVars      map[string]string
	WSHost       string
	OrchestratorURL string
}

// PodStatus is a lightweight status snapshot returned by GetStatus.
type PodStatus struct {
	PodName   string
	Namespace string
	Phase     string // Pending, Running, Succeeded, Failed, Unknown
	PodIP     string
	Message   string
}

// PodEvent is emitted by WatchEvents for real-time pod lifecycle changes.
type PodEvent struct {
	Type      string // ADDED, MODIFIED, DELETED
	PodName   string
	Namespace string
	Phase     string
	PodIP     string
}

// PodManager is the abstraction layer between the orchestrator and the Kubernetes runtime.
// Two implementations exist: direct client-go (hosted) and remote WebSocket (BYO cluster).
type PodManager interface {
	// Spawn creates a new pod for the given deployment.
	Spawn(ctx context.Context, opts SpawnOptions) error

	// Terminate gracefully stops a pod.
	Terminate(ctx context.Context, podName, namespace string) error

	// List returns all hive-managed pods in the given namespace.
	List(ctx context.Context, namespace string) ([]*PodStatus, error)

	// GetStatus returns the current status of a specific pod.
	GetStatus(ctx context.Context, podName, namespace string) (*PodStatus, error)

	// WatchEvents returns a channel of pod lifecycle events.
	WatchEvents(ctx context.Context, namespace string) (<-chan PodEvent, error)
}
