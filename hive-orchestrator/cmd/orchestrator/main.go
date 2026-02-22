package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"hive-mind/internal/api"
	"hive-mind/internal/builder"
	"hive-mind/internal/config"
	"hive-mind/internal/dashboard/logbuf"
	"hive-mind/internal/orchestrator"
	"hive-mind/internal/podmanager"
	"hive-mind/internal/registry"
)

func ensureNamespaces(ctx context.Context, k8s kubernetes.Interface, log *slog.Logger, namespaces ...string) {
	for _, ns := range namespaces {
		_, err := k8s.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err == nil {
			continue
		}
		if !errors.IsNotFound(err) {
			log.Warn("could not check namespace", "namespace", ns, "err", err)
			continue
		}
		_, err = k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}, metav1.CreateOptions{})
		if err != nil {
			log.Warn("could not create namespace", "namespace", ns, "err", err)
		} else {
			log.Info("created namespace", "namespace", ns)
		}
	}
}

func buildK8sClient(cfg *config.Config) (kubernetes.Interface, error) {
	var restCfg *rest.Config
	var err error
	if cfg.Kubernetes.InCluster {
		restCfg, err = rest.InClusterConfig()
	} else {
		restCfg, err = clientcmd.BuildConfigFromFlags("", cfg.Kubernetes.Kubeconfig)
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restCfg)
}

func newPodManager(cfg *config.Config, log *slog.Logger) podmanager.PodManager {
	pm, err := podmanager.NewKubernetesPodManager(cfg.Kubernetes.Kubeconfig, cfg.Kubernetes.InCluster, log)
	if err != nil {
		log.Warn("k8s unavailable, using noop pod manager (dev mode)", "err", err)
		return podmanager.NewNoopPodManager(log)
	}
	return pm
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	store, err := registry.NewSQLiteStore(cfg.Database.Path)
	if err != nil {
		log.Error("failed to open database", "err", err)
		os.Exit(1)
	}

	k8sClient, err := buildK8sClient(cfg)
	if err != nil {
		log.Error("failed to build k8s client", "err", err)
		os.Exit(1)
	}

	nsCtx, nsCancel := context.WithTimeout(context.Background(), 10*time.Second)
	ensureNamespaces(nsCtx, k8sClient, log, cfg.Kubernetes.Namespace, "hive-system")
	nsCancel()

	podMgr := newPodManager(cfg, log)
	bldr := builder.NewBuilder(k8sClient, cfg.Registry.URL, log)
	logs := logbuf.NewRegistry()
	orch := orchestrator.New(cfg, store, podMgr, bldr, logs, log)

	srv := api.NewServer(api.ServerConfig{
		Host: cfg.Server.Host,
		Port: cfg.Server.Port,
	}, store, podMgr, orch, log)

	// Wire the route registrar after both orch and srv are constructed.
	orch.SetRegistrar(srv)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Error("server error", "err", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", "err", err)
	}
	store.Close()
}
