**Author:** Alex Freidah

---

## Table of Contents

- [Core Principles](#core-principles)
- [Comment Types and Spacing](#comment-types-and-spacing)
- [File Headers](#file-headers)
- [Go Conventions](#go-conventions)
- [Error Handling](#error-handling)
- [Logging and Tracing](#logging-and-tracing)
- [Testing](#testing)
- [Code Style](#code-style)
- [Branch Naming](#branch-naming)

---

## Core Principles

- **ASCII-only characters** - Never use Unicode em-dashes, en-dashes, or box-drawing characters
- **Dashes, not equals** - Always use `-` for dividers, never `=`
- **Box comment spacing** - ALL box comments (79-char file headers and 73-char sections) ALWAYS have a blank line after
- **Professional tone** - No personal references, no numbered lists, no casual language
- **Self-documenting** - Code explains *why*, not just *what*
- **Godoc-compliant comments** - Every type, function, and method gets a comment, including unexported ones
- **Context propagation** - Pass `context.Context` through all function chains for cancellation, tracing, and log correlation

---

## Comment Types and Spacing

### File Header (79 characters)

**Format:**
```go
// -------------------------------------------------------------------------------
// Title of File or Component
//
// Author: Alex Freidah
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
// Cloudflare GraphQL Client
//
// Author: Alex Freidah
//
// HTTP client for the Cloudflare GraphQL Analytics API. Queries firewall events
// and HTTP traffic statistics. Handles response parsing and seek-based
// pagination via datetime filters.
// -------------------------------------------------------------------------------

package cloudflare
```

**Rules:**
- Use `//` comments (not `/* */` blocks)
- Title line describes the file's scope, not the package
- Description covers purpose, key behaviors, and dependencies
- The `package` declaration follows immediately after the closing divider + blank line

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
type HTTPCollector struct {
    cf           *cloudflare.Client
    loki         *loki.Client
    pollInterval time.Duration
    lastSeen     time.Time
    batchSize    int
}
```

### Concurrency Patterns

- **Context-scoped cancellation** via `context.WithCancel` for background workers
- **Lifecycle manager** for supervised goroutines with panic recovery and auto-restart
- **Graceful shutdown** via signal handling with ordered service teardown

---

## Error Handling

### Wrapped Errors

Use `fmt.Errorf` with `%w` to wrap errors with context:

```go
if err := json.Unmarshal(body, &resp); err != nil {
    return nil, fmt.Errorf("parse graphql response: %w", err)
}
```

### Span Error Recording

Record errors on OpenTelemetry spans for visibility in Tempo:

```go
span.RecordError(err)
span.SetStatus(codes.Error, err.Error())
```

### Background Operation Errors

Background workers (collectors) log errors and continue rather than crashing. Individual poll failures are logged with `slog.ErrorContext` and the next poll cycle proceeds normally.

---

## Logging and Tracing

### Structured Logging

All logging uses `log/slog` with JSON output to stdout. The logger wraps a `TraceHandler` that automatically injects `trace_id` and `span_id` from the OpenTelemetry span context into every log record.

```go
jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: &logLevel})
traceHandler := telemetry.NewTraceHandler(jsonHandler)
slog.SetDefault(slog.New(traceHandler))
```

### Log-Trace Correlation

Use `slog.InfoContext(ctx, ...)` instead of `slog.Info(...)` to ensure the trace context propagates into log output. This enables one-click navigation between Loki logs and Tempo traces in Grafana.

### Log Levels

| Level | Use |
|-------|-----|
| `slog.Info` | Startup, shutdown, successful poll results, Loki push confirmations |
| `slog.Warn` | Recoverable failures (marshal errors for individual events) |
| `slog.Error` | Poll failures, Loki push failures, server errors |
| `slog.Debug` | Empty poll results, detailed operational state |

---

## Testing

### Unit Tests

- Test files live alongside the code they test: `client_test.go`, `http_test.go`
- Use `httptest.NewServer` to mock external APIs (Cloudflare, Loki)
- Test names follow `TestFunctionName_Scenario` convention
- Use standard `testing.T` methods, not external assertion libraries

### Test Patterns

- **httptest servers** mock Cloudflare GraphQL API and Loki push API responses
- **Helper functions** use `t.Helper()` for reusable verification (e.g., `verifyAuthHeader`, `assertGaugeValue`)
- **Cleanup** via `t.Cleanup(ts.Close)` for test server teardown
- **Temporary files** via `t.TempDir()` for config file tests

### Coverage Exclusions

Use `.codecov.yml` to exclude untestable code from coverage reports:
- `cmd/` - process entry points with `os.Exit`
- `internal/telemetry/` - OTel wiring, not unit-testable
- `internal/metrics/` - metric definitions, not unit-testable

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
- Declarative statements: "Service runs on port 9102"
- Technical precision: "Uses OTLP gRPC for trace export"
- Impersonal voice: "The collector polls...", "The handler injects..."

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
    cf           *cloudflare.Client
    loki         *loki.Client
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

    // Fetch traffic data from Cloudflare
    groups, err := c.cf.QueryHTTPRequests(ctx, c.lastSeen, until)
    if err != nil {
        slog.ErrorContext(ctx, "HTTP traffic poll failed", "error", err)
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
    cf           *cloudflare.Client
    // ...
}
```

---

**Remember:** Comments should explain *why* decisions were made, not *what* the code does. The code itself should be clear enough to understand *what* it does. Every type, function, and method must have a godoc-compliant comment.
