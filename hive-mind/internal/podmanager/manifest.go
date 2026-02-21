package podmanager

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildPodSpec returns a Kubernetes Pod object for a Hive agent deployment.
// Phase 1: single agent container only. Sidecar added in Phase 4.
func buildPodSpec(opts SpawnOptions) *corev1.Pod {
	env := []corev1.EnvVar{
		{Name: "HIVE_DEPLOYMENT_ID", Value: opts.DeploymentID},
		{Name: "HIVE_AGENT_ID", Value: opts.AgentID},
		{Name: "HIVE_POD_NAME", Value: opts.PodName},
	}
	for k, v := range opts.EnvVars {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.PodName,
			Namespace: opts.Namespace,
			Labels: map[string]string{
				"app":           "hive-agent",
				"hive-agent-id": opts.AgentID,
				"hive-deploy":   opts.DeploymentID,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:            "agent",
					Image:           opts.ImageRef,
					ImagePullPolicy: corev1.PullAlways,
					Env:             env,
				},
			},
		},
	}
}
