---
title: "Grafana Dashboard"
weight: 15
---

<p class="landing-subheader">Pre-built Grafana dashboard for monitoring Cloudflare analytics collection</p>

The dashboard tracks the full collection pipeline at a glance. **Poll health** panels show success/failure rates, latency histograms, and last-poll timestamps for each dataset and zone. **Firewall events** are broken down by action type (block, challenge, allow) with trending counters. **HTTP traffic** panels display client request rates by domain, method, status code, and country alongside bandwidth. **Per-service** panels split HTTP traffic by host: request rate, 5xx error rate, cache hit ratio, 5xx responses with no origin (an unreachable origin for a tunneled service, a failing Worker otherwise), the upstream errors behind Workers, and origin latency and edge time to first byte at p95. **DNS and visitors** panels show DNS queries by type and response code, processing time, the top NXDOMAIN names, daily unique IPs against server-side and RUM visits, and server-side visits per host over time and in total for the selected range. **Audit** and **RUM** panels cover account audit actions, browser page views per service, and Core Web Vitals per host; the same Service selector filters both a service's HTTP and RUM panels. **Loki push** panels monitor push success rates and latency to catch delivery issues early. Build info and version metadata round out the overview.

![Grafana Dashboard](/images/grafana.png)

## Metrics

The dashboard visualizes the following Prometheus metrics exposed on the configured listen address (default `:9101`):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cflog_poll_total` | counter | `dataset`, `zone`, `status` | Poll attempts by dataset, zone, and outcome |
| `cflog_poll_duration_seconds` | histogram | `dataset`, `zone` | Poll latency |
| `cflog_last_poll_timestamp` | gauge | `dataset`, `zone` | Unix timestamp of last successful poll |
| `cflog_firewall_events_total` | counter | `action`, `zone` | Firewall events by action type |
| `cflog_audit_events_total` | counter | `action`, `account` | Audit log events by action type |
| `cflog_http_requests_total` | counter | `method`, `status`, `country`, `host`, `cache_status`, `source`, `zone` | Cumulative HTTP requests |
| `cflog_http_origin_requests_total` | counter | `host`, `origin_status`, `status`, `source`, `zone` | Cumulative HTTP requests by origin status and edge status |
| `cflog_http_bytes_total` | counter | `type`, `host`, `source`, `zone` | Cumulative edge response bytes |
| `cflog_http_origin_latency_seconds` | gauge | `quantile`, `host`, `zone` | Origin response time p50/p95/p99 over the trailing latency window (client requests an origin answered) |
| `cflog_http_edge_ttfb_seconds` | gauge | `quantile`, `host`, `zone` | Edge time to first byte p50/p95/p99 over the trailing latency window (client requests, cache hits included) |
| `cflog_http_unique_ips` | gauge | `window`, `zone` | Distinct client IPs in the last complete hour (`1h`) or UTC day (`1d`); IPs, not people |
| `cflog_http_visits_total` | counter | `host`, `zone` | Server-side visits from real clients, excluded user agents left out |
| `cflog_dns_queries_total` | counter | `query_type`, `response_code`, `zone` | Authoritative DNS queries |
| `cflog_dns_processing_seconds` | gauge | `quantile`, `zone` | DNS processing time p50/p95/p99 over the trailing latency window |
| `cflog_rum_pageviews_total` | counter | `country`, `device`, `host`, `site` | RUM page views (known bots excluded) |
| `cflog_rum_sessions_total` | counter | `country`, `device`, `host`, `site` | RUM sessions (entry page loads, known bots excluded) |
| `cflog_rum_web_vital_seconds` | gauge | `vital`, `device`, `host`, `site` | Core Web Vitals p75 in seconds (lcp, inp, fid, fcp, ttfb), real browsers only |
| `cflog_rum_cumulative_layout_shift` | gauge | `device`, `host`, `site` | Cumulative Layout Shift p75 (unitless), real browsers only |
| `cflog_loki_push_total` | counter | `status` | Loki push attempts by outcome |
| `cflog_loki_push_duration_seconds` | histogram | | Loki push latency |
| `cflog_build_info` | gauge | `version`, `go_version` | Build metadata |

## Loki Streams

These log streams are pushed to Loki and can be queried in Grafana:

| Stream | Labels | Content |
|--------|--------|---------|
| Firewall events | `{job="cloudflare", type="firewall", zone="..."}` | One JSON log line per firewall event |
| HTTP traffic | `{job="cloudflare", type="http_traffic", zone="..."}` | One JSON log line per traffic group |
| HTTP visits | `{job="cloudflare", type="http_visits", zone="..."}` | One JSON log line per host and minute with visits |
| DNS queries | `{job="cloudflare", type="dns", zone="..."}` | One JSON log line per DNS query group |
| Audit events | `{job="cloudflare", type="audit", account="..."}` | One JSON log line per audit event |
| RUM page loads | `{job="cloudflare", type="rum_pageload", site="..."}` | One JSON log line per page-load group |
