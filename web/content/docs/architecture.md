---
title: "Architecture"
weight: 5
---

<p class="landing-subheader">Data flow from Cloudflare's Analytics, Audit, and Web Analytics APIs through the collector into the observability stack</p>

<style>
.diagram-tooltip {
  display: none;
  position: fixed;
  background: #1e293b;
  border: 1px solid #38bdf8;
  border-radius: 8px;
  padding: 1rem 1.25rem;
  color: #e2e8f0;
  font-size: 0.9rem;
  line-height: 1.6;
  max-width: 360px;
  z-index: 1000;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
  pointer-events: none;
}
.diagram-tooltip strong {
  color: #38bdf8;
  font-size: 1rem;
}
.diagram-tooltip .detail {
  margin-top: 0.5rem;
  color: #94a3b8;
}
</style>

<div id="diagram-tooltip" class="diagram-tooltip"></div>

{{< mermaid >}}
flowchart TD
    CF["Cloudflare API"]
    POLL["Poll Scheduler"]
    FW["Firewall Collector"]
    HTTP["HTTP Collector"]
    AUDIT["Audit Collector"]
    RUM["RUM Collector"]
    METRICS["Metrics Exporter"]
    LOKIC["Loki Client"]
    TRACE["Trace Context"]
    SLOG["Structured Logger"]
    LOKI["Loki"]
    PROM["Prometheus"]
    TEMPO["Tempo"]
    GRAFANA["Grafana"]

    CF -->|"GraphQL + REST queries"| POLL
    POLL --> FW
    POLL --> HTTP
    POLL --> AUDIT
    POLL --> RUM
    FW -->|"JSON log lines"| LOKIC
    HTTP -->|"JSON log lines"| LOKIC
    AUDIT -->|"JSON log lines"| LOKIC
    RUM -->|"JSON log lines"| LOKIC
    FW -->|"event counters"| METRICS
    HTTP -->|"counter updates"| METRICS
    AUDIT -->|"event counters"| METRICS
    RUM -->|"counters + vitals gauges"| METRICS
    LOKIC -->|"POST /loki/api/v1/push"| LOKI
    METRICS -->|"/metrics"| PROM
    TRACE -->|"OTLP gRPC"| TEMPO
    SLOG -->|"trace_id injection"| TRACE

    LOKI --> GRAFANA
    PROM --> GRAFANA
    TEMPO --> GRAFANA

    classDef source fill:#0c2d48,stroke:#38bdf8,color:#e0f2fe
    classDef collector fill:#1e293b,stroke:#334155,color:#e2e8f0
    classDef sink fill:#132a1f,stroke:#22c55e,color:#dcfce7
    classDef viz fill:#2d2513,stroke:#f97316,color:#fef3c7

    class CF source
    class POLL,FW,HTTP,AUDIT,RUM,METRICS,LOKIC,TRACE,SLOG collector
    class LOKI,PROM,TEMPO sink
    class GRAFANA viz
{{< /mermaid >}}

<script>
document.addEventListener('DOMContentLoaded', function() {
  const nodeInfo = {
    'CF':      { title: 'Cloudflare API', detail: 'Zone-scoped GraphQL Analytics (firewallEventsAdaptive, httpRequestsAdaptiveGroups), account-scoped GraphQL RUM (rumPageloadEventsAdaptiveGroups, rumWebVitalsEventsAdaptiveGroups), and the REST Audit Logs API. Free plan supports ~24h lookback. Rate limit: 300 queries per 5 minutes.' },
    'POLL':    { title: 'Poll Scheduler', detail: 'Triggers collection on a configurable interval (default 5m). On startup, backfills up to the configured window (default 1h). Each enabled collector is supervised independently with panic recovery and restart, and wraps its poll in an OpenTelemetry trace.' },
    'FW':      { title: 'Firewall Collector', detail: 'Per zone. Queries firewallEventsAdaptive for individual WAF events. Captures action, client IP, host, method, path, query, ray ID, rule ID, source, user agent, and country. Warns if results hit the 10,000 event cap.' },
    'HTTP':    { title: 'HTTP Collector', detail: 'Per zone. Queries httpRequestsAdaptiveGroups for aggregated traffic stats grouped by method, status code, and country. Adds to cumulative Prometheus counters and pushes raw data to Loki. Warns at the 5,000 group cap.' },
    'AUDIT':   { title: 'Audit Collector', detail: 'Per account (optional). Queries the REST Audit Logs API for account audit events with cursor pagination, deduping the inclusive since-boundary by event ID. Ships each event to Loki and counts events by action type.' },
    'RUM':     { title: 'RUM Collector', detail: 'Per Web Analytics site (optional). Browser-side Real User Monitoring: page views and sessions become Loki rows and bounded counters (advancing cursor, counted once); Core Web Vitals (LCP, INP, FID, FCP, TTFB, CLS) p75 are gauges over a rolling window. Account-scoped, filtered by site tag; needs Account Analytics Read.' },
    'METRICS': { title: 'Metrics Exporter', detail: 'Exposes Prometheus metric families on the configured port (default :9101): poll health (counters, histograms, timestamps), firewall and audit event counts by action, HTTP request and byte counters, RUM page views and sessions, RUM Core Web Vitals gauges, Loki push status, and build info.' },
    'LOKIC':   { title: 'Loki Client', detail: 'Pushes structured JSON log lines to Loki\'s push API. Supports configurable batch size (default 100), multi-tenant X-Scope-OrgID header, and automatic retry with exponential backoff on transient failures (429, 502, 503, 504).' },
    'TRACE':   { title: 'Trace Context', detail: 'OpenTelemetry tracer exporting spans via OTLP gRPC to Tempo. Each poll cycle creates a root span with child spans for every API call, data transformation, and Loki push. Configurable sampling rate.' },
    'SLOG':    { title: 'Structured Logger', detail: 'Custom slog handler that injects trace_id and span_id from the active OpenTelemetry span into every JSON log line. Enables one-click navigation between Loki logs and Tempo traces in Grafana.' },
    'LOKI':    { title: 'Loki', detail: 'Log aggregation. Receives streams keyed by type: {type="firewall"}, {type="http_traffic"}, {type="audit"}, and {type="rum_pageload"}, all under job="cloudflare". Each entry is a structured JSON line queryable via LogQL.' },
    'PROM':    { title: 'Prometheus', detail: 'Scrapes /metrics endpoint. Provides poll health monitoring, firewall and audit event trending, HTTP traffic dashboarding, RUM page-view and Core Web Vitals tracking, and Loki push reliability.' },
    'TEMPO':   { title: 'Tempo', detail: 'Distributed tracing backend. Receives OTLP gRPC traces showing the full poll lifecycle with timing for each component.' },
    'GRAFANA': { title: 'Grafana', detail: 'Unified dashboard combining Loki log queries, Prometheus metric panels, and Tempo trace views. Ships with a pre-built dashboard JSON for import.' }
  };

  const tooltip = document.getElementById('diagram-tooltip');

  // Wait for Mermaid to render
  setTimeout(function() {
    document.querySelectorAll('.mermaid .node, .mermaid .nodeLabel').forEach(function(el) {
      var node = el.closest('.node') || el;
      var id = node.id || '';
      // Extract node ID from mermaid's generated ID
      var key = id.replace(/^flowchart-/, '').replace(/-\d+$/, '');

      if (!nodeInfo[key]) return;

      node.style.cursor = 'pointer';

      node.addEventListener('mouseenter', function(e) {
        var info = nodeInfo[key];
        tooltip.innerHTML = '<strong>' + info.title + '</strong><div class="detail">' + info.detail + '</div>';
        tooltip.style.display = 'block';
      });

      node.addEventListener('mousemove', function(e) {
        tooltip.style.left = (e.clientX + 16) + 'px';
        tooltip.style.top = (e.clientY + 16) + 'px';
      });

      node.addEventListener('mouseleave', function() {
        tooltip.style.display = 'none';
      });
    });
  }, 1000);
});
</script>

