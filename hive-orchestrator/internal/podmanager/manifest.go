package podmanager

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const SidecarImage = "hive-registry:5000/hive-sidecar:latest"

// buildPodSpec returns a Kubernetes Pod object for a Hive agent deployment.
func buildPodSpec(opts SpawnOptions) *corev1.Pod {
	env := []corev1.EnvVar{
		{Name: "HIVE_DEPLOYMENT_ID", Value: opts.DeploymentID},
		{Name: "HIVE_AGENT_ID", Value: opts.AgentID},
		{Name: "HIVE_POD_NAME", Value: opts.PodName},
		{Name: "HIVE_POD_PORT", Value: "8080"},
		{Name: "HIVE_MIND_ADDRESS", Value: opts.OrchestratorURL},
		{Name: "HIVE_MIND_COMMUNICATION_ENDPOINT", Value: "api/v1/communicate"},
	}
	for k, v := range opts.EnvVars {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	shareProcessNamespace := true

	sidecarEnv := []corev1.EnvVar{
		{Name: "HIVE_DEPLOYMENT_ID", Value: opts.DeploymentID},
		{Name: "HIVE_AGENT_ID", Value: opts.AgentID},
		{Name: "HIVE_ORCHESTRATOR_URL", Value: opts.OrchestratorURL},
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
			RestartPolicy:         corev1.RestartPolicyNever,
			ShareProcessNamespace: &shareProcessNamespace,
			// Hard-enforce even spread across all nodes.
			// MinDomains ensures nodes with 0 pods are counted, so the scheduler
			// fills every machine before doubling up on any one node.
			TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
				{
					MaxSkew:            1,
					TopologyKey:        "kubernetes.io/hostname",
					WhenUnsatisfiable:  corev1.DoNotSchedule,
					MinDomains:         &opts.NodeCount,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "hive-agent"},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:            "agent",
					Image:           opts.ImageRef,
					ImagePullPolicy: corev1.PullAlways,
					Env:             env,
				},
				{
					Name:            "sidecar",
					Image:           SidecarImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env:             sidecarEnv,
				},
			},
		},
	}
}
