// -------------------------------------------------------------------------------
// Configuration - Cloudflare Log Collector Settings
//
// Author: Alex Freidah
//
// YAML configuration loader with environment variable expansion using ${VAR}
// syntax. Validates required fields and applies defaults for optional settings.
// -------------------------------------------------------------------------------

package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// -------------------------------------------------------------------------
// CONFIGURATION TYPES
// -------------------------------------------------------------------------

// Config holds the complete service configuration.
type Config struct {
	Cloudflare CloudflareConfig `yaml:"cloudflare"`
	Loki       LokiConfig       `yaml:"loki"`
	Metrics    MetricsConfig    `yaml:"metrics"`
	Tracing    TracingConfig    `yaml:"tracing"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// CloudflareConfig holds Cloudflare API connection settings.
type CloudflareConfig struct {
	APIToken       string          `yaml:"api_token"`
	Zones          []ZoneConfig    `yaml:"zones"`
	AuditLogs      AuditLogsConfig `yaml:"audit_logs"`
	PollInterval   time.Duration   `yaml:"poll_interval"`
	BackfillWindow time.Duration   `yaml:"backfill_window"`
}

// ZoneConfig identifies a single Cloudflare zone to monitor.
type ZoneConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// AuditLogsConfig holds account audit log collection settings.
type AuditLogsConfig struct {
	Enabled  bool            `yaml:"enabled"`
	Accounts []AccountConfig `yaml:"accounts"`
}

// AccountConfig identifies a single Cloudflare account to collect audit logs from.
type AccountConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// LokiConfig holds Loki push API settings.
type LokiConfig struct {
	Endpoint  string `yaml:"endpoint"`
	TenantID  string `yaml:"tenant_id"`
	BatchSize int    `yaml:"batch_size"`
}

// MetricsConfig holds Prometheus metrics endpoint settings.
type MetricsConfig struct {
	Listen string `yaml:"listen"`
}

// TracingConfig holds OpenTelemetry tracing settings.
type TracingConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Endpoint   string  `yaml:"endpoint"`
	SampleRate float64 `yaml:"sample_rate"`
	Insecure   bool    `yaml:"insecure"`
}

// LoggingConfig holds structured logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// -------------------------------------------------------------------------
// LOADING
// -------------------------------------------------------------------------

// LoadConfig reads the YAML file at path, expands environment variables, and
// returns a validated Config.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// --- Expand environment variables ---
	expanded := os.Expand(string(data), os.Getenv)

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.setDefaultsAndValidate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// -------------------------------------------------------------------------
// VALIDATION
// -------------------------------------------------------------------------

// setDefaultsAndValidate fills in defaults for optional fields, then checks
// that all required configuration values are present.
func (c *Config) setDefaultsAndValidate() error {
	c.applyDefaults()
	return c.validate()
}

// applyDefaults fills in default values for optional fields left unset.
func (c *Config) applyDefaults() {
	if c.Cloudflare.PollInterval == 0 {
		c.Cloudflare.PollInterval = 5 * time.Minute
	}
	if c.Cloudflare.BackfillWindow == 0 {
		c.Cloudflare.BackfillWindow = 1 * time.Hour
	}
	if c.Loki.TenantID == "" {
		c.Loki.TenantID = "fake"
	}
	if c.Loki.BatchSize == 0 {
		c.Loki.BatchSize = 100
	}
	if c.Metrics.Listen == "" {
		c.Metrics.Listen = ":9101"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
}

// validate checks that all required configuration values are present, gathering
// errors per config section.
func (c *Config) validate() error {
	var errs []error
	errs = append(errs, c.validateCloudflare()...)
	errs = append(errs, c.validateAuditLogs()...)
	if c.Loki.Endpoint == "" {
		errs = append(errs, fmt.Errorf("loki.endpoint is required"))
	}
	return errors.Join(errs...)
}

// validateCloudflare checks the required Cloudflare fields and per-zone values.
func (c *Config) validateCloudflare() []error {
	var errs []error
	if c.Cloudflare.APIToken == "" {
		errs = append(errs, fmt.Errorf("cloudflare.api_token is required"))
	}
	if len(c.Cloudflare.Zones) == 0 && !c.Cloudflare.AuditLogs.Enabled {
		errs = append(errs, fmt.Errorf("cloudflare.zones requires at least one zone"))
	}
	for i, z := range c.Cloudflare.Zones {
		if z.ID == "" {
			errs = append(errs, fmt.Errorf("cloudflare.zones[%d].id is required", i))
		}
		if z.Name == "" {
			errs = append(errs, fmt.Errorf("cloudflare.zones[%d].name is required", i))
		}
	}
	return errs
}

// validateAuditLogs checks the audit-log accounts when collection is enabled.
func (c *Config) validateAuditLogs() []error {
	if !c.Cloudflare.AuditLogs.Enabled {
		return nil
	}
	var errs []error
	if len(c.Cloudflare.AuditLogs.Accounts) == 0 {
		errs = append(errs, fmt.Errorf("cloudflare.audit_logs.accounts requires at least one account when enabled"))
	}
	for i, a := range c.Cloudflare.AuditLogs.Accounts {
		if a.ID == "" {
			errs = append(errs, fmt.Errorf("cloudflare.audit_logs.accounts[%d].id is required", i))
		}
		if a.Name == "" {
			errs = append(errs, fmt.Errorf("cloudflare.audit_logs.accounts[%d].name is required", i))
		}
	}
	return errs
}

// ParseLogLevel maps a config string to an slog.Level.
func ParseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
