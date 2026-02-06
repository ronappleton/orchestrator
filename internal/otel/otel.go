package otel

import (
	"context"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

func Init(serviceName string) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "otel-collector:4317"
	}
	ctx := context.Background()

	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	attrs := []attribute.KeyValue{attribute.String("service.name", serviceName)}
	if env := os.Getenv("METRIC_SERVICE_ENV"); env != "" {
		attrs = append(attrs, attribute.String("deployment.environment", env))
	}
	if tags := os.Getenv("METRIC_SERVICE_TAGS"); tags != "" {
		attrs = append(attrs, attribute.String("service.tags", tags))
	}
	if version := firstNonEmpty(os.Getenv("APP_VERSION"), os.Getenv("GIT_SHA")); version != "" {
		attrs = append(attrs, attribute.String("service.version", version))
	}
	if instance := os.Getenv("HOSTNAME"); instance != "" {
		attrs = append(attrs, attribute.String("service.instance.id", instance))
	}

	res, _ := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithHost(),
		resource.WithAttributes(attrs...),
	)

	tp := trace.NewTracerProvider(trace.WithBatcher(traceExp), trace.WithResource(res))
	mp := metric.NewMeterProvider(metric.WithReader(metric.NewPeriodicReader(metricExp, metric.WithInterval(15*time.Second))), metric.WithResource(res))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		http.DefaultTransport = otelhttp.NewTransport(base)
	}

	return func(ctx context.Context) error {
		_ = mp.Shutdown(ctx)
		return tp.Shutdown(ctx)
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
