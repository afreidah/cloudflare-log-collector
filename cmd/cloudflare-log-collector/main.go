// -------------------------------------------------------------------------------
// Cloudflare Log Collector - Entry Point
//
// Authors: Alex Freidah, Aaron Florey
//
// Polls the Cloudflare GraphQL Analytics, Web Analytics, and REST Audit Logs
// APIs for firewall events, HTTP traffic and latency, browser-side RUM data,
// and account audit logs. Ships records to Loki as structured JSON logs and
// exposes counters and gauges via Prometheus. Traces every poll cycle with
// OpenTelemetry for Tempo correlation.
// -------------------------------------------------------------------------------

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/collector"
	"github.com/afreidah/cloudflare-log-collector/internal/config"
	"github.com/afreidah/cloudflare-log-collector/internal/lifecycle"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"
)

// main is the process entry point. Loads config, initializes tracing and
// logging, starts background collectors, and runs the metrics HTTP server.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("cloudflare-log-collector %s (%s)\n", telemetry.Version, runtime.Version())
		return
	}

	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration.
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize the structured logger with trace correlation.
	var logLevel slog.LevelVar
	logLevel.Set(config.ParseLogLevel(cfg.Logging.Level))
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: &logLevel})
	traceHandler := telemetry.NewTraceHandler(jsonHandler)
	slog.SetDefault(slog.New(traceHandler))

	// Initialize tracing.
	ctx := context.Background()
	shutdownTracer, err := telemetry.InitTracer(ctx, cfg.Tracing)
	if err != nil {
		slog.Error("Failed to initialize tracer", "error", err)
		os.Exit(1)
	}

	// Set the build info metric.
	metrics.BuildInfo.WithLabelValues(telemetry.Version, runtime.Version()).Set(1)

	// Create the shared clients.
	cfClient := cloudflare.NewClient(cfg.Cloudflare.APIToken)
	lokiClient := loki.NewClient(cfg.Loki.Endpoint, cfg.Loki.TenantID)

	// Start the background collectors under the lifecycle manager.
	sm := lifecycle.NewManager()
	for _, c := range plannedCollectors(cfg, cfClient, lokiClient) {
		sm.Register(c.name, c.runner)
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	bgDone := make(chan struct{})
	go func() {
		sm.Run(bgCtx)
		close(bgDone)
	}()

	// Set up the HTTP server for metrics and health.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// healthHandler returns a simple health check response.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"ok"}`)
	})

	httpServer := &http.Server{
		Addr:              cfg.Metrics.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Log startup info.
	zoneNames, accountNames, siteNames := collectorNames(cfg)

	slog.Info("Cloudflare Log Collector starting",
		"version", telemetry.Version,
		"zones", zoneNames,
		"audit_accounts", accountNames,
		"web_analytics_sites", siteNames,
		"listen", cfg.Metrics.Listen,
		"poll_interval", cfg.Cloudflare.PollInterval,
		"backfill_window", cfg.Cloudflare.BackfillWindow,
		"loki_endpoint", cfg.Loki.Endpoint,
	)

	if cfg.Tracing.Enabled {
		slog.Info("Tracing enabled",
			"endpoint", cfg.Tracing.Endpoint,
			"sample_rate", cfg.Tracing.SampleRate,
			"insecure", cfg.Tracing.Insecure,
		)
	}

	// Handle graceful shutdown.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigChan

		slog.Info("Shutting down", "signal", sig.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Drain the HTTP server.
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP server shutdown error", "error", err)
		}

		// Stop the background collectors.
		bgCancel()
		<-bgDone
		sm.Stop(10 * time.Second)

		// Flush traces.
		traceCtx, traceCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer traceCancel()
		if err := shutdownTracer(traceCtx); err != nil {
			slog.Error("Tracer shutdown error", "error", err)
		}
	}()

	// Start the metrics server.
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	<-shutdownDone
	slog.Info("Server stopped")
}

// namedRunner pairs a lifecycle registration name with the runner to supervise.
type namedRunner struct {
	name   string
	runner lifecycle.Runner
}

// newNamedRunner names a collector's registration after its dataset and the
// zone, account, or site it polls, e.g. "firewall-example.com".
func newNamedRunner(dataset collector.Dataset, scope string, runner lifecycle.Runner) namedRunner {
	return namedRunner{name: fmt.Sprintf("%s-%s", dataset, scope), runner: runner}
}

