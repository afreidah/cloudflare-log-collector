---
title: "Grafana Dashboard"
weight: 15
---

<p class="landing-subheader">Pre-built Grafana dashboard for monitoring Cloudflare analytics collection</p>

The dashboard tracks the full collection pipeline at a glance. **Poll health** panels show success/failure rates, latency histograms, and last-poll timestamps for each dataset and zone. **Firewall events** are broken down by action type (block, challenge, allow) with trending counters. **HTTP traffic** panels display client request rates by domain, method, status code, and country alongside bandwidth. **Per-service** panels split HTTP traffic by host: request rate, 5xx error rate, cache hit ratio, and requests where the origin was unreachable. **Audit** and **RUM** panels cover account audit actions, browser page views, and Core Web Vitals. **Loki push** panels monitor push success rates and latency to catch delivery issues early. Build info and version metadata round out the overview.

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
| `cflog_rum_pageviews_total` | counter | `country`, `device`, `site` | RUM page views |
| `cflog_rum_sessions_total` | counter | `country`, `device`, `site` | RUM sessions (entry page loads) |
| `cflog_rum_web_vital_seconds` | gauge | `vital`, `device`, `site` | Core Web Vitals p75 in seconds (lcp, inp, fid, fcp, ttfb) |
| `cflog_rum_cumulative_layout_shift` | gauge | `device`, `site` | Cumulative Layout Shift p75 (unitless) |
| `cflog_loki_push_total` | counter | `status` | Loki push attempts by outcome |
| `cflog_loki_push_duration_seconds` | histogram | | Loki push latency |
| `cflog_build_info` | gauge | `version`, `go_version` | Build metadata |

## Loki Streams

These log streams are pushed to Loki and can be queried in Grafana:

| Stream | Labels | Content |
|--------|--------|---------|
| Firewall events | `{job="cloudflare", type="firewall", zone="..."}` | One JSON log line per firewall event |
| HTTP traffic | `{job="cloudflare", type="http_traffic", zone="..."}` | One JSON log line per traffic group |
| Audit events | `{job="cloudflare", type="audit", account="..."}` | One JSON log line per audit event |
| RUM page loads | `{job="cloudflare", type="rum_pageload", site="..."}` | One JSON log line per page-load group |
