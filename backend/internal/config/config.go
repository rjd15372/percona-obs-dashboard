package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	OBSRoot    string
	OBS        OBSConfig
	MQ         MQConfig
	Poller     PollerConfig
	Store      StoreConfig
	Server     ServerConfig
	WorkerPool WorkerPoolConfig
	Telemetry  TelemetryConfig
	Unblocker  UnblockerConfig
	Idle       IdleConfig
}

type OBSConfig struct {
	Username            string
	Password            string
	BaseURL             string
	MinuteRequestBudget int
}

type MQConfig struct {
	URL string
}

type PollerConfig struct {
	Interval time.Duration
}

type StoreConfig struct {
	DBPath           string
	EventRetention   time.Duration
	MetricsRetention time.Duration
}

type ServerConfig struct {
	HTTPPort    int
	FrontendDir string
}

type WorkerPoolConfig struct {
	Size           int
	PollInterval   time.Duration
	QueueSize      int
	BackoffMax     time.Duration
	BatchThreshold int
}

type TelemetryConfig struct {
	Interval time.Duration
	Enabled  bool
}

type UnblockerConfig struct {
	Enabled   bool
	Threshold time.Duration
}

type IdleConfig struct {
	Enabled bool
	Linger  time.Duration
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetDefault("obs_root", "isv:percona")
	_ = v.BindEnv("obs_root", "OBS_ROOT")

	v.SetDefault("obs.base_url", "https://api.opensuse.org")
	v.SetDefault("obs.minute_request_budget", 60)
	v.SetDefault("mq.url", "amqps://opensuse:opensuse@rabbit.opensuse.org:5671/")
	v.SetDefault("poller.interval", "2m")
	v.SetDefault("store.db_path", "/data/obsboard.db")
	v.SetDefault("store.event_retention", "7d")
	v.SetDefault("store.metrics_retention", "30d")
	v.SetDefault("server.http_port", 4000)
	v.SetDefault("server.frontend_dir", "")
	v.SetDefault("worker_pool.size", 5)
	v.SetDefault("worker_pool.poll_interval", "30s")
	v.SetDefault("worker_pool.queue_size", 512)
	v.SetDefault("worker_pool.backoff_max", "5m")
	v.SetDefault("worker_pool.batch_threshold", 4)
	v.SetDefault("telemetry.interval", "60s")
	v.SetDefault("telemetry.enabled", false)
	v.SetDefault("unblocker.enabled", false)
	v.SetDefault("unblocker.threshold", "30m")
	v.SetDefault("idle.enabled", true)
	v.SetDefault("idle.linger", "5m")

	// Config file (optional)
	cfgFile := "config.yaml"
	if f := v.GetString("CONFIG_FILE"); f != "" {
		cfgFile = f
	}
	v.SetConfigFile(cfgFile)
	_ = v.ReadInConfig()

	// Env vars take priority
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	for _, pair := range [][]string{
		{"obs.username", "OBS_USERNAME"},
		{"obs.password", "OBS_PASSWORD"},
		{"obs.base_url", "OBS_BASE_URL"},
		{"obs.minute_request_budget", "OBS_MINUTE_REQUEST_BUDGET"},
		{"mq.url", "MQ_URL"},
		{"poller.interval", "POLL_INTERVAL"},
		{"store.db_path", "DB_PATH"},
		{"store.event_retention", "EVENT_RETENTION"},
		{"store.metrics_retention", "METRICS_RETENTION"},
		{"server.http_port", "HTTP_PORT"},
		{"server.frontend_dir", "FRONTEND_DIR"},
		{"worker_pool.size", "WORKER_POOL_SIZE"},
		{"worker_pool.poll_interval", "WORKER_POOL_POLL_INTERVAL"},
		{"worker_pool.queue_size", "WORKER_POOL_QUEUE_SIZE"},
		{"worker_pool.backoff_max", "WORKER_POOL_BACKOFF_MAX"},
		{"worker_pool.batch_threshold", "WORKER_POOL_BATCH_THRESHOLD"},
		{"telemetry.interval", "TELEMETRY_INTERVAL"},
		{"telemetry.enabled", "TELEMETRY_ENABLED"},
		{"unblocker.enabled", "UNBLOCKER_ENABLED"},
		{"unblocker.threshold", "UNBLOCKER_THRESHOLD"},
		{"idle.enabled", "IDLE_ENABLED"},
		{"idle.linger", "IDLE_LINGER"},
	} {
		_ = v.BindEnv(pair[0], pair[1])
	}

	pollInterval, err := time.ParseDuration(v.GetString("poller.interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid POLL_INTERVAL %q: %w", v.GetString("poller.interval"), err)
	}

	retention, err := parseRetention(v.GetString("store.event_retention"))
	if err != nil {
		return nil, fmt.Errorf("invalid EVENT_RETENTION %q: %w", v.GetString("store.event_retention"), err)
	}

	metricsRetention, err := parseRetention(v.GetString("store.metrics_retention"))
	if err != nil {
		return nil, fmt.Errorf("invalid METRICS_RETENTION %q: %w", v.GetString("store.metrics_retention"), err)
	}

	pollIntervalWP, err := time.ParseDuration(v.GetString("worker_pool.poll_interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid WORKER_POOL_POLL_INTERVAL %q: %w", v.GetString("worker_pool.poll_interval"), err)
	}

	backoffMax, err := time.ParseDuration(v.GetString("worker_pool.backoff_max"))
	if err != nil {
		return nil, fmt.Errorf("invalid WORKER_POOL_BACKOFF_MAX %q: %w", v.GetString("worker_pool.backoff_max"), err)
	}

	telemetryInterval, err := time.ParseDuration(v.GetString("telemetry.interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid TELEMETRY_INTERVAL %q: %w", v.GetString("telemetry.interval"), err)
	}

	unblockThreshold, err := time.ParseDuration(v.GetString("unblocker.threshold"))
	if err != nil {
		return nil, fmt.Errorf("invalid UNBLOCKER_THRESHOLD %q: %w", v.GetString("unblocker.threshold"), err)
	}

	idleLinger, err := time.ParseDuration(v.GetString("idle.linger"))
	if err != nil {
		return nil, fmt.Errorf("invalid IDLE_LINGER %q: %w", v.GetString("idle.linger"), err)
	}

	cfg := &Config{
		OBSRoot: v.GetString("obs_root"),
		OBS: OBSConfig{
			Username:            v.GetString("obs.username"),
			Password:            v.GetString("obs.password"),
			BaseURL:             strings.TrimRight(v.GetString("obs.base_url"), "/"),
			MinuteRequestBudget: v.GetInt("obs.minute_request_budget"),
		},
		MQ:     MQConfig{URL: v.GetString("mq.url")},
		Poller: PollerConfig{Interval: pollInterval},
		Store: StoreConfig{
			DBPath:           v.GetString("store.db_path"),
			EventRetention:   retention,
			MetricsRetention: metricsRetention,
		},
		Server: ServerConfig{
			HTTPPort:    v.GetInt("server.http_port"),
			FrontendDir: v.GetString("server.frontend_dir"),
		},
		WorkerPool: WorkerPoolConfig{
			Size:           v.GetInt("worker_pool.size"),
			PollInterval:   pollIntervalWP,
			QueueSize:      v.GetInt("worker_pool.queue_size"),
			BackoffMax:     backoffMax,
			BatchThreshold: v.GetInt("worker_pool.batch_threshold"),
		},
		Telemetry: TelemetryConfig{
			Interval: telemetryInterval,
			Enabled:  v.GetBool("telemetry.enabled"),
		},
		Unblocker: UnblockerConfig{
			Enabled:   v.GetBool("unblocker.enabled"),
			Threshold: unblockThreshold,
		},
		Idle: IdleConfig{
			Enabled: v.GetBool("idle.enabled"),
			Linger:  idleLinger,
		},
	}

	if cfg.OBS.Username == "" {
		return nil, fmt.Errorf("OBS_USERNAME is required")
	}

	return cfg, nil
}

// parseRetention handles "7d" as well as standard Go duration strings.
func parseRetention(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