// plannedCollectors builds the full ordered set of collector registrations from
// config: a firewall and HTTP collector per zone, with visits and DNS
// collectors unless disabled, plus an audit collector per account and a RUM
// collector per site when those are enabled. Keeping the plan a pure function
// of config and clients lets tests assert the registration set without
// standing up a lifecycle manager.
func plannedCollectors(cfg *config.Config, cf *cloudflare.Client, lk *loki.Client) []namedRunner {
	var planned []namedRunner

	// Settings every collector shares. Zone collectors add the latency window;
	// RUM has its own ingestion lag and its vitals window. Audit has no
	// trailing poll, so it takes the shared settings as they are.
	common := collector.CommonConfig{
		Loki:           lk,
		PollInterval:   cfg.Cloudflare.PollInterval,
		BackfillWindow: cfg.Cloudflare.BackfillWindow,
		IngestionLag:   cfg.Cloudflare.IngestionLag,
		BatchSize:      cfg.Loki.BatchSize,
	}
	zoneCommon := common
	zoneCommon.TrailingWindow = cfg.Cloudflare.LatencyWindow
	rumCommon := common
	rumCommon.IngestionLag = cfg.Cloudflare.WebAnalytics.IngestionLag
	rumCommon.TrailingWindow = cfg.Cloudflare.WebAnalytics.VitalsWindow

	// Every zone runs the same visits query, so it is built once.
	visitsQuery := cloudflare.NewHTTPVisitsQuery(cfg.Cloudflare.HTTPVisits.ExcludeUserAgents)

	for _, zone := range cfg.Cloudflare.Zones {
		cc := collector.ZoneCollectorConfig{
			CommonConfig: zoneCommon,
			CF:           cf,
			ZoneID:       zone.ID,
			ZoneName:     zone.Name,
		}

		planned = append(planned,
			newNamedRunner(collector.DatasetFirewall, zone.Name, collector.NewFirewallCollector(cc)),
			newNamedRunner(collector.DatasetHTTP, zone.Name, collector.NewHTTPCollector(cc)),
		)
		if cfg.Cloudflare.HTTPVisits.On() {
			planned = append(planned, newNamedRunner(collector.DatasetHTTPVisits, zone.Name, collector.NewVisitsCollector(cc, visitsQuery)))
		}
		if cfg.Cloudflare.DNS.On() {
			planned = append(planned, newNamedRunner(collector.DatasetDNS, zone.Name, collector.NewDNSCollector(cc)))
		}
	}

	if cfg.Cloudflare.AuditLogs.Enabled {
		for _, account := range cfg.Cloudflare.AuditLogs.Accounts {
			ac := collector.AuditCollectorConfig{
				CommonConfig: common,
				CF:           cf,
				AccountID:    account.ID,
				AccountName:  account.Name,
			}

			planned = append(planned, newNamedRunner(collector.DatasetAudit, account.Name, collector.NewAuditCollector(ac)))
		}
	}

	if cfg.Cloudflare.WebAnalytics.Enabled {
		for _, site := range cfg.Cloudflare.WebAnalytics.Sites {
			rc := collector.RUMCollectorConfig{
				CommonConfig: rumCommon,
				CF:           cf,
				AccountID:    cfg.Cloudflare.WebAnalytics.AccountID,
				SiteTag:      site.SiteTag,
				SiteName:     site.Name,
			}

			planned = append(planned, newNamedRunner(collector.DatasetRUM, site.Name, collector.NewRUMCollector(rc)))
		}
	}

	return planned
}

// collectorNames returns the configured zone names and, when the respective
// collection is enabled, the audit account names and Web Analytics site names.
// Used to populate the startup log line.
func collectorNames(cfg *config.Config) (zones, accounts, sites []string) {
	zones = names(cfg.Cloudflare.Zones, func(z config.ZoneConfig) string { return z.Name })
	if cfg.Cloudflare.AuditLogs.Enabled {
		accounts = names(cfg.Cloudflare.AuditLogs.Accounts, func(a config.AccountConfig) string { return a.Name })
	}
	if cfg.Cloudflare.WebAnalytics.Enabled {
		sites = names(cfg.Cloudflare.WebAnalytics.Sites, func(s config.SiteConfig) string { return s.Name })
	}
	return zones, accounts, sites
}

// names returns the display name of each config list item, in order.
func names[T any](items []T, name func(T) string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = name(item)
	}
	return out
}
