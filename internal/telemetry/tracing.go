// -------------------------------------------------------------------------------
// Tracing - OpenTelemetry Instrumentation
//
// Author: Alex Freidah
//
// OpenTelemetry tracer setup with OTLP gRPC export to Tempo. Provides span
// helpers and context propagation for distributed tracing. Each poll cycle
// creates a root span with Cloudflare API and Loki push child spans.
// -------------------------------------------------------------------------------

// Package telemetry wires OpenTelemetry tracing and trace-correlated structured
// logging, including span helpers for Cloudflare and Loki operations.
package telemetry

import (
	"context"
	"fmt"

	"github.com/afreidah/cloudflare-log-collector/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// -------------------------------------------------------------------------
// CONSTANTS
// -------------------------------------------------------------------------

// TracerName identifies spans created by this service.
const TracerName = "cloudflare-log-collector"

// Version of the service for trace metadata. Set at build time via
// -ldflags "-X github.com/afreidah/cloudflare-log-collector/internal/telemetry.Version=..."
var Version = "dev"

// -------------------------------------------------------------------------
// TRACER SETUP
// -------------------------------------------------------------------------

// InitTracer initializes the OpenTelemetry tracer with OTLP export. Returns a
// shutdown function that should be called on service termination to flush spans.
func InitTracer(ctx context.Context, cfg config.TracingConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	// Create the OTLP exporter.
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Create the resource with service info.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(TracerName),
			semconv.ServiceVersion(Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Configure the sampler.
	var sampler sdktrace.Sampler
	switch {
	case cfg.SampleRate >= 1.0:
		sampler = sdktrace.AlwaysSample()
	case cfg.SampleRate <= 0:
		sampler = sdktrace.NeverSample()
	default:
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// Create the trace provider.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// -------------------------------------------------------------------------
// SPAN HELPERS
// -------------------------------------------------------------------------

// Tracer returns the global tracer for this service.
func Tracer() trace.Tracer {
	return otel.Tracer(TracerName)
}

// StartSpan creates a new span with the given name and attributes.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

// StartClientSpan creates a span with SpanKindClient for an outbound call to
// peerService at serverAddress, plus any further attributes. Client spans
// carrying peer.service are what Tempo's service graph uses to draw
// service-to-service edges, so every outbound call names its peer here.
func StartClientSpan(ctx context.Context, name, peerService, serverAddress string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	attrs = append([]attribute.KeyValue{
		attribute.String("peer.service", peerService),
		attribute.String("server.address", serverAddress),
	}, attrs...)
	return Tracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
}

// FailSpan marks span as failed by err: the error is recorded as a span event
// and the span status is set to Error with err's message, so the span shows as
// failed in Tempo and carries the cause.
func FailSpan(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// -------------------------------------------------------------------------
// COMMON ATTRIBUTES
// -------------------------------------------------------------------------

// Span attribute keys shared across the codebase. AttrDataset names the
// dataset a span works on. AttrEventCount and AttrGroupCount record how many
// rows a poll returned: events for event datasets (firewall, audit), groups
// for aggregated ones (HTTP traffic, RUM).
var (
	AttrDataset    = attribute.Key("cflog.dataset")
	AttrEventCount = attribute.Key("cflog.event_count")
	AttrGroupCount = attribute.Key("cflog.group_count")
)
