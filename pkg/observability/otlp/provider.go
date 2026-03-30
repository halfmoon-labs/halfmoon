package otlp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"runtime"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc/credentials"
)

type providers struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metric.MeterProvider
}

func newProviders(ctx context.Context, cfg Config) (*providers, error) {
	res, err := buildResource(cfg)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	exportInterval := time.Duration(cfg.ExportIntervalMs) * time.Millisecond

	tp, err := newTracerProvider(ctx, cfg, res, timeout)
	if err != nil {
		return nil, fmt.Errorf("create tracer provider: %w", err)
	}

	mp, err := newMeterProvider(ctx, cfg, res, timeout, exportInterval)
	if err != nil {
		tp.Shutdown(ctx) //nolint:errcheck
		return nil, fmt.Errorf("create meter provider: %w", err)
	}

	return &providers{
		tracerProvider: tp,
		meterProvider:  mp,
	}, nil
}

func (p *providers) shutdown(ctx context.Context) error {
	return errors.Join(
		p.tracerProvider.Shutdown(ctx),
		p.meterProvider.Shutdown(ctx),
	)
}

func buildResource(cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		attribute.String("host.arch", runtime.GOARCH),
		attribute.String("os.type", runtime.GOOS),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...), nil
}

func newTracerProvider(
	ctx context.Context,
	cfg Config,
	res *resource.Resource,
	timeout time.Duration,
) (*sdktrace.TracerProvider, error) {
	var exporter sdktrace.SpanExporter
	var err error

	switch cfg.Protocol {
	case "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTimeout(timeout),
			otlptracegrpc.WithHeaders(cfg.Headers),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		} else {
			opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)
	case "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithTimeout(timeout),
			otlptracehttp.WithHeaders(cfg.Headers),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exporter, err = otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unsupported otlp protocol: %q", cfg.Protocol)
	}
	if err != nil {
		return nil, err
	}

	bsp := sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithMaxExportBatchSize(cfg.BatchSize),
	)
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	), nil
}

func newMeterProvider(
	ctx context.Context,
	cfg Config,
	res *resource.Resource,
	timeout time.Duration,
	exportInterval time.Duration,
) (*metric.MeterProvider, error) {
	var exporter metric.Exporter
	var err error

	switch cfg.Protocol {
	case "grpc":
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
			otlpmetricgrpc.WithTimeout(timeout),
			otlpmetricgrpc.WithHeaders(cfg.Headers),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		} else {
			opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
		}
		exporter, err = otlpmetricgrpc.New(ctx, opts...)
	case "http":
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
			otlpmetrichttp.WithTimeout(timeout),
			otlpmetrichttp.WithHeaders(cfg.Headers),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		exporter, err = otlpmetrichttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unsupported otlp protocol: %q", cfg.Protocol)
	}
	if err != nil {
		return nil, err
	}

	reader := metric.NewPeriodicReader(exporter,
		metric.WithInterval(exportInterval),
	)
	return metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(reader),
	), nil
}
