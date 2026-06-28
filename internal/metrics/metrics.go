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
// RUM / WEB ANALYTICS METRICS
// -------------------------------------------------------------------------

// The RUM page-view and session counters are kept at bounded cardinality
// (country x device x site); the high-cardinality request path and referer are
// shipped to Loki instead. The web-vital gauges report the p75 over the latest
// poll's rolling window - quantiles cannot be accumulated into a counter.

// RUMPageviewsTotal counts browser-side page views by country, device, and site.
var RUMPageviewsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_rum_pageviews_total",
	Help: "Total RUM page views observed via Cloudflare Web Analytics by country, device, and site",
}, []string{"country", "device", "site"})

// RUMSessionsTotal counts RUM sessions (entry page loads) by country, device, and site.
var RUMSessionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_rum_sessions_total",
	Help: "Total RUM sessions (entry page loads) observed via Cloudflare Web Analytics by country, device, and site",
}, []string{"country", "device", "site"})

// RUMWebVitalSeconds reports the p75 of each time-based Core Web Vital in
// seconds, by vital, device, and site.
var RUMWebVitalSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_rum_web_vital_seconds",
	Help: "RUM Core Web Vitals p75 in seconds (time-based vitals: lcp, inp, fid, fcp, ttfb) by vital, device, and site",
}, []string{"vital", "device", "site"})

// RUMCumulativeLayoutShift reports the p75 Cumulative Layout Shift (a unitless
// score, so it has its own metric) by device and site.
var RUMCumulativeLayoutShift = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_rum_cumulative_layout_shift",
	Help: "RUM Cumulative Layout Shift p75 (unitless layout-shift score) by device and site",
}, []string{"device", "site"})

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
