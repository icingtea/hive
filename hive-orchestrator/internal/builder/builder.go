package builder

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/template"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LineWriter is anything that can receive a log line (e.g. logbuf.Buffer).
type LineWriter interface {
	Write(line string)
}

//go:embed templates/python/Dockerfile.tmpl
var templatesFS embed.FS

const (
	kanikoImage     = "gcr.io/kaniko-project/executor:latest"
	buildNamespace  = "hive-system"
	buildPollInterval = 5 * time.Second
	buildTimeout    = 10 * time.Minute
)

// Builder builds agent images using Kaniko pods inside k8s.
// No Docker daemon required — works identically locally (kind) and in prod.
type Builder struct {
	k8s         kubernetes.Interface
	registryURL string
	log         *slog.Logger
}

func NewBuilder(k8s kubernetes.Interface, registryURL string, log *slog.Logger) *Builder {
	return &Builder{k8s: k8s, registryURL: registryURL, log: log}
}

// BuildAndPushWithLog is like BuildAndPush but streams build log lines into lw.
func (b *Builder) BuildAndPushWithLog(ctx context.Context, repoURL, branch, agentID, commitSHA string, lw LineWriter) (string, error) {
	return b.buildAndPush(ctx, repoURL, branch, agentID, commitSHA, lw)
}

// BuildAndPush spawns a Kaniko pod that clones the repo, builds the image,
// and pushes it to the registry. Blocks until the build completes or fails.
// Returns the full image reference.
func (b *Builder) BuildAndPush(ctx context.Context, repoURL, branch, agentID, commitSHA string) (string, error) {
	return b.buildAndPush(ctx, repoURL, branch, agentID, commitSHA, nil)
}

func (b *Builder) buildAndPush(ctx context.Context, repoURL, branch, agentID, commitSHA string, lw LineWriter) (string, error) {
	tag := commitSHA
	if len(tag) > 12 {
		tag = tag[:12]
	}
	if tag == "" {
		tag = "latest"
	}
	imageRef := fmt.Sprintf("%s/hive-agent-%s:%s", b.registryURL, agentID[:8], tag)

	dockerfile, err := b.renderDockerfile()
	if err != nil {
		return "", err
	}

	podName := fmt.Sprintf("hive-build-%s", agentID[:8])

	// Store the Dockerfile as a ConfigMap so the Kaniko pod can access it.
	if err := b.ensureNamespace(ctx); err != nil {
		return "", err
	}
	if err := b.createDockerfileConfigMap(ctx, podName, dockerfile); err != nil {
		return "", err
	}
	defer b.deleteConfigMap(ctx, podName)

	pod := b.buildPodSpec(podName, repoURL, branch, imageRef)
	if err := b.runBuildPod(ctx, pod, imageRef, lw); err != nil {
		return "", err
	}
	return imageRef, nil
}

func (b *Builder) renderDockerfile() (string, error) {
	raw, err := templatesFS.ReadFile("templates/python/Dockerfile.tmpl")
	if err != nil {
		return "", fmt.Errorf("read dockerfile template: %w", err)
	}
	tmpl, err := template.New("dockerfile").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse dockerfile template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("render dockerfile: %w", err)
	}
	return buf.String(), nil
}

func (b *Builder) ensureNamespace(ctx context.Context) error {
	_, err := b.k8s.CoreV1().Namespaces().Get(ctx, buildNamespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	_, err = b.k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: buildNamespace},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create build namespace: %w", err)
	}
	return nil
}

func (b *Builder) createDockerfileConfigMap(ctx context.Context, name, dockerfile string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: buildNamespace,
		},
		Data: map[string]string{
			"Dockerfile": dockerfile,
		},
	}
	// Delete if already exists (retry scenario)
	_ = b.k8s.CoreV1().ConfigMaps(buildNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	_, err := b.k8s.CoreV1().ConfigMaps(buildNamespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create dockerfile configmap: %w", err)
	}
	return nil
}

