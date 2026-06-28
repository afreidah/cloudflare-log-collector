// -------------------------------------------------------------------------------
// Entry Point Helper Tests
//
// Author: Alex Freidah
//
// Covers the pure helpers extracted from main: plannedCollectors (the ordered
// set of named collector registrations derived from config) and collectorNames
// (the zone and account name lists used in the startup log). Driving these
// directly keeps main itself thin and verifies the registration naming that the
// lifecycle manager and operators rely on.
// -------------------------------------------------------------------------------

package main

import (
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/config"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

// testConfig returns a config with two zones and, when auditEnabled, one audit
// account, plus the poll/batch fields the collector constructors read.
func testConfig(auditEnabled bool) *config.Config {
	cfg := &config.Config{}
	cfg.Cloudflare.PollInterval = time.Minute
	cfg.Cloudflare.BackfillWindow = time.Hour
	cfg.Loki.BatchSize = 100
	cfg.Cloudflare.Zones = []config.ZoneConfig{
		{ID: "z1", Name: "example.com"},
		{ID: "z2", Name: "example.org"},
	}
	cfg.Cloudflare.AuditLogs.Enabled = auditEnabled
	if auditEnabled {
		cfg.Cloudflare.AuditLogs.Accounts = []config.AccountConfig{
			{ID: "a1", Name: "acme"},
		}
	}
	return cfg
}

func names(planned []namedRunner) []string {
	out := make([]string, len(planned))
	for i, p := range planned {
		out[i] = p.name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlannedCollectors_ZonesOnly(t *testing.T) {
	cf := cloudflare.NewClient("token")
	lk := loki.NewClient("http://loki", "tenant")

	planned := plannedCollectors(testConfig(false), cf, lk)

	want := []string{
		"firewall-example.com", "http-example.com",
		"firewall-example.org", "http-example.org",
	}
	if got := names(planned); !equalStrings(got, want) {
		t.Errorf("registration names = %v, want %v", got, want)
	}
	for _, p := range planned {
		if p.runner == nil {
			t.Errorf("runner for %q is nil", p.name)
		}
	}
}

func TestPlannedCollectors_WithAudit(t *testing.T) {
	cf := cloudflare.NewClient("token")
	lk := loki.NewClient("http://loki", "tenant")

	planned := plannedCollectors(testConfig(true), cf, lk)

	want := []string{
		"firewall-example.com", "http-example.com",
		"firewall-example.org", "http-example.org",
		"audit-acme",
	}
	if got := names(planned); !equalStrings(got, want) {
		t.Errorf("registration names = %v, want %v", got, want)
	}
}

func TestPlannedCollectors_AuditDisabledSkipsAccounts(t *testing.T) {
	cfg := testConfig(false)
	// Accounts present but disabled must not be registered.
	cfg.Cloudflare.AuditLogs.Accounts = []config.AccountConfig{{ID: "a1", Name: "acme"}}

	planned := plannedCollectors(cfg, cloudflare.NewClient("t"), loki.NewClient("http://loki", "t"))

	for _, p := range planned {
		if p.name == "audit-acme" {
			t.Errorf("audit collector registered while audit logging disabled")
		}
	}
	if len(planned) != 4 {
		t.Errorf("got %d registrations, want 4 (2 zones x 2)", len(planned))
	}
}

func TestCollectorNames_ZonesOnly(t *testing.T) {
	zones, accounts := collectorNames(testConfig(false))

	if want := []string{"example.com", "example.org"}; !equalStrings(zones, want) {
		t.Errorf("zone names = %v, want %v", zones, want)
	}
	if len(accounts) != 0 {
		t.Errorf("account names = %v, want empty", accounts)
	}
}

func TestCollectorNames_WithAudit(t *testing.T) {
	zones, accounts := collectorNames(testConfig(true))

	if want := []string{"example.com", "example.org"}; !equalStrings(zones, want) {
		t.Errorf("zone names = %v, want %v", zones, want)
	}
	if want := []string{"acme"}; !equalStrings(accounts, want) {
		t.Errorf("account names = %v, want %v", accounts, want)
	}
}
