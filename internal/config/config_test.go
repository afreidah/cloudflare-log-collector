// -------------------------------------------------------------------------------
// Configuration Tests
//
// Author: Alex Freidah
//
// Validates config loading, environment variable expansion, default application,
// and required field validation.
// -------------------------------------------------------------------------------

package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validConfig returns a minimal valid Config for test cases that need to
// modify one field at a time.
func validConfig() Config {
	return Config{
		Cloudflare: CloudflareConfig{
			APIToken: "test-token",
			Zones: []ZoneConfig{
				{ID: "test-zone-id", Name: "example.com"},
			},
		},
		Loki: LokiConfig{
			Endpoint: "http://localhost:3100",
		},
	}
}

// writeConfigFile writes YAML content to a temp file and returns the path.
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// -------------------------------------------------------------------------
// LOADING
// -------------------------------------------------------------------------

func TestLoadConfig_ValidMinimal(t *testing.T) {
	path := writeConfigFile(t, `
cloudflare:
  api_token: "tok"
  zones:
    - id: "zid"
      name: "example.com"
loki:
  endpoint: "http://localhost:3100"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Cloudflare.APIToken != "tok" {
		t.Errorf("api_token = %q, want %q", cfg.Cloudflare.APIToken, "tok")
	}
	if len(cfg.Cloudflare.Zones) != 1 {
		t.Fatalf("zones count = %d, want 1", len(cfg.Cloudflare.Zones))
	}
	if cfg.Cloudflare.Zones[0].ID != "zid" {
		t.Errorf("zones[0].id = %q, want %q", cfg.Cloudflare.Zones[0].ID, "zid")
	}
	if cfg.Cloudflare.Zones[0].Name != "example.com" {
		t.Errorf("zones[0].name = %q, want %q", cfg.Cloudflare.Zones[0].Name, "example.com")
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	t.Setenv("CF_TEST_TOKEN", "expanded-token")
	t.Setenv("CF_TEST_ZONE", "expanded-zone")

	path := writeConfigFile(t, `
cloudflare:
  api_token: "${CF_TEST_TOKEN}"
  zones:
    - id: "${CF_TEST_ZONE}"
      name: "example.com"
loki:
  endpoint: "http://localhost:3100"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Cloudflare.APIToken != "expanded-token" {
		t.Errorf("api_token = %q, want %q", cfg.Cloudflare.APIToken, "expanded-token")
	}
	if cfg.Cloudflare.Zones[0].ID != "expanded-zone" {
		t.Errorf("zones[0].id = %q, want %q", cfg.Cloudflare.Zones[0].ID, "expanded-zone")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/config.yaml")
	if err == nil {
		t.Error("LoadConfig() should fail for missing file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	path := writeConfigFile(t, `{{{invalid yaml`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Error("LoadConfig() should fail for invalid YAML")
	}
}

// -------------------------------------------------------------------------
// DEFAULTS
// -------------------------------------------------------------------------

func TestDefaults_PollInterval(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Cloudflare.PollInterval != 5*time.Minute {
		t.Errorf("poll_interval = %v, want %v", cfg.Cloudflare.PollInterval, 5*time.Minute)
	}
}

func TestDefaults_BackfillWindow(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Cloudflare.BackfillWindow != 1*time.Hour {
		t.Errorf("backfill_window = %v, want %v", cfg.Cloudflare.BackfillWindow, 1*time.Hour)
	}
}

func TestDefaults_LagsAndWindows(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	for _, tt := range []struct {
		key       string
		got, want time.Duration
	}{
		{"ingestion_lag", cfg.Cloudflare.IngestionLag, 3 * time.Minute},
		{"latency_window", cfg.Cloudflare.LatencyWindow, 15 * time.Minute},
		{"web_analytics.ingestion_lag", cfg.Cloudflare.WebAnalytics.IngestionLag, 10 * time.Minute},
		{"web_analytics.vitals_window", cfg.Cloudflare.WebAnalytics.VitalsWindow, time.Hour},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.key, tt.got, tt.want)
		}
	}
}

func TestDNSConfig_OnUnlessDisabled(t *testing.T) {
	on, off := true, false
	for _, tt := range []struct {
		name    string
		enabled *bool
		want    bool
	}{{"unset", nil, true}, {"true", &on, true}, {"false", &off, false}} {
		if got := (DNSConfig{Enabled: tt.enabled}).On(); got != tt.want {
			t.Errorf("%s: On() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDefaults_LokiTenantID(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Loki.TenantID != "fake" {
		t.Errorf("tenant_id = %q, want %q", cfg.Loki.TenantID, "fake")
	}
}

func TestDefaults_LokiBatchSize(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Loki.BatchSize != 100 {
		t.Errorf("batch_size = %d, want %d", cfg.Loki.BatchSize, 100)
	}
}

func TestDefaults_MetricsListen(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Metrics.Listen != ":9101" {
		t.Errorf("metrics.listen = %q, want %q", cfg.Metrics.Listen, ":9101")
	}
}

func TestDefaults_LoggingLevel(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Logging.Level != "info" {
		t.Errorf("logging.level = %q, want %q", cfg.Logging.Level, "info")
	}
}

func TestDefaults_LoggingFormat(t *testing.T) {
	cfg := validConfig()
	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Logging.Format != "json" {
		t.Errorf("logging.format = %q, want %q", cfg.Logging.Format, "json")
	}
}

func TestDefaults_PreserveExplicitValues(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.PollInterval = 10 * time.Minute
	cfg.Loki.TenantID = "custom"
	cfg.Loki.BatchSize = 50

	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Fatalf("setDefaultsAndValidate() error = %v", err)
	}

	if cfg.Cloudflare.PollInterval != 10*time.Minute {
		t.Errorf("explicit poll_interval overwritten: got %v", cfg.Cloudflare.PollInterval)
	}
	if cfg.Loki.TenantID != "custom" {
		t.Errorf("explicit tenant_id overwritten: got %q", cfg.Loki.TenantID)
	}
	if cfg.Loki.BatchSize != 50 {
		t.Errorf("explicit batch_size overwritten: got %d", cfg.Loki.BatchSize)
	}
}

// -------------------------------------------------------------------------
// VALIDATION
// -------------------------------------------------------------------------

func TestValidation_MissingAPIToken(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.APIToken = ""

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Error("validation should fail when api_token is empty")
	}
}

func TestValidation_MissingZones(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.Zones = nil

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Error("validation should fail when zones is empty")
	}
}

