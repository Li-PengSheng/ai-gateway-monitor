// Package telemetry configures OpenTelemetry tracing for go-gateway.
package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// InitTracer installs a global TracerProvider that exports spans via OTLP/gRPC
// to jaegerEndpoint (host:port, insecure).
//
// Parameters:
//   - ctx: used when creating the OTLP exporter
//   - jaegerEndpoint: OTLP collector address (e.g. "localhost:4317")
//
// Returns the TracerProvider (caller must Shutdown on process exit) or an
// error if the exporter cannot be created.
//
// Side effects: sets the global otel TracerProvider and TraceContext propagator.
// gRPC client spans are created by otelgrpc on the dial path; Gin/HTTP is not
// auto-instrumented in this service.
func InitTracer(ctx context.Context, jaegerEndpoint string) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(jaegerEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("go-gateway"),
		)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, nil
}
