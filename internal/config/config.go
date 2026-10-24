// -------------------------------------------------------------------------------
// Configuration - Cloudflare Log Collector Settings
//
// Authors: Alex Freidah, Aaron Florey
//
// YAML configuration loader with environment variable expansion using ${VAR}
// syntax. Validates required fields and applies defaults for optional settings.
// -------------------------------------------------------------------------------

// Package config loads the YAML configuration with environment-variable
// expansion, validates required fields, and applies defaults.
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

// CloudflareConfig holds Cloudflare API connection settings. IngestionLag holds
// each cursor-based poll's window end back from now, so Cloudflare has finished
// ingesting a window before it is read and the cursor moves past it; it applies
// to the firewall, HTTP, and audit datasets (Web Analytics has its own).
// LatencyWindow is how far back the per-host HTTP latency quantiles reach:
// short enough to show an incident while it happens, long enough to hold
// enough requests per host for a stable p95.
type CloudflareConfig struct {
	APIToken       string             `yaml:"api_token"`
	Zones          []ZoneConfig       `yaml:"zones"`
	AuditLogs      AuditLogsConfig    `yaml:"audit_logs"`
	WebAnalytics   WebAnalyticsConfig `yaml:"web_analytics"`
	DNS            DNSConfig          `yaml:"dns"`
	PollInterval   time.Duration      `yaml:"poll_interval"`
	BackfillWindow time.Duration      `yaml:"backfill_window"`
	IngestionLag   time.Duration      `yaml:"ingestion_lag"`
	LatencyWindow  time.Duration      `yaml:"latency_window"`
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

// WebAnalyticsConfig holds RUM / Web Analytics collection settings. The RUM
// datasets are account-scoped, so all sites share one account_id and are
// identified by their Web Analytics site tag. IngestionLag is the page-load
// window's lag behind now; RUM data lands well after the zone datasets, so it
// is set separately from cloudflare.ingestion_lag. VitalsWindow is how far
// back the Core Web Vitals p75 reaches; page views are sparse, so it is wider
// than the HTTP latency window.
type WebAnalyticsConfig struct {
	Enabled      bool          `yaml:"enabled"`
	AccountID    string        `yaml:"account_id"`
	Sites        []SiteConfig  `yaml:"sites"`
	IngestionLag time.Duration `yaml:"ingestion_lag"`
	VitalsWindow time.Duration `yaml:"vitals_window"`
}

// DNSConfig holds authoritative DNS analytics settings. Collection runs for
// every zone unless Enabled is set to false; unset means on.
type DNSConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// On reports whether DNS analytics are collected.
func (d DNSConfig) On() bool {
	return d.Enabled == nil || *d.Enabled
}

// SiteConfig identifies a single Web Analytics site to collect RUM data from.
type SiteConfig struct {
	SiteTag string `yaml:"site_tag"`
	Name    string `yaml:"name"`
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

	// Expand environment variables.
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
	if c.Cloudflare.IngestionLag == 0 {
		c.Cloudflare.IngestionLag = 3 * time.Minute
	}
	if c.Cloudflare.LatencyWindow == 0 {
		c.Cloudflare.LatencyWindow = 15 * time.Minute
	}
	if c.Cloudflare.WebAnalytics.IngestionLag == 0 {
		c.Cloudflare.WebAnalytics.IngestionLag = 10 * time.Minute
	}
	if c.Cloudflare.WebAnalytics.VitalsWindow == 0 {
		c.Cloudflare.WebAnalytics.VitalsWindow = 1 * time.Hour
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
	errs = append(errs, c.validateWebAnalytics()...)
	if c.Loki.Endpoint == "" {
		errs = append(errs, fmt.Errorf("loki.endpoint is required"))
	}
	return errors.Join(errs...)
}

// validateCloudflare checks the required Cloudflare fields, per-zone values,
// and the zone datasets' ingestion lag and latency window.
func (c *Config) validateCloudflare() []error {
	var errs []error
	if c.Cloudflare.APIToken == "" {
		errs = append(errs, fmt.Errorf("cloudflare.api_token is required"))
	}
	if len(c.Cloudflare.Zones) == 0 && !c.Cloudflare.AuditLogs.Enabled && !c.Cloudflare.WebAnalytics.Enabled {
		errs = append(errs, fmt.Errorf("cloudflare.zones requires at least one zone"))
	}
	errs = append(errs, requireFields("cloudflare.zones", c.Cloudflare.Zones, func(z ZoneConfig) []requiredField {
		return idAndName(z.ID, z.Name)
	})...)
	errs = append(errs, c.validateLag("cloudflare.ingestion_lag", c.Cloudflare.IngestionLag)...)
	errs = append(errs, notNegative("cloudflare.latency_window", c.Cloudflare.LatencyWindow)...)
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
	errs = append(errs, requireFields("cloudflare.audit_logs.accounts", c.Cloudflare.AuditLogs.Accounts, func(a AccountConfig) []requiredField {
		return idAndName(a.ID, a.Name)
	})...)
	return errs
}

// validateWebAnalytics checks the Web Analytics sites, their ingestion lag,
// and the vitals window when collection is enabled.
func (c *Config) validateWebAnalytics() []error {
	if !c.Cloudflare.WebAnalytics.Enabled {
		return nil
	}
	var errs []error
	if c.Cloudflare.WebAnalytics.AccountID == "" {
		errs = append(errs, fmt.Errorf("cloudflare.web_analytics.account_id is required when enabled"))
	}
	if len(c.Cloudflare.WebAnalytics.Sites) == 0 {
		errs = append(errs, fmt.Errorf("cloudflare.web_analytics.sites requires at least one site when enabled"))
	}
	errs = append(errs, requireFields("cloudflare.web_analytics.sites", c.Cloudflare.WebAnalytics.Sites, func(s SiteConfig) []requiredField {
		return []requiredField{{"site_tag", s.SiteTag}, {"name", s.Name}}
	})...)
	errs = append(errs, c.validateLag("cloudflare.web_analytics.ingestion_lag", c.Cloudflare.WebAnalytics.IngestionLag)...)
	errs = append(errs, notNegative("cloudflare.web_analytics.vitals_window", c.Cloudflare.WebAnalytics.VitalsWindow)...)
	return errs
}

// validateLag checks an ingestion lag at the given config path. It must not be
// negative, and it must be shorter than the backfill window: the cursor starts
// one backfill window back, so a lag at least that long leaves the startup
// backfill nothing to collect.
func (c *Config) validateLag(path string, lag time.Duration) []error {
	if errs := notNegative(path, lag); errs != nil {
		return errs
	}
	if lag >= c.Cloudflare.BackfillWindow {
		return []error{fmt.Errorf("%s (%s) must be less than cloudflare.backfill_window (%s)", path, lag, c.Cloudflare.BackfillWindow)}
	}
	return nil
}

// notNegative checks that the duration at the given config path is not
// negative. Zero is never seen here: an unset duration has its default.
func notNegative(path string, d time.Duration) []error {
	if d < 0 {
		return []error{fmt.Errorf("%s must not be negative", path)}
	}
	return nil
}

// -------------------------------------------------------------------------
// LIST VALIDATION
// -------------------------------------------------------------------------

// requiredField is one field a config list item must set: its YAML key and its
// value for that item.
type requiredField struct {
	key   string
	value string
}

// idAndName is the required fields of a list item identified by an ID and a
// display name (zones and audit accounts).
func idAndName(id, name string) []requiredField {
	return []requiredField{{"id", id}, {"name", name}}
}

// requireFields reports every required field left empty across a config list,
// as "<path>[<index>].<key> is required". fields returns an item's required
// fields in the order they should be reported.
func requireFields[T any](path string, items []T, fields func(T) []requiredField) []error {
	var errs []error
	for i, item := range items {
		for _, f := range fields(item) {
			if f.value == "" {
				errs = append(errs, fmt.Errorf("%s[%d].%s is required", path, i, f.key))
			}
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
