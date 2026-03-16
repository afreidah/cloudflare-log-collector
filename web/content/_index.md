---
title: "cloudflare-log-collector"
archetype: "home"
description: "Cloudflare analytics collector for self-hosted observability stacks"
---

<div style="text-align: center; margin-bottom: -3rem;">
  <img src="/images/logo.png" alt="cloudflare-log-collector" style="max-width: 650px; height: auto;">
</div>

<div class="badge-grid">

{{% badge style="primary" icon="fas fa-shield-alt" %}}Firewall Events{{% /badge %}}
{{% badge style="info" title=" " icon="fas fa-chart-line" %}}HTTP Traffic Stats{{% /badge %}}
{{% badge style="danger" icon="fas fa-fire" %}}Prometheus Metrics{{% /badge %}}
{{% badge style="green" icon="fas fa-stream" %}}Loki Log Streams{{% /badge %}}
{{% badge style="warning" title=" " icon="fas fa-project-diagram" %}}OpenTelemetry Tracing{{% /badge %}}

</div>

<div style="text-align: center; margin-top: 1rem;">

{{% button href="docs/readme/" style="primary" icon="fas fa-book" %}}README{{% /button %}}
{{% button href="docs/architecture/" style="primary" icon="fas fa-project-diagram" %}}Architecture{{% /button %}}
{{% button href="godoc/" style="primary" icon="fas fa-code" %}}Go API{{% /button %}}
{{% button href="docs/grafana/" style="primary" icon="fas fa-chart-area" %}}Grafana{{% /button %}}
{{% button href="https://github.com/afreidah/cloudflare-log-collector" style="primary" icon="fab fa-github" %}}GitHub{{% /button %}}

</div>

<hr style="margin-top: 3rem;">

<h2 style="text-align: center; color: #38bdf8;">Cloudflare analytics for your self-hosted stack</h2>

A lightweight Go service that polls the Cloudflare GraphQL Analytics API for firewall events and HTTP traffic statistics, ships them into a self-hosted observability stack, and traces every poll cycle with OpenTelemetry.

<div class="hero-bullets">

- **Firewall events** are pushed to Loki as structured JSON log lines for querying in Grafana
- **HTTP traffic stats** are exposed as Prometheus gauges and also pushed to Loki for raw detail
- **Every poll cycle** gets its own OpenTelemetry trace with child spans, exported to Tempo via OTLP gRPC
- **Log-trace correlation** is automatic — `trace_id` and `span_id` are injected into every structured log line

</div>

<hr style="margin-top: 3rem;">

<h2 style="text-align: center; color: #38bdf8;">Key Features</h2>

<div class="feature-grid">
  <div class="feature-item">
    <div>
      <strong>Firewall Event Collection</strong>
      <p>Polls Cloudflare's firewallEventsAdaptive dataset for WAF events with full request detail.</p>
    </div>
    <div class="feature-detail">Captures action, client IP, host, method, path, query, ray ID, rule ID, source, user agent, and country. Each event becomes a structured JSON log line in Loki.</div>
  </div>
  <div class="feature-item">
    <div>
      <strong>HTTP Traffic Statistics</strong>
      <p>Aggregated request counts grouped by method, status code, and country.</p>
    </div>
    <div class="feature-detail">Polls httpRequestsAdaptiveGroups for traffic aggregates. Data is exposed as Prometheus gauges for dashboarding and also pushed to Loki for raw queryability.</div>
  </div>
  <div class="feature-item">
    <div>
      <strong>Prometheus Metrics</strong>
      <p>Rich metrics covering poll health, firewall events, HTTP traffic, and Loki push status.</p>
    </div>
    <div class="feature-detail">Exposes poll counters and histograms, firewall event counts by action, HTTP request gauges by method/status/country, Loki push success/failure rates, and build info.</div>
  </div>
  <div class="feature-item">
    <div>
      <strong>Loki Integration</strong>
      <p>Pushes structured JSON log streams directly to Loki's push API.</p>
    </div>
    <div class="feature-detail">Two log streams: firewall events and HTTP traffic, each with distinct labels. Supports multi-tenant Loki via configurable X-Scope-OrgID header. Automatic batching and retry with exponential backoff.</div>
  </div>
  <div class="feature-item">
    <div>
      <strong>OpenTelemetry Tracing</strong>
      <p>Every poll cycle is traced end-to-end with child spans for API calls and Loki pushes.</p>
    </div>
    <div class="feature-detail">Exports traces to Tempo via OTLP gRPC with configurable sampling rate. Each trace captures the full poll lifecycle: Cloudflare API query, data transformation, Loki push, and metric updates.</div>
  </div>
  <div class="feature-item">
    <div>
      <strong>Log-Trace Correlation</strong>
      <p>Automatic trace_id and span_id injection into every structured log line.</p>
    </div>
    <div class="feature-detail">A custom slog handler injects OpenTelemetry context into all JSON log output. Enables one-click navigation between Loki logs and Tempo traces in Grafana.</div>
  </div>
</div>
