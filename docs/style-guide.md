# Style Guide

**Author:** Alex Freidah

---

## Table of Contents

- [Core Principles](#core-principles)
- [Comment Types and Spacing](#comment-types-and-spacing)
- [File Headers](#file-headers)
- [Go Conventions](#go-conventions)
- [Project Structure and Layers](#project-structure-and-layers)
- [Error Handling](#error-handling)
- [Logging](#logging)
- [Tracing](#tracing)
- [Metrics](#metrics)
- [Testing](#testing)
- [Code Style](#code-style)
- [Versioning](#versioning)
- [Documentation Updates](#documentation-updates)
- [Branch Naming](#branch-naming)

---

## Core Principles

- **ASCII-only characters** - Never use Unicode em-dashes, en-dashes, or box-drawing characters
- **Dashes, not equals** - Always use `-` for dividers, never `=`
- **Box comment spacing** - ALL box comments (79-char file headers and 73-char sections) ALWAYS have a blank line after
- **Professional tone** - No personal references, no numbered lists, no casual language
- **Self-documenting** - Code explains *why*, not just *what*
- **Godoc-compliant comments** - Every type, function, and method gets a comment, including unexported ones
- **Accept interfaces, return structs** - Producers export concrete `*Type` values; each consumer declares its own narrow interface naming only the methods it calls (see [Interface Design](#interface-design-consumer-declared-interfaces))
- **Context propagation** - Pass `context.Context` through all function chains for cancellation, tracing, and log correlation

---

## Comment Types and Spacing

### File Header (79 characters)

**Format:**
```go
// -------------------------------------------------------------------------------
// Title of File or Component
//
// Author: <creator>   (or "Authors: <a>, <b>" when more than one has contributed)
//
// 2-4 sentence description of the file's purpose, scope, and key functionality.
// Include architecture notes, design decisions, or important context that helps
// readers understand the overall purpose.
// -------------------------------------------------------------------------------

package mypackage
```

**Spacing Rules:**
- Blank line after title
- Blank line after metadata
- Blank line before closing divider
- **Blank line after closing divider** - always separate box from code

### Major Section Box (73 characters)

**Format:**
```go
// -------------------------------------------------------------------------
// SECTION NAME
// -------------------------------------------------------------------------

func doSomething() {
    // ...
}
```

**Spacing Rules:**
- Use ALL CAPS for section name
- **Blank line AFTER closing divider** - separates section from code
- Used for major logical divisions (e.g., CLIENT, QUERIES, TYPES)

### Single-Line Comments

Standard Go comments placed directly above the code they describe:

```go
// Parse config file
cfg, err := config.LoadConfig(path)
if err != nil {
    return err
}
```

- **NO blank line before code** - placed directly above the block
- Use lowercase or sentence case
- Used for minor divisions or labels within functions

### Inline Comments

```go
entries[i] = []string{e.Timestamp, e.Line} // [nanos, json]
```

- Use sparingly
- Explain *why*, not *what*
- Keep concise (< 50 characters)

---

## Comment Type Decision Tree

```
Is this a file header?
  YES -> Use 79-char divider, blank line AFTER

Is this a major section (types, public API, internals)?
  YES -> Use 73-char box, blank line AFTER

Is this a minor division or label within a function?
  YES -> Use a standard single-line comment, NO blank line before code

Is this explaining a specific line?
  YES -> Use inline comment
```

**Key Rule:** ALL box comments (79-char and 73-char) have a blank line after. Single-line comments have no extra spacing.

---

## File Headers

Every `.go` file starts with a 79-char header block:

```go
// -------------------------------------------------------------------------------
// Cloudflare API Client
//
// Authors: Alex Freidah, Aaron Florey
//
// HTTP client for the Cloudflare APIs. Queries firewall events and HTTP traffic
// statistics via the GraphQL Analytics API, and account audit logs via the REST
// Audit Logs API. Handles rate limiting with exponential backoff and seek-based
// pagination via datetime filters.
// -------------------------------------------------------------------------------

package cloudflare
```

**Rules:**
- Use `//` comments (not `/* */` blocks)
- Title line describes the file's scope, not the package
- Description covers purpose, key behaviors, and dependencies
- The `package` declaration follows immediately after the closing divider + blank line
- **Author line** - the creator of a new file is its `Author:`. When you make a
  substantive change to an existing file, append your name: `Authors: <prior>, <you>`.
  Trivial touches (dependency bumps, formatting, mechanical renames) don't earn a
  credit; git history is the authoritative record.

---

## Go Conventions

### Indentation

- **1 tab** - Go standard (`gofmt` enforced)

### Imports

Group imports in three blocks separated by blank lines:

```go
import (
    "context"
    "fmt"
    "time"

    "github.com/afreidah/cloudflare-log-collector/internal/metrics"
    "github.com/afreidah/cloudflare-log-collector/internal/telemetry"

    "go.opentelemetry.io/otel/attribute"
    "github.com/prometheus/client_golang/prometheus"
)
```

Order: stdlib, internal packages, external packages.

### Naming

- **All types, functions, and methods** get godoc-compliant comments, even unexported ones
- **Constants** grouped by concern with `const` blocks, named in `CamelCase`
- **Sentinel errors** use `Err` prefix: `ErrConfigInvalid`, `ErrMissingToken`

### Struct Organization

Group related fields with inline comments explaining non-obvious fields:

```go
type AuditCollector struct {
    cf           auditQuerier
    loki         logPusher
    accountID    string
    accountName  string
    pollInterval time.Duration
    lastSeen     time.Time
    batchSize    int

    // shippedAtCursor holds the IDs of events already shipped at the lastSeen
    // boundary, so they can be dropped if the API returns them again.
    shippedAtCursor map[string]struct{}
}
```

### Interface Design: Consumer-Declared Interfaces

This codebase follows the Go-idiomatic "accept interfaces, return structs" pattern: **producer packages export concrete `*Type` values with no producer-side interface**, and **each consumer declares its own narrow interface** listing only the methods it actually calls. The concrete type satisfies every consumer's local interface because Go interfaces are structurally typed.

The producers are `cloudflare.Client` and `loki.Client` - both exported as concrete `*Client` pointers. The consumers are the collectors (`internal/collector`) and the service supervisor (`internal/lifecycle`). `lifecycle.Runner` / `lifecycle.Stopper` are the original example of the pattern in this repo; the collector interfaces in `internal/collector/consumer_interfaces.go` follow it. The same pattern applies one level down: `cloudflare.Client` and `loki.Client` are themselves consumers of `*http.Client`, and each declares a narrow `httpDoer` interface (the single `Do` method) so tests can inject a fake transport.

**Rationale:**
- A consumer's dependency footprint is documented in its own source file.
- Adding a method to a producer (`cloudflare.Client`, `loki.Client`) never bloats existing consumer mocks.
- Tests can mock at the granularity of what is used (one or two methods), not the full producer surface.
- Aligns with Rob Pike's "accept interfaces, return structs" guideline.

**Trade-offs:**
- Each consumer declares its own small interface (extra text, but localized).
- The composition layer (`cmd/cloudflare-log-collector/main.go`) still holds concrete types - it owns construction and is the seam where interfaces meet implementations.

**Where the interfaces live:**

| Location | Holds |
|---|---|
| `internal/collector/consumer_interfaces.go` | The narrow interfaces each collector declares against `cloudflare.Client` and `loki.Client` |
| `internal/lifecycle/manager.go` | The `Runner` (`Run`) and optional `Stopper` (`Stop`) interfaces the manager supervises |
| `internal/cloudflare/client.go`, `internal/loki/client.go` | The `httpDoer` (`Do`) interface each client declares against `*http.Client` for transport-error testing |

**Naming convention.** Single-method interfaces follow the Go `-er` convention: name the interface after its method in agent-noun form. `Push` -> `logPusher`, `QueryFirewallEvents` -> `firewallQuerier`. A composite that unions narrow interfaces is named after the producer concept it stands in for (`cloudflareQuerier`). Names ending in `-Ops`, `-Store`, `-ing`, or other shapes that do not describe an actor get flagged by SonarCloud (rule S8196) and should be renamed.

**Constructor shape.** Consumers store and accept the interfaces, not concrete pointers. The composition root passes the concrete `*cloudflare.Client` / `*loki.Client`, which satisfy the interfaces implicitly. Because the firewall and HTTP collectors share one `CollectorConfig` bag, the bag's `CF` field is the composite `cloudflareQuerier`; each collector narrows it to the single querier it calls when storing the field (interface-to-interface assignment is legal when the source method set is a superset).

```go
// internal/collector/consumer_interfaces.go
type firewallQuerier interface {
    QueryFirewallEvents(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.FirewallEvent, error)
}

type logPusher interface {
    Push(ctx context.Context, labels map[string]string, entries []loki.Entry) error
}

// cloudflareQuerier composes the zone-scoped queriers for the shared CollectorConfig.
type cloudflareQuerier interface {
    firewallQuerier
    httpQuerier
}

// internal/collector/firewall.go
type FirewallCollector struct {
    cf   firewallQuerier // narrowed from CollectorConfig.CF
    loki logPusher
    // ...
}
```

**Mocking.** Mocks are not generated eagerly. The existing tests inject the real `*cloudflare.Client` (via `cloudflare.NewTestClient`) and `*loki.Client` backed by `httptest` servers - the concrete types satisfy the interfaces, so the seam costs the tests nothing. When a test genuinely benefits from a hand-rolled fake (driving an error path the httptest server cannot easily produce, or asserting call arguments without HTTP), satisfy the narrow interface directly. Until a fake is needed, the interface declaration alone documents the dependency surface.

**When NOT to declare a narrow interface.** A consumer-side interface earns its keep when at least one of these is true:

1. **A test fake genuinely benefits from the seam** - exercising the consumer without standing up the real producer.
2. **An import cycle would otherwise form.**
3. **The interface models a real domain boundary** between subsystems (the collector's view of "fetch Cloudflare data" and "ship logs" are such boundaries).

If none apply - single impl, single consumer, no test fake, no cycle, no boundary - pass the concrete `*Type` directly. Do not add an interface for its own sake.

**Producer-side interfaces are an anti-pattern.** `cloudflare.Client` and `loki.Client` are exported as concrete pointer types with no sibling `Client` interface mirroring their public surface. A producer-side interface forces every consumer to mock the full producer API, which is exactly what this pattern avoids.

**Logger is not a behavior dependency.** Never include a logger accessor in a consumer-declared interface. Logging is observability infrastructure with no return value the consumer depends on. This repo logs through the package-global `slog.Default()` (wired with the `TraceHandler` in `main.go`), so collectors call `slog.InfoContext(ctx, ...)` directly and no logger is threaded through any interface.

### Constructor Patterns

**Config and Deps bags.** A constructor with **four or more parameters**, or with two or more same-typed primitives whose call-site order is ambiguous, takes a single config/deps struct instead of a positional list - named fields document each argument and make transposition impossible. `CollectorConfig` and `AuditCollectorConfig` are the examples: each carries the client interfaces, the zone/account identity, and the polling parameters. `context.Context` is never stored in such a struct; it stays the first positional argument of the methods that need it (`Run(ctx)`, `poll(ctx)`).

**Composition root, no DI framework.** `cmd/cloudflare-log-collector/main.go` is the single composition root. It constructs the concrete `*cloudflare.Client` and `*loki.Client` once, builds a `CollectorConfig` per zone, and registers each collector with the `lifecycle.Manager`. There is no dependency-injection container; wiring is explicit and read top-to-bottom in `main`.

**Validation lives in config, not constructors.** Constructors here do not panic on nil and do not re-validate - operator-facing invariants (missing API token, empty zone list, missing Loki endpoint) are checked once in `config.setDefaultsAndValidate`, which gathers errors with `errors.Join` and returns them so `main` can report and exit. A constructor trusts that `main` passed it validated, non-nil dependencies.

### Variable Naming

Avoid shadowing package imports with local variable names. When a function works with a value whose type comes from an imported package, pick a short name that does not collide with the package identifier:

```go
// Good - does not shadow the loki package
for _, e := range entries {
    values = append(values, []string{e.Timestamp, e.Line})
}

// Bad - a local named `loki` would shadow the loki import for the rest of scope
```

### Typed Constants

Use typed string constants for values compared or emitted in multiple places. The dataset names (`"firewall"`, `"http"`, `"audit"`) appear as metric label values, span attributes, and service-registration keys; bare string literals at each site are error-prone and drift apart over time:

```go
type Dataset string

const (
    DatasetFirewall Dataset = "firewall"
    DatasetHTTP     Dataset = "http"
    DatasetAudit    Dataset = "audit"
)
```

### No Empty Cleanup Funcs

Never return `func() {}` to satisfy a `(T, cleanup func(), error)` signature when the "no-cleanup" branch has nothing to do. SonarCloud flags empty function literals (rule `go:S1186`), and the empty literal hides whether the branch *meant* to be empty or someone forgot to wire the cleanup. Attach the cleanup as a method on the returned type that internally branches on which resource (if any) needs releasing, so the caller writes `defer x.Cleanup()` once and the lint never sees an empty literal. When the cleanup must be a callback to satisfy an interface, wrap it in `sync.OnceFunc` and have it do something meaningful so the body is never literally empty.

### Concurrency Patterns

- **Context-scoped cancellation** via `context.WithCancel` for background collectors
- **Lifecycle manager** (`internal/lifecycle`) for supervised goroutines with panic recovery and auto-restart
- **Ticker-driven polling** - each collector runs an initial poll, then a `time.NewTicker` loop that selects on `ctx.Done()` and the tick
- **Graceful shutdown** via signal handling with ordered service teardown and trace flushing

---

## Project Structure and Layers

The codebase splits along clear layers. New code goes in the layer whose
responsibility matches; the producer clients and config must never import the
collectors or `main`.

```
cmd/cloudflare-log-collector/   # Binary entry point and composition root: config load,
                                # logger/tracer init, client construction, collector
                                # registration, metrics/health server, signal handling
internal/
  config/                       # YAML + ${ENV} config: types, defaults, validation
  cloudflare/                   # Cloudflare GraphQL + REST Audit Logs client (producer)
  loki/                         # Loki push API client (producer)
  collector/                    # Pollers: firewall, http, audit. Consumer interfaces in
                                # consumer_interfaces.go; shared cursor helpers in cursor.go
  lifecycle/                    # Background service supervisor (Runner / Stopper)
  metrics/                      # Prometheus metric definitions (promauto)
  telemetry/                    # OTel tracer init, span helpers, slog TraceHandler
```

### Layer Responsibilities

| Layer | Imports | Responsibility |
|---|---|---|
| `cmd/` | Everything | Composition root: load config, build clients, register collectors, run servers |
| `collector/` | `cloudflare`, `loki`, `metrics`, `telemetry` | Poll a dataset, ship to Loki, update metrics, advance the seek cursor |
| `cloudflare/`, `loki/` | `telemetry` (and `metrics` for loki) | Producer clients: HTTP, retry/backoff, response parsing, client spans |
| `lifecycle/` | nothing app-specific | Supervise goroutines: panic recovery, restart, ordered stop |
| `metrics/` | nothing app-specific | Prometheus metric variables |
| `telemetry/` | `config` | Tracer setup, span helpers, trace-to-log correlation handler |
| `config/` | nothing app-specific | Config types, env expansion, defaults, validation |

The producer clients (`cloudflare`, `loki`) and `config` must not import the
collectors. The compiler does not enforce the full layering, so reviewers must -
it is the rule that keeps the dependency graph acyclic and the code testable.

---

## Error Handling

### Wrapped Errors

Use `fmt.Errorf` with `%w` to wrap errors with context:

```go
if err := json.Unmarshal(body, &resp); err != nil {
    return nil, fmt.Errorf("parse graphql response: %w", err)
}
```

### Retry and Backoff

Both producer clients retry transient failures (HTTP 429, 502, 503, 504) with
exponential backoff up to `maxRetries`, honoring the `Retry-After` header when
present. The request is rebuilt for every attempt so no per-request state leaks
across retries, and the retry sleep selects on `ctx.Done()` so a cancelled
context aborts the wait. Non-retryable statuses and exhausted retries return the
last response for the caller to interpret.

### Span Error Recording

Record errors on OpenTelemetry spans for visibility in Tempo:

```go
span.RecordError(err)
span.SetStatus(codes.Error, err.Error())
```

### Background Operation Errors

Background collectors log errors and continue rather than crashing. Individual
poll failures are logged with `slog.ErrorContext` and the next poll cycle
proceeds normally; the cursor is not advanced on failure so the window is
retried. The lifecycle manager additionally recovers panics and restarts the
service after a brief delay.

---

## Logging

All logging uses `log/slog` with JSON output to stdout. The logger wraps a
`TraceHandler` (`internal/telemetry/tracehandler.go`) that automatically injects
`trace_id` and `span_id` from the active OpenTelemetry span into every record,
enabling one-click navigation between Loki logs and Tempo traces in Grafana.

```go
jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: &logLevel})
traceHandler := telemetry.NewTraceHandler(jsonHandler)
slog.SetDefault(slog.New(traceHandler))
```

### Log-Trace Correlation

Use the context-aware slog variants - `slog.InfoContext(ctx, ...)`,
`WarnContext`, `ErrorContext` - inside any code that carries a span, so the
trace context propagates into log output. Never use the context-free
`slog.Info(...)` when an active span exists; that produces a log line with no
trace correlation.

### Log Levels

| Level | Use |
|-------|-----|
| `slog.Info` | Startup, shutdown, successful poll results, Loki push confirmations |
| `slog.Warn` | Recoverable failures (marshal errors for individual events, retryable API status, unparseable cursor timestamps) |
| `slog.Error` | Poll failures, Loki push failures, server errors |
| `slog.Debug` | Empty poll results, detailed operational state |

---

## Tracing

OpenTelemetry tracing is wired in `internal/telemetry/tracing.go` with OTLP gRPC
export to Tempo. Every poll cycle creates a root span; each outbound client call
(Cloudflare query, Loki push) creates a child client span, so a single poll is
one trace from collector through to each backend.

### Starting a Span

Use the package helpers rather than the OTel API directly. `StartSpan` opens an
internal span for a poll cycle; `StartClientSpan` opens a `SpanKindClient` span
for outbound calls - the client kind is required for Tempo's service graph to
detect service-to-service edges. Always `defer span.End()` on the line after the
call so an early return cannot leave the span open.

```go
ctx, span := telemetry.StartSpan(ctx, "firewall.poll",
    telemetry.AttrDataset.String("firewall"),
    attribute.String("cflog.zone", c.zoneName),
)
defer span.End()
```

### Span Naming

Span names are stable strings (no per-request data) so traces aggregate cleanly:

| Kind | Pattern | Example |
|---|---|---|
| Poll cycle | `<dataset>.poll` | `firewall.poll`, `http.poll`, `audit.poll` |
| Cloudflare call | `cloudflare.<api>` | `cloudflare.graphql`, `cloudflare.audit_logs` |
| Loki push | `loki.push` | `loki.push` |

### Attributes

Reusable attribute keys live in `internal/telemetry/tracing.go` (`AttrDataset`,
`AttrEventCount`) so every span uses the same string; prefer adding a key there
over inlining a literal at the call site. Custom attribute keys use the `cflog.`
prefix (`cflog.zone`, `cflog.zone_id`, `cflog.event_count`, `cflog.account_id`).
Outbound client spans also set the OTel semantic keys `peer.service` and
`server.address`, and record `http.status_code` after the call completes.

High-cardinality per-event values (client IP, ray name, request path, user
agent) belong in the Loki log line body, never as span attributes.

### Recording Errors

Record span errors with the `RecordError` + `SetStatus` pair so trace UIs flag
the span as failing. The accompanying `slog.ErrorContext` log line carries the
error text and the trace correlation links the two:

```go
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())
    return err
}
```

---

## Metrics

Prometheus metrics live in `internal/metrics/metrics.go`, defined with `promauto`
so they register with the default registry on package init; nothing else is
needed to make them visible at `/metrics`. Group metrics by concern with 73-char
section boxes (POLL METRICS, EVENT METRICS, LOKI METRICS, BUILD INFO).

### Naming

| Type | Format | Example |
|---|---|---|
| Counter | `cflog_<noun>_total{labels}` | `cflog_poll_total{dataset,zone,status}` |
| Gauge | `cflog_<noun>{labels}` | `cflog_http_requests{method,status,country,zone}` |
| Histogram | `cflog_<noun>_seconds` or `_bytes` | `cflog_poll_duration_seconds` |

The `cflog_` prefix is mandatory so a multi-service Prometheus instance can pick
out collector metrics with one label match. Counters end in `_total`; histograms
end in `_seconds` or `_bytes`; gauges take no unit suffix.

### Label Cardinality

Labels multiply storage cost. Hard rules:

- **Allowed labels**: `dataset`, `zone`, `status`, `action`, `account`,
  `method`, `country`, `type` - each drawn from a small fixed set.
- **Forbidden labels**: client IP, ray name, request path, user agent, rule ID,
  or anything user-supplied. These create unbounded cardinality and will
  eventually crash Prometheus - they go in the Loki log body instead.
- The `zone` label doubles as the account name for the audit dataset, since
  audit logs are scoped per account rather than per zone (see the note in
  `metrics.go`).

### Updating a Metric

Counters use `Inc`/`Add`, gauges use `Set`, histograms use `Observe`. Always
pass label values in the order they appear in the metric declaration; Prometheus
does not protect against argument-order swaps. Per-poll gauges
(`cflog_http_requests`, `cflog_http_bytes`) are reset for the current zone via
`DeletePartialMatch` before being repopulated, so they reflect only the latest
poll window.

---

## Testing

### Unit Tests

- Test files live alongside the code they test: `client_test.go`, `http_test.go`
- Use `httptest.NewServer` to mock external APIs (Cloudflare, Loki)
- Construct collectors through `CollectorConfig` / `AuditCollectorConfig`, injecting `cloudflare.NewTestClient(server.URL, token)` and `loki.NewClient(server.URL, tenant)` - both satisfy the consumer interfaces
- Test names follow `TestFunctionName_Scenario` convention
- Use standard `testing.T` methods, not external assertion libraries

### Test Patterns

- **httptest servers** mock the Cloudflare GraphQL/REST APIs and the Loki push API
- **Helper functions** use `t.Helper()` for reusable setup (e.g., `mockCFServer`, `mockLokiServer`)
- **Cleanup** via `t.Cleanup(ts.Close)` for test server teardown
- **Temporary files** via `t.TempDir()` for config file tests
- **Consumer-interface fakes** are written by hand only when an httptest server cannot easily drive the path under test; satisfy the narrow interface directly rather than generating a mock

### Coverage Exclusions

The following are excluded from coverage analysis in `sonar-project.properties`
as untestable wiring:

- `cmd/` - process entry point with `os.Exit`
- `internal/metrics/` - metric definitions
- `internal/telemetry/` - OTel wiring
- `internal/lifecycle/` - thin supervision glue

---

## Code Style

### Character Rules

**ALWAYS USE:**
- ASCII dash: `-` (hyphen-minus, U+002D)
- Standard ASCII characters only

**NEVER USE:**
- Unicode em-dash (U+2014)
- Unicode en-dash (U+2013)
- Unicode box-drawing (U+2500)
- Equals signs for dividers

### Professional Tone

Avoid:
- Personal references: "Let me show you...", "We need to..."
- Numbered lists in comments: "1. First do this", "2. Then do that"
- Conversational tone: "Now we're going to..."
- Future tense: "This will create...", "We'll configure..."

Use:
- Present tense: "Creates", "Configures", "Manages"
- Declarative statements: "Service runs on port 9101"
- Technical precision: "Uses OTLP gRPC for trace export"
- Impersonal voice: "The collector polls...", "The handler injects..."

---

## Versioning

Versioning and releases are automated by
[release-please](https://github.com/googleapis/release-please) - there is no
manual version file. The version is baked into the binary at build time via
`-ldflags "-X .../internal/telemetry.Version=<v>"` and surfaced through
`cflog_build_info` and the `version` subcommand.

release-please reads [Conventional Commits](https://www.conventionalcommits.org/)
on `main` and maintains a release PR that bumps the version and updates
`CHANGELOG.md`. Merging that PR tags the release and runs GoReleaser. Commit
prefixes drive the bump (pre-1.0, with `bump-minor-pre-major` set):

| Prefix | Effect |
|---|---|
| `fix:` | Patch bump, "Bug Fixes" changelog entry |
| `feat:` | Minor bump, "Features" changelog entry |
| `feat!:` / `BREAKING CHANGE:` | Minor bump pre-1.0, flagged as breaking |
| `chore:`, `docs:`, `test:`, `refactor:`, `ci:` | No release on their own |

Write commit subjects in the imperative and scope them when useful
(`fix(audit): ...`). The commit message is the source of the changelog, so make
it describe the user-visible change.

---

## Documentation Updates

When a change touches config fields, metrics, Loki streams, or deployment
requirements, update all affected documentation in the same change:

- `README.md` - config reference, Prometheus metrics table, Loki streams table
- `packaging/config.example.yaml` - sample configuration
- `web/content/docs/*.md` - project website pages (architecture, readme, grafana, changelog)
- `grafana/cloudflare-log-collector.json` - dashboard, if a metric is operator-facing
- `docs/style-guide.md` - this file, when a convention changes

Search across docs before committing to catch every reference:

```
grep -rn 'field_name' README.md docs/ web/content/ packaging/ grafana/
```

---

## Branch Naming

When a branch corresponds to a GitHub issue, use this format:

```
GH_ISSUE_<issue number>-<description of topic>
```

Examples:
- `GH_ISSUE_12-add-country-metrics`
- `GH_ISSUE_5-loki-retry-logic`

For branches without a linked issue, use a short kebab-case description of the topic.

---

## Quick Reference

| Comment Type | Length | Spacing After | Use Case |
|-------------|--------|---------------|----------|
| File header | 79 chars | 1 blank line | Top of every `.go` file |
| Major section | 73 chars | 1 blank line | Major divisions (types, API, internals) |
| Single-line comment | Variable | None | Minor divisions within functions |
| Inline | Brief | N/A | Specific line explanation |

---

## Examples

### Good

```go
// -------------------------------------------------------------------------------
// HTTP Traffic Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare httpRequestsAdaptiveGroups dataset on a configurable
// interval. Updates Prometheus gauges with aggregated traffic statistics and
// ships raw traffic groups to Loki as structured JSON logs.
// -------------------------------------------------------------------------------

package collector

// -------------------------------------------------------------------------
// HTTP COLLECTOR
// -------------------------------------------------------------------------

// HTTPCollector polls Cloudflare for HTTP traffic stats, updates Prometheus
// gauges, and ships raw traffic data to Loki.
type HTTPCollector struct {
    cf           httpQuerier
    loki         logPusher
    pollInterval time.Duration
    lastSeen     time.Time
    batchSize    int
}

// poll executes a single HTTP traffic collection cycle within a traced span.
func (c *HTTPCollector) poll(ctx context.Context) {
    ctx, span := telemetry.StartSpan(ctx, "http.poll",
        telemetry.AttrDataset.String("http"),
    )
    defer span.End()

    groups, err := c.cf.QueryHTTPRequests(ctx, c.zoneID, c.lastSeen, time.Now().UTC())
    if err != nil {
        slog.ErrorContext(ctx, "HTTP traffic poll failed", "zone", c.zoneName, "error", err)
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
        return
    }
    // ...
}
```

### Bad

```go
// ==================================
// HTTP Traffic Collector
//
// This module will handle HTTP traffic collection for the user.
// Here's how it works:
// 1. First we poll Cloudflare
// 2. Then we update metrics
// 3. Finally we push to Loki
// ==================================

package collector

// Let's create the collector struct
type HTTPCollector struct {
    cf           *cloudflare.Client // producer-side concrete type leaks into the consumer
    // ...
}
```

---

**Remember:** Comments should explain *why* decisions were made, not *what* the code does. The code itself should be clear enough to understand *what* it does. Every type, function, and method must have a godoc-compliant comment.