func TestValidation_MissingZoneID(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.Zones[0].ID = ""

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Error("validation should fail when zone id is empty")
	}
}

func TestValidation_MissingZoneName(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.Zones[0].Name = ""

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Error("validation should fail when zone name is empty")
	}
}

func TestValidation_MissingLokiEndpoint(t *testing.T) {
	cfg := validConfig()
	cfg.Loki.Endpoint = ""

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Error("validation should fail when loki.endpoint is empty")
	}
}

func TestValidation_WebAnalyticsMissingAccountID(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.WebAnalytics = WebAnalyticsConfig{
		Enabled: true,
		Sites:   []SiteConfig{{SiteTag: "s1", Name: "example.com"}},
	}

	if err := cfg.setDefaultsAndValidate(); err == nil {
		t.Error("validation should fail when web_analytics.account_id is empty")
	}
}

func TestValidation_WebAnalyticsNoSites(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.WebAnalytics = WebAnalyticsConfig{Enabled: true, AccountID: "acct1"}

	if err := cfg.setDefaultsAndValidate(); err == nil {
		t.Error("validation should fail when web_analytics is enabled with no sites")
	}
}

func TestValidation_WebAnalyticsMissingSiteTag(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.WebAnalytics = WebAnalyticsConfig{
		Enabled:   true,
		AccountID: "acct1",
		Sites:     []SiteConfig{{Name: "example.com"}},
	}

	if err := cfg.setDefaultsAndValidate(); err == nil {
		t.Error("validation should fail when site_tag is empty")
	}
}

