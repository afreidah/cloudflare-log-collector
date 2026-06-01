// -------------------------------------------------------------------------------
// Prometheus Metrics - Cloudflare Log Collector
//
// Author: Alex Freidah
//
// Defines all Prometheus metrics for the collector. Uses promauto for automatic
// registration. Tracks poll operations, Loki pushes, and Cloudflare event counts.
// -------------------------------------------------------------------------------

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// -------------------------------------------------------------------------
// POLL METRICS
// -------------------------------------------------------------------------

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

// HTTPRequests tracks HTTP request counts from the last poll window.
var HTTPRequests = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_http_requests",
	Help: "HTTP request counts from last poll window",
}, []string{"method", "status", "country", "zone"})

// HTTPBytes tracks byte counts by type and zone from the last poll window.
var HTTPBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_http_bytes",
	Help: "HTTP bytes by type from last poll window",
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
