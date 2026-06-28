// -------------------------------------------------------------------------------
// Cloudflare Log Collector - Entry Point
//
// Authors: Alex Freidah, Aaron Florey
//
// Polls the Cloudflare GraphQL Analytics API for firewall events and HTTP
// traffic statistics. Ships firewall events to Loki as structured JSON logs
// and exposes HTTP traffic metrics via Prometheus. Traces every poll cycle
// with OpenTelemetry for Tempo correlation.
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

	// --- Load configuration ---
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// --- Initialize structured logger with trace correlation ---
	var logLevel slog.LevelVar
	logLevel.Set(config.ParseLogLevel(cfg.Logging.Level))
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: &logLevel})
	traceHandler := telemetry.NewTraceHandler(jsonHandler)
	slog.SetDefault(slog.New(traceHandler))

	// --- Initialize tracing ---
	ctx := context.Background()
	shutdownTracer, err := telemetry.InitTracer(ctx, cfg.Tracing)
	if err != nil {
		slog.Error("Failed to initialize tracer", "error", err)
		os.Exit(1)
	}

	// --- Set build info metric ---
	metrics.BuildInfo.WithLabelValues(telemetry.Version, runtime.Version()).Set(1)

	// --- Create shared clients ---
	cfClient := cloudflare.NewClient(cfg.Cloudflare.APIToken)
	lokiClient := loki.NewClient(cfg.Loki.Endpoint, cfg.Loki.TenantID)

	// --- Start background collectors with lifecycle manager ---
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

	// --- Setup HTTP server for metrics and health ---
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

	// --- Log startup info ---
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

	// --- Handle graceful shutdown ---
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigChan

		slog.Info("Shutting down", "signal", sig.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// --- Drain HTTP server ---
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP server shutdown error", "error", err)
		}

		// --- Stop background collectors ---
		bgCancel()
		<-bgDone
		sm.Stop(10 * time.Second)

		// --- Flush traces ---
		traceCtx, traceCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer traceCancel()
		if err := shutdownTracer(traceCtx); err != nil {
			slog.Error("Tracer shutdown error", "error", err)
		}
	}()

	// --- Start metrics server ---
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

// plannedCollectors builds the full ordered set of collector registrations from
// config: a firewall and HTTP collector per zone, plus an audit collector per
// account when audit logging is enabled. Keeping the plan a pure function of
// config and clients lets tests assert the registration set without standing up
// a lifecycle manager.
func plannedCollectors(cfg *config.Config, cf *cloudflare.Client, lk *loki.Client) []namedRunner {
	var planned []namedRunner

	for _, zone := range cfg.Cloudflare.Zones {
		cc := collector.CollectorConfig{
			CF:             cf,
			Loki:           lk,
			ZoneID:         zone.ID,
			ZoneName:       zone.Name,
			PollInterval:   cfg.Cloudflare.PollInterval,
			BackfillWindow: cfg.Cloudflare.BackfillWindow,
			BatchSize:      cfg.Loki.BatchSize,
		}

		planned = append(planned,
			namedRunner{fmt.Sprintf("%s-%s", collector.DatasetFirewall, zone.Name), collector.NewFirewallCollector(cc)},
			namedRunner{fmt.Sprintf("%s-%s", collector.DatasetHTTP, zone.Name), collector.NewHTTPCollector(cc)},
		)
	}

	if cfg.Cloudflare.AuditLogs.Enabled {
		for _, account := range cfg.Cloudflare.AuditLogs.Accounts {
			ac := collector.AuditCollectorConfig{
				CF:             cf,
				Loki:           lk,
				AccountID:      account.ID,
				AccountName:    account.Name,
				PollInterval:   cfg.Cloudflare.PollInterval,
				BackfillWindow: cfg.Cloudflare.BackfillWindow,
				BatchSize:      cfg.Loki.BatchSize,
			}

			planned = append(planned, namedRunner{
				name:   fmt.Sprintf("%s-%s", collector.DatasetAudit, account.Name),
				runner: collector.NewAuditCollector(ac),
			})
		}
	}

	if cfg.Cloudflare.WebAnalytics.Enabled {
		for _, site := range cfg.Cloudflare.WebAnalytics.Sites {
			rc := collector.RUMCollectorConfig{
				CF:             cf,
				Loki:           lk,
				AccountID:      cfg.Cloudflare.WebAnalytics.AccountID,
				SiteTag:        site.SiteTag,
				SiteName:       site.Name,
				PollInterval:   cfg.Cloudflare.PollInterval,
				BackfillWindow: cfg.Cloudflare.BackfillWindow,
				BatchSize:      cfg.Loki.BatchSize,
			}

			planned = append(planned, namedRunner{
				name:   fmt.Sprintf("%s-%s", collector.DatasetRUM, site.Name),
				runner: collector.NewRUMCollector(rc),
			})
		}
	}

	return planned
}

// collectorNames returns the configured zone names and, when the respective
// collection is enabled, the audit account names and Web Analytics site names.
// Used to populate the startup log line.
func collectorNames(cfg *config.Config) (zones, accounts, sites []string) {
	zones = make([]string, len(cfg.Cloudflare.Zones))
	for i, z := range cfg.Cloudflare.Zones {
		zones[i] = z.Name
	}

	if cfg.Cloudflare.WebAnalytics.Enabled {
		sites = make([]string, len(cfg.Cloudflare.WebAnalytics.Sites))
		for i, s := range cfg.Cloudflare.WebAnalytics.Sites {
			sites[i] = s.Name
		}
	}

	if cfg.Cloudflare.AuditLogs.Enabled {
		accounts = make([]string, len(cfg.Cloudflare.AuditLogs.Accounts))
		for i, a := range cfg.Cloudflare.AuditLogs.Accounts {
			accounts[i] = a.Name
		}
	}

	return zones, accounts, sites
}
