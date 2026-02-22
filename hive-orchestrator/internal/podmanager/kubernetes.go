package podmanager

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesPodManager implements PodManager using direct client-go calls.
type KubernetesPodManager struct {
	client kubernetes.Interface
	log    *slog.Logger
}

// NewKubernetesPodManager creates a pod manager that connects to k8s via kubeconfig or in-cluster config.
func NewKubernetesPodManager(kubeconfigPath string, inCluster bool, log *slog.Logger) (*KubernetesPodManager, error) {
	var cfg *rest.Config
	var err error

	if inCluster {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	return &KubernetesPodManager{client: client, log: log}, nil
}

func (m *KubernetesPodManager) Spawn(ctx context.Context, opts SpawnOptions) error {
	pod := buildPodSpec(opts)
	_, err := m.client.CoreV1().Pods(opts.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod %s: %w", opts.PodName, err)
	}
	m.log.Info("spawned pod", "pod", opts.PodName, "namespace", opts.Namespace, "image", opts.ImageRef)
	return nil
}

func (m *KubernetesPodManager) Terminate(ctx context.Context, podName, namespace string) error {
	grace := int64(30)
	err := m.client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

func (m *KubernetesPodManager) List(ctx context.Context, namespace string) ([]*PodStatus, error) {
	list, err := m.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=hive-agent",
	})
	if err != nil {
		return nil, err
	}
	var out []*PodStatus
	for _, p := range list.Items {
		out = append(out, podToStatus(&p))
	}
	return out, nil
}

func (m *KubernetesPodManager) GetStatus(ctx context.Context, podName, namespace string) (*PodStatus, error) {
	p, err := m.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return podToStatus(p), nil
}

func (m *KubernetesPodManager) WatchEvents(ctx context.Context, namespace string) (<-chan PodEvent, error) {
	watcher, err := m.client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: "app=hive-agent",
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan PodEvent, 64)
	go func() {
		defer close(ch)
		defer watcher.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-watcher.ResultChan():
				if !ok {
					return
				}
				pod, ok := evt.Object.(*corev1.Pod)
				if !ok {
					continue
				}
				evtType := ""
				switch evt.Type {
				case watch.Added:
					evtType = "ADDED"
				case watch.Modified:
					evtType = "MODIFIED"
				case watch.Deleted:
					evtType = "DELETED"
				default:
					continue
				}
				ch <- PodEvent{
					Type:      evtType,
					PodName:   pod.Name,
					Namespace: pod.Namespace,
					Phase:     string(pod.Status.Phase),
					PodIP:     pod.Status.PodIP,
				}
			}
		}
	}()

	return ch, nil
}

func (m *KubernetesPodManager) CountNodes(ctx context.Context) (int32, error) {
	list, err := m.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	// Count only schedulable nodes
	var count int32
	for _, n := range list.Items {
		if !n.Spec.Unschedulable {
			count++
		}
	}
	if count == 0 {
		count = 1
	}
	return count, nil
}

func podToStatus(p *corev1.Pod) *PodStatus {
	msg := ""
	if len(p.Status.Conditions) > 0 {
		for _, c := range p.Status.Conditions {
			if c.Status != corev1.ConditionTrue {
				msg = c.Message
				break
			}
		}
	}
	return &PodStatus{
		PodName:   p.Name,
		Namespace: p.Namespace,
		Phase:     string(p.Status.Phase),
		PodIP:     p.Status.PodIP,
		NodeName:  p.Spec.NodeName,
		Message:   msg,
	}
}
