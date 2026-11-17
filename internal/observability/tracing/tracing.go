// Package tracing sets up OpenTelemetry tracing with an OTLP gRPC exporter.
package tracing

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Init installs the global tracer provider and W3C propagators, exporting to
// the OTLP gRPC endpoint (e.g. localhost:4317), and returns a shutdown func
// the caller must defer. An empty endpoint leaves tracing disabled.
func Init(ctx context.Context, endpoint, serviceName, version string) (func(context.Context) error, error) {
	if endpoint == "" {
		log.Printf("[tracing] disabled (no endpoint configured)")
		return func(context.Context) error { return nil }, nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	exporter, err := otlptracegrpc.New(connectCtx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("trace resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Printf("[tracing] enabled | service=%s exporter=otlp/grpc endpoint=%s", serviceName, endpoint)

	return tp.Shutdown, nil
}
