// -------------------------------------------------------------------------------
// Tracing Tests
//
// Author: Alex Freidah
//
// Tests for span creation helpers. Verifies that StartSpan creates INTERNAL
// spans (default) and StartClientSpan creates CLIENT spans for service graph
// visibility in Tempo.
// -------------------------------------------------------------------------------

package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// setupTestTracer installs an in-memory span exporter and returns it for
// inspection. Caller should defer the returned cleanup function.
func setupTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return exporter
}

// -------------------------------------------------------------------------
// START SPAN
// -------------------------------------------------------------------------

func TestStartSpan_CreatesInternalSpan(t *testing.T) {
	exporter := setupTestTracer(t)

	ctx, span := StartSpan(context.Background(), "test.internal",
		attribute.String("key", "value"),
	)
	span.End()

	_ = ctx
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}

	if spans[0].Name != "test.internal" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "test.internal")
	}
	if spans[0].SpanKind != trace.SpanKindInternal {
		t.Errorf("span kind = %v, want %v", spans[0].SpanKind, trace.SpanKindInternal)
	}
}

// -------------------------------------------------------------------------
// START CLIENT SPAN
// -------------------------------------------------------------------------

func TestStartClientSpan_CreatesClientSpan(t *testing.T) {
	exporter := setupTestTracer(t)

	ctx, span := StartClientSpan(context.Background(), "test.client",
		attribute.String("peer.service", "downstream"),
		attribute.String("server.address", "example.com"),
	)
	span.End()

	_ = ctx
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}

	if spans[0].Name != "test.client" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "test.client")
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("span kind = %v, want %v (CLIENT)", spans[0].SpanKind, trace.SpanKindClient)
	}

	// Verify peer.service attribute is present
	found := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "peer.service" && attr.Value.AsString() == "downstream" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected peer.service attribute on client span")
	}
}

func TestStartClientSpan_IsChildOfParent(t *testing.T) {
	exporter := setupTestTracer(t)

	ctx, parent := StartSpan(context.Background(), "parent.poll")
	_, child := StartClientSpan(ctx, "child.api_call",
		attribute.String("peer.service", "external-api"),
	)
	child.End()
	parent.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}

	// Child should reference parent's span ID
	childSpan := spans[0] // child ends first
	parentSpan := spans[1]

	if childSpan.Parent.SpanID() != parentSpan.SpanContext.SpanID() {
		t.Error("client span should be a child of the parent span")
	}
}
