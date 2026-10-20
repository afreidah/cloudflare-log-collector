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

// The HTTP metrics are cumulative: each non-overlapping poll window adds its
// counts, so the values are immune to poll-window length (backfill, missed
// polls) and dashboards apply rate()/increase() rather than reading a
// per-window snapshot. host is the normalized request hostname, so services
// sharing a zone get their own series. source is Cloudflare's request source:
// "eyeball" for real clients, other values for Cloudflare-internal traffic
// such as a Worker's own subrequests, which would otherwise count a proxied
// request twice - client-traffic queries filter on source="eyeball".

// HTTPRequestsTotal counts HTTP requests by method, edge status, country, host,
// cache status, request source, and zone.
var HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_http_requests_total",
	Help: "Total HTTP requests observed via Cloudflare analytics by method, status, country, host, cache status, source, and zone",
}, []string{"method", "status", "country", "host", "cache_status", "source", "zone"})

// HTTPOriginRequestsTotal counts HTTP requests by origin status alongside the
// edge status. origin_status is what the origin returned to Cloudflare, or "0"
// when no origin answered; the edge status tells those apart - a cache hit or
// edge redirect has a 2xx/3xx edge status, while origin_status="0" with a 5xx
// edge status means the origin was unreachable.
var HTTPOriginRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_http_origin_requests_total",
	Help: "Total HTTP requests observed via Cloudflare analytics by host, origin status, edge status, source, and zone",
}, []string{"host", "origin_status", "status", "source", "zone"})

// HTTPBytesTotal counts HTTP response bytes by type, host, request source, and
// zone.
var HTTPBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_http_bytes_total",
	Help: "Total HTTP response bytes observed via Cloudflare analytics by type, host, source, and zone",
}, []string{"type", "host", "source", "zone"})

// AuditEventsTotal counts audit log events by action type and account.
var AuditEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_audit_events_total",
	Help: "Cloudflare audit log events by action type",
}, []string{"action", "account"})

// -------------------------------------------------------------------------
// RUM / WEB ANALYTICS METRICS
// -------------------------------------------------------------------------

// The RUM page-view and session counters are kept at bounded cardinality
// (country x device x host x site, hosts being the websites carrying the
// beacon); the high-cardinality request path and referer are shipped to Loki
// instead. Known bots are excluded from every RUM metric. The web-vital gauges
// report the p75 over the latest poll's rolling window - quantiles cannot be
// accumulated into a counter.

// RUMPageviewsTotal counts browser-side page views by country, device, host,
// and site, excluding known bots.
var RUMPageviewsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_rum_pageviews_total",
	Help: "Total RUM page views observed via Cloudflare Web Analytics by country, device, host, and site (known bots excluded)",
}, []string{"country", "device", "host", "site"})

// RUMSessionsTotal counts RUM sessions (entry page loads) by country, device,
// host, and site, excluding known bots.
var RUMSessionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cflog_rum_sessions_total",
	Help: "Total RUM sessions (entry page loads) observed via Cloudflare Web Analytics by country, device, host, and site (known bots excluded)",
}, []string{"country", "device", "host", "site"})

// RUMWebVitalSeconds reports the p75 of each time-based Core Web Vital in
// seconds, by vital, device, host, and site, over real browsers only.
var RUMWebVitalSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_rum_web_vital_seconds",
	Help: "RUM Core Web Vitals p75 in seconds (time-based vitals: lcp, inp, fid, fcp, ttfb) by vital, device, host, and site (known bots excluded)",
}, []string{"vital", "device", "host", "site"})

// RUMCumulativeLayoutShift reports the p75 Cumulative Layout Shift (a unitless
// score, so it has its own metric) by device, host, and site, over real
// browsers only.
var RUMCumulativeLayoutShift = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cflog_rum_cumulative_layout_shift",
	Help: "RUM Cumulative Layout Shift p75 (unitless layout-shift score) by device, host, and site (known bots excluded)",
}, []string{"device", "host", "site"})

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
