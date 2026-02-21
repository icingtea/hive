package podmanager

import (
	"context"
	"log/slog"
)

// NoopPodManager is a no-op implementation for local dev without a k8s cluster.
type NoopPodManager struct {
	log *slog.Logger
}

func NewNoopPodManager(log *slog.Logger) *NoopPodManager {
	return &NoopPodManager{log: log}
}

func (n *NoopPodManager) Spawn(ctx context.Context, opts SpawnOptions) error {
	n.log.Info("[noop] spawn pod", "pod", opts.PodName, "image", opts.ImageRef)
	return nil
}

func (n *NoopPodManager) Terminate(ctx context.Context, podName, namespace string) error {
	n.log.Info("[noop] terminate pod", "pod", podName)
	return nil
}

func (n *NoopPodManager) List(ctx context.Context, namespace string) ([]*PodStatus, error) {
	return []*PodStatus{}, nil
}

func (n *NoopPodManager) GetStatus(ctx context.Context, podName, namespace string) (*PodStatus, error) {
	return &PodStatus{PodName: podName, Phase: "Unknown"}, nil
}

func (n *NoopPodManager) WatchEvents(ctx context.Context, namespace string) (<-chan PodEvent, error) {
	ch := make(chan PodEvent)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