## Data Flow

### Poll Cycle

1. The **poll scheduler** supervises each enabled collector on a configurable interval (default 5 minutes), restarting any that panic or exit
2. Up to four collector types run, each wrapped in its own **OpenTelemetry span** for end-to-end trace visibility:
   - **Firewall collector** (per zone) queries `firewallEventsAdaptive` for individual WAF events
   - **HTTP collector** (per zone) queries `httpRequestsAdaptiveGroups` for aggregated traffic stats
   - **Audit collector** (per account, optional) queries the REST Audit Logs API for account audit events
   - **RUM collector** (per Web Analytics site, optional) queries `rumPageloadEventsAdaptiveGroups` and `rumWebVitalsEventsAdaptiveGroups` for browser-side page views and Core Web Vitals

### Firewall Events

- Each event becomes a **JSON log line** pushed to Loki under `{job="cloudflare", type="firewall"}`
- Event counts are tracked as **Prometheus counters** broken down by action type (block, challenge, allow)
- Fields captured: action, client IP, host, method, path, query, ray name, rule ID, source, user agent, country

### HTTP Traffic

- Aggregated groups are pushed to Loki under `{job="cloudflare", type="http_traffic"}` as JSON
- Request and edge-byte totals are accumulated into **cumulative Prometheus counters** labeled by method, status code, and country, so dashboards derive rates with `rate()` and stay immune to poll-window length

### Audit Events

- Each event becomes a **JSON log line** pushed to Loki under `{job="cloudflare", type="audit"}`
- Event counts are tracked as **Prometheus counters** by action type and account
- The REST API exposes only an inclusive `since` lower bound, so the collector dedupes the cursor boundary by event ID to avoid shipping an event twice

### RUM / Web Analytics

- Browser-side data is **account-scoped** and identified by a Web Analytics **site tag** (Web Analytics must be enabled on the zone; the token needs Account Analytics Read)
- **Page loads** advance a seek cursor and ship raw rows to Loki under `{job="cloudflare", type="rum_pageload"}`; bounded page-view and session **counters** (by country and device) are incremented only after a successful ship, so a page view is counted exactly once
- **Core Web Vitals** (LCP, INP, FID, FCP, TTFB) p75 are exposed as **Prometheus gauges** in seconds; CLS is a unitless score on its own gauge. Because quantiles cannot be accumulated, each poll reports the p75 over a fixed rolling window. The API's `-1` "no data" sentinel is dropped rather than emitted as a zero

### Observability

- **Prometheus**: metric families covering poll health, firewall and audit event counts, HTTP traffic counters, RUM page views and Core Web Vitals, Loki push status, and build info
- **Loki**: structured log streams keyed by `type` (`firewall`, `http_traffic`, `audit`, `rum_pageload`) for filtering
- **Tempo**: Full trace per poll cycle with child spans for each API call and Loki push
- **Log-trace correlation**: A custom slog handler injects `trace_id` and `span_id` into every log line, enabling one-click navigation between logs and traces in Grafana

### Resilience

- Both Cloudflare and Loki clients **retry on transient failures** (HTTP 429, 502, 503, 504) with exponential backoff up to 3 attempts
- `Retry-After` headers are honored when present
- On startup, the collector **backfills** up to the configured window (default 1 hour) to catch events from while it was down
