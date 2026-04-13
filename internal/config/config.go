package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime settings for the bot.
// Values are resolved in order: defaults → config.yaml → environment variables.
type Config struct {
	BotToken     string `yaml:"bot_token"`
	SubsPath     string `yaml:"subs_path"`
	SeenPath     string `yaml:"seen_path"`
	PollInterval string `yaml:"poll_interval"`
	LogLevel     string `yaml:"log_level"`
	DBPath       string `yaml:"db_path"`
	ProxyURL     string `yaml:"proxy_url"`

	// PollIntervalDuration is the parsed form of PollInterval. Not a YAML field.
	PollIntervalDuration time.Duration `yaml:"-"`
}

// Load reads configuration from path (YAML), then overrides any field where
// the corresponding environment variable is non-empty. Missing config file is
// not an error — defaults are used instead.
func Load(path string) (*Config, error) {
	cfg := Config{
		SubsPath:     "subscriptions.json",
		SeenPath:     "seen_jobs.json",
		PollInterval: "30m",
		LogLevel:     "info",
		DBPath:       "./job-intel-bot.db",
	}

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err == nil {
		if err = yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	if v := os.Getenv("BOT_TOKEN"); v != "" {
		cfg.BotToken = v
	}
	if v := os.Getenv("SUBS_PATH"); v != "" {
		cfg.SubsPath = v
	}
	if v := os.Getenv("SEEN_PATH"); v != "" {
		cfg.SeenPath = v
	}
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		cfg.PollInterval = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("PROXY_URL"); v != "" {
		cfg.ProxyURL = v
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("bot_token is required (set BOT_TOKEN env var or bot_token in %s)", path)
	}

	d, err := time.ParseDuration(cfg.PollInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid poll_interval %q: %w", cfg.PollInterval, err)
	}
	cfg.PollIntervalDuration = d

	return &cfg, nil
}
