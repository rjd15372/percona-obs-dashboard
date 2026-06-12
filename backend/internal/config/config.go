package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	OBS    OBSConfig
	MQ     MQConfig
	Poller PollerConfig
	Store  StoreConfig
	Server ServerConfig
}

type OBSConfig struct {
	Username string
	Password string
	BaseURL  string
}

type MQConfig struct {
	URL string
}

type PollerConfig struct {
	Interval time.Duration
}

type StoreConfig struct {
	DBPath         string
	EventRetention time.Duration
}

type ServerConfig struct {
	HTTPPort    int
	FrontendDir string
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetDefault("obs.base_url", "https://api.opensuse.org")
	v.SetDefault("mq.url", "amqps://opensuse:opensuse@rabbit.opensuse.org:5671/")
	v.SetDefault("poller.interval", "5m")
	v.SetDefault("store.db_path", "/data/obsboard.db")
	v.SetDefault("store.event_retention", "7d")
	v.SetDefault("server.http_port", 8080)
	v.SetDefault("server.frontend_dir", "")

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
		{"mq.url", "MQ_URL"},
		{"poller.interval", "POLL_INTERVAL"},
		{"store.db_path", "DB_PATH"},
		{"store.event_retention", "EVENT_RETENTION"},
		{"server.http_port", "HTTP_PORT"},
		{"server.frontend_dir", "FRONTEND_DIR"},
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

	cfg := &Config{
		OBS: OBSConfig{
			Username: v.GetString("obs.username"),
			Password: v.GetString("obs.password"),
			BaseURL:  strings.TrimRight(v.GetString("obs.base_url"), "/"),
		},
		MQ: MQConfig{URL: v.GetString("mq.url")},
		Poller: PollerConfig{Interval: pollInterval},
		Store: StoreConfig{
			DBPath:         v.GetString("store.db_path"),
			EventRetention: retention,
		},
		Server: ServerConfig{
			HTTPPort:    v.GetInt("server.http_port"),
			FrontendDir: v.GetString("server.frontend_dir"),
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