func (b *Builder) deleteConfigMap(ctx context.Context, name string) {
	_ = b.k8s.CoreV1().ConfigMaps(buildNamespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// buildPodSpec returns a Kaniko pod that:
//  1. init container: git clone the repo into /workspace
//  2. main container: kaniko reads /workspace, builds, pushes
//  3. Dockerfile injected via ConfigMap into /workspace
func (b *Builder) buildPodSpec(podName, repoURL, branch, imageRef string) *corev1.Pod {
	registryURL := b.registryURL
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: buildNamespace,
			Labels:    map[string]string{"app": "hive-builder"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			InitContainers: []corev1.Container{
				{
					Name:  "git-clone",
					Image: "alpine/git:latest",
					Command: []string{
						"sh", "-c",
						fmt.Sprintf("git clone --depth 1 --branch %s %s /workspace/repo", branch, repoURL),
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "kaniko",
					Image: kanikoImage,
					Args: []string{
						"--context=/workspace/repo",
						"--dockerfile=/workspace/Dockerfile",
						fmt.Sprintf("--destination=%s", imageRef),
						fmt.Sprintf("--insecure-registry=%s", registryURL),
						"--insecure",
						"--skip-tls-verify",
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
						{Name: "dockerfile", MountPath: "/workspace/Dockerfile", SubPath: "Dockerfile"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "workspace",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "dockerfile",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: podName},
						},
					},
				},
			},
		},
	}
}

// runBuildPod creates the build pod and waits for it to succeed or fail.
// If lw is non-nil, Kaniko log lines are streamed into it during the poll loop.
func (b *Builder) runBuildPod(ctx context.Context, pod *corev1.Pod, imageRef string, lw LineWriter) error {
	_ = b.k8s.CoreV1().Pods(buildNamespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	time.Sleep(time.Second)

	emit := func(line string) {
		if lw != nil {
			lw.Write(line)
		}
	}

	b.log.Info("starting build pod", "pod", pod.Name, "image", imageRef)
	emit("  [k8s] creating build pod " + pod.Name)
	if _, err := b.k8s.CoreV1().Pods(buildNamespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create build pod: %w", err)
	}

	var logOffset int64 // tracks how many bytes of kaniko logs we've already streamed
	deadline := time.Now().Add(buildTimeout)
	var buildErr error

	for {
		if time.Now().After(deadline) {
			buildErr = fmt.Errorf("build timed out after %s", buildTimeout)
			break
		}

		select {
		case <-ctx.Done():
			buildErr = ctx.Err()
			goto cleanup
		case <-time.After(buildPollInterval):
		}

		p, err := b.k8s.CoreV1().Pods(buildNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			buildErr = fmt.Errorf("get build pod: %w", err)
			break
		}

		// Stream any new Kaniko log lines into lw
		if lw != nil {
			logOffset = b.streamNewLogs(pod.Name, logOffset, lw)
		}

		switch p.Status.Phase {
		case corev1.PodSucceeded:
			b.log.Info("build succeeded", "image", imageRef)
			b.k8s.CoreV1().Pods(buildNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
			return nil
		case corev1.PodFailed:
			b.streamPodLogs(pod.Name)
			buildErr = fmt.Errorf("build pod failed: %s", buildPodMessage(p))
			break
		default:
			if msg, bad := earlyFailureMessage(p); bad {
				b.streamPodLogs(pod.Name)
				buildErr = fmt.Errorf("build pod failed: %s", msg)
				break
			}
			emit(fmt.Sprintf("  [kaniko] phase=%s", p.Status.Phase))
			b.log.Info("build in progress", "phase", p.Status.Phase, "image", imageRef)
			continue
		}
		break
	}

cleanup:
	b.k8s.CoreV1().Pods(buildNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	return buildErr
}

// streamNewLogs fetches kaniko container logs from sinceBytes offset and writes
// each new line into lw. Returns the new offset.
func (b *Builder) streamNewLogs(podName string, sinceBytes int64, lw LineWriter) int64 {
	req := b.k8s.CoreV1().Pods(buildNamespace).GetLogs(podName, &corev1.PodLogOptions{
		Container:  "kaniko",
		LimitBytes: func() *int64 { n := int64(128 * 1024); return &n }(),
	})
	stream, err := req.Stream(context.Background())
	if err != nil {
		return sinceBytes
	}
	defer stream.Close()

	all, err := io.ReadAll(stream)
	if err != nil || int64(len(all)) <= sinceBytes {
		return sinceBytes
	}
	newData := all[sinceBytes:]
	scanner := bufio.NewScanner(bytes.NewReader(newData))
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lw.Write("  [kaniko] " + line)
		}
	}
	return int64(len(all))
}

// buildPodMessage collects failure details from all container + init container statuses.
func buildPodMessage(p *corev1.Pod) string {
	all := append(p.Status.InitContainerStatuses, p.Status.ContainerStatuses...)
	for _, cs := range all {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return fmt.Sprintf("container %s exited %d: %s",
				cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Message)
		}
	}
	if p.Status.Message != "" {
		return p.Status.Message
	}
	return "unknown failure"
}

// earlyFailureMessage detects ErrImagePull / ImagePullBackOff / CreateContainerConfigError
// in Waiting states before the pod phase transitions to Failed.
func earlyFailureMessage(p *corev1.Pod) (string, bool) {
	bad := map[string]bool{
		"ErrImagePull":              true,
		"ImagePullBackOff":          true,
		"InvalidImageName":          true,
		"CreateContainerConfigError": true,
	}
	all := append(p.Status.InitContainerStatuses, p.Status.ContainerStatuses...)
	for _, cs := range all {
		if cs.State.Waiting != nil && bad[cs.State.Waiting.Reason] {
			return fmt.Sprintf("container %s: %s: %s",
				cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message), true
		}
	}
	return "", false
}

// streamPodLogs dumps logs from all containers in the build pod to the orchestrator's stdout.
func (b *Builder) streamPodLogs(podName string) {
	for _, container := range []string{"git-clone", "kaniko"} {
		req := b.k8s.CoreV1().Pods(buildNamespace).GetLogs(podName, &corev1.PodLogOptions{
			Container: container,
		})
		stream, err := req.Stream(context.Background())
		if err != nil {
			b.log.Warn("could not get logs", "pod", podName, "container", container, "err", err)
			continue
		}
		b.log.Info("=== build logs ===", "pod", podName, "container", container)
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		stream.Close()
	}
}