func TestValidation_WebAnalyticsMissingSiteName(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.WebAnalytics = WebAnalyticsConfig{
		Enabled:   true,
		AccountID: "acct1",
		Sites:     []SiteConfig{{SiteTag: "s1"}},
	}

	if err := cfg.setDefaultsAndValidate(); err == nil {
		t.Error("validation should fail when site name is empty")
	}
}

func TestValidation_WebAnalyticsOnlyIsValid(t *testing.T) {
	// A config with only Web Analytics enabled (no zones, no audit) is valid.
	cfg := validConfig()
	cfg.Cloudflare.Zones = nil
	cfg.Cloudflare.WebAnalytics = WebAnalyticsConfig{
		Enabled:   true,
		AccountID: "acct1",
		Sites:     []SiteConfig{{SiteTag: "s1", Name: "example.com"}},
	}

	if err := cfg.setDefaultsAndValidate(); err != nil {
		t.Errorf("web-analytics-only config should be valid, got: %v", err)
	}
}

func TestValidation_LagsAndWindows(t *testing.T) {
	tests := []struct {
		name          string
		lag           time.Duration
		rumLag        time.Duration
		latencyWindow time.Duration
		vitalsWindow  time.Duration
		wantErr       string
	}{
		{name: "negative zone lag", lag: -time.Minute, wantErr: "cloudflare.ingestion_lag must not be negative"},
		{name: "zone lag at backfill window", lag: time.Hour, wantErr: "cloudflare.ingestion_lag (1h0m0s) must be less than"},
		{name: "rum lag past backfill window", rumLag: 2 * time.Hour, wantErr: "cloudflare.web_analytics.ingestion_lag (2h0m0s) must be less than"},
		{name: "negative latency window", latencyWindow: -time.Minute, wantErr: "cloudflare.latency_window must not be negative"},
		{name: "negative vitals window", vitalsWindow: -time.Minute, wantErr: "cloudflare.web_analytics.vitals_window must not be negative"},
		{name: "explicit values", lag: 5 * time.Minute, rumLag: 15 * time.Minute, latencyWindow: 5 * time.Minute, vitalsWindow: 6 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Cloudflare.IngestionLag = tt.lag
			cfg.Cloudflare.LatencyWindow = tt.latencyWindow
			cfg.Cloudflare.WebAnalytics = WebAnalyticsConfig{
				Enabled:      true,
				AccountID:    "acct1",
				Sites:        []SiteConfig{{SiteTag: "s1", Name: "example.com"}},
				IngestionLag: tt.rumLag,
				VitalsWindow: tt.vitalsWindow,
			}

			err := cfg.setDefaultsAndValidate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidation_RequiredFieldsReportIndexAndKey(t *testing.T) {
	cfg := validConfig()
	cfg.Cloudflare.Zones = []ZoneConfig{{ID: "z1", Name: "a.com"}, {Name: "b.com"}}
	cfg.Cloudflare.AuditLogs = AuditLogsConfig{Enabled: true, Accounts: []AccountConfig{{ID: "a1"}}}

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Fatal("expected missing-field errors")
	}
	for _, want := range []string{
		"cloudflare.zones[1].id is required",
		"cloudflare.audit_logs.accounts[0].name is required",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "zones[0]") {
		t.Errorf("err = %v, the complete zone[0] must not be reported", err)
	}
}

func TestValidation_MultipleErrors(t *testing.T) {
	cfg := Config{}

	err := cfg.setDefaultsAndValidate()
	if err == nil {
		t.Fatal("validation should fail with multiple errors")
	}

	// Every missing required field is reported.
	errStr := err.Error()
	if !strings.Contains(errStr, "api_token") || !strings.Contains(errStr, "zones") || !strings.Contains(errStr, "loki.endpoint") {
		t.Errorf("error should mention all missing fields, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// LOG LEVEL
// -------------------------------------------------------------------------

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseLogLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
