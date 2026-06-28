// -------------------------------------------------------------------------------
// Prometheus Metrics - Cloudflare Log Collector
//
// Authors: Alex Freidah, Aaron Florey
//
// Defines all Prometheus metrics for the collector. Uses promauto for automatic
// registration. Tracks poll operations, Loki pushes, and Cloudflare event counts.
// -------------------------------------------------------------------------------

// Package metrics defines and registers the Prometheus metrics exported by the
// collector: poll operations, Loki pushes, and Cloudflare event counts.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// -------------------------------------------------------------------------
// POLL METRICS
// -------------------------------------------------------------------------

// The "zone" label identifies the polled scope. For the firewall and http
// datasets it holds the zone name; for the audit dataset it holds the account
// name, since audit logs are scoped per account rather than per zone.

// PollTotal counts poll attempts by dataset, zone, and status.
var PollTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_poll_total",
	Help: "Total Cloudflare API poll attempts",
}, []string{"dataset", "zone", "status"})

// PollDuration tracks poll latency by dataset and zone.
var PollDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "cflog_poll_duration_seconds",
	Help:    "Cloudflare API poll latency in seconds",
	Buckets: prometheus.DefBuckets,
}, []string{"dataset", "zone"})

// LastPollTimestamp records the unix timestamp of the last successful poll.
var LastPollTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_last_poll_timestamp",
	Help: "Unix timestamp of last successful poll",
}, []string{"dataset", "zone"})

// -------------------------------------------------------------------------
// EVENT METRICS
// -------------------------------------------------------------------------

// FirewallEventsTotal counts firewall events by action and zone.
var FirewallEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_firewall_events_total",
	Help: "Cloudflare firewall events by action",
}, []string{"action", "zone"})

// HTTPRequestsTotal counts HTTP requests by method, status, country, and zone.
// It is cumulative: each non-overlapping poll window adds its counts, so the
// value is immune to poll-window length (backfill, missed polls) and dashboards
// apply rate()/increase() rather than reading a per-window snapshot.
var HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_http_requests_total",
	Help: "Total HTTP requests observed via Cloudflare analytics by method, status, country, and zone",
}, []string{"method", "status", "country", "zone"})

// HTTPBytesTotal counts HTTP response bytes by type and zone, cumulative across
// poll windows like HTTPRequestsTotal.
var HTTPBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_http_bytes_total",
	Help: "Total HTTP response bytes observed via Cloudflare analytics by type and zone",
}, []string{"type", "zone"})

// AuditEventsTotal counts audit log events by action type and account.
var AuditEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_audit_events_total",
	Help: "Cloudflare audit log events by action type",
}, []string{"action", "account"})

// -------------------------------------------------------------------------
// LOKI METRICS
// -------------------------------------------------------------------------

// LokiPushTotal counts Loki push attempts by status.
var LokiPushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_loki_push_total",
	Help: "Total Loki push attempts",
}, []string{"status"})

// LokiPushDuration tracks Loki push latency.
var LokiPushDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "cflog_loki_push_duration_seconds",
	Help:    "Loki push latency in seconds",
	Buckets: prometheus.DefBuckets,
})

// -------------------------------------------------------------------------
// BUILD INFO
// -------------------------------------------------------------------------

// BuildInfo exposes version and Go runtime metadata.
var BuildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_build_info",
	Help: "Build information",
}, []string{"version", "go_version"})
