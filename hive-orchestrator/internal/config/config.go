package config

import (
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all orchestrator configuration.
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
	Registry  RegistryConfig  `mapstructure:"registry"`
	GitHub    GitHubConfig    `mapstructure:"github"`
	Hive      HiveConfig      `mapstructure:"hive"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

type KubernetesConfig struct {
	Kubeconfig string `mapstructure:"kubeconfig"`
	Namespace  string `mapstructure:"namespace"`
	InCluster  bool   `mapstructure:"in_cluster"`
}

type RegistryConfig struct {
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

type GitHubConfig struct {
	Token         string `mapstructure:"token"`
	WebhookSecret string `mapstructure:"webhook_secret"`
}

type HiveConfig struct {
	WSHost          string `mapstructure:"ws_host"`           // ws://orchestrator.hive.svc.cluster.local
	OrchestratorURL string `mapstructure:"orchestrator_url"`  // http://orchestrator.hive.svc.cluster.local:8080
	StagingDir      string `mapstructure:"staging_dir"`       // /tmp/hive-staging
}

// Load reads configuration from hive.yaml and environment variables.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("database.path", "./hive.db")
	v.SetDefault("kubernetes.namespace", "hive-agents")
	v.SetDefault("kubernetes.in_cluster", false)
	v.SetDefault("registry.url", "localhost:5000")
	v.SetDefault("hive.ws_host", "ws://localhost:8080")
	v.SetDefault("hive.orchestrator_url", "http://localhost:8080")
	v.SetDefault("hive.staging_dir", "/tmp/hive-staging")

	// Config file
	v.SetConfigName("hive")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("$HOME/.hive")
	v.AddConfigPath("/etc/hive")
	_ = v.ReadInConfig() // OK if not found

	// Env vars: HIVE_SERVER_PORT=9090 overrides server.port
	v.SetEnvPrefix("HIVE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Explicit bindings needed for nested keys with Unmarshal
	_ = v.BindEnv("server.host", "HIVE_SERVER_HOST")
	_ = v.BindEnv("server.port", "HIVE_SERVER_PORT")
	_ = v.BindEnv("database.path", "HIVE_DATABASE_PATH")
	_ = v.BindEnv("kubernetes.kubeconfig", "HIVE_KUBERNETES_KUBECONFIG")
	_ = v.BindEnv("kubernetes.namespace", "HIVE_KUBERNETES_NAMESPACE")
	_ = v.BindEnv("kubernetes.in_cluster", "HIVE_KUBERNETES_IN_CLUSTER")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// Expand ~ in paths
	cfg.Kubernetes.Kubeconfig = expandHome(cfg.Kubernetes.Kubeconfig)
	cfg.Database.Path = expandHome(cfg.Database.Path)
	cfg.Hive.StagingDir = expandHome(cfg.Hive.StagingDir)

	return &cfg, nil
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path // return as-is rather than silently produce a wrong path
	}
	return home + path[1:]
}
