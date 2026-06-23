// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// Package exporter wires up the OTel SDK tracer provider to send spans to the
// AMP gateway over OTLP/HTTP.
//
// Usage:
//
//	cfg, err := config.Load()
//	exp, err := exporter.New(ctx, cfg)
//	// ... instrument ...
//	exp.Shutdown(ctx)
//
// The API key is read through a CredentialFunc so that auto-refresh can be
// added later without an API break. The default CredentialFunc closes over the
// value read once at Init time.
package exporter

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/config"
)

// componentUIDKey is the OTel resource attribute that ties a trace to an AMP
// registered component. Defined in traces-observer-service/observer/convert.go.
const componentUIDKey = "openchoreo.dev/component-uid"

// CredentialFunc returns the current x-amp-api-key value. The default
// implementation returns the key read once at Init time; replace it with a
// file-watcher or vault lookup for key rotation without changing the API.
type CredentialFunc func() string

// Provider holds the configured tracer provider and its shutdown function.
type Provider struct {
	tp       *sdktrace.TracerProvider
	credFunc CredentialFunc
}

// global singleton state (mirrors Python's _initialized / _init_lock).
var (
	globalMu       sync.Mutex
	globalProvider *Provider
)

// New creates a new OTel tracer provider configured for AMP export. It:
//   - exports over OTLP/HTTP to cfg.TracesEndpoint with header x-amp-api-key
//   - attaches the openchoreo.dev/component-uid resource attribute (when set)
//   - installs the global W3C TraceContext + Baggage propagator
//
// The returned Provider's Shutdown must be called on exit to flush batched spans.
func New(ctx context.Context, cfg *config.Config) (*Provider, error) {
	credFunc := staticCred(cfg.APIKey)
	return newWithCred(ctx, cfg, credFunc)
}

// newWithCred is the internal constructor used by New and tests.
func newWithCred(ctx context.Context, cfg *config.Config, credFunc CredentialFunc) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("amp/exporter: config must not be nil")
	}

	if cfg.Debug {
		slog.Debug("amp: creating OTLP/HTTP exporter", "endpoint", cfg.TracesEndpoint)
	}

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(cfg.TracesEndpoint),
		otlptracehttp.WithHeaders(map[string]string{
			"x-amp-api-key": credFunc(),
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("amp/exporter: failed to create OTLP/HTTP exporter: %w", err)
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("amp/exporter: failed to build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)

	// Install W3C TraceContext + Baggage propagators globally.
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	otel.SetTracerProvider(tp)

	if cfg.Debug {
		slog.Debug("amp: tracer provider installed", "traces_endpoint", cfg.TracesEndpoint)
	}

	return &Provider{tp: tp, credFunc: credFunc}, nil
}

// Shutdown flushes all batched spans and releases resources. It must be called
// before the process exits to avoid losing the last spans.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.tp == nil {
		return nil
	}
	return p.tp.Shutdown(ctx)
}

// TracerProvider returns the underlying SDK tracer provider.
func (p *Provider) TracerProvider() *sdktrace.TracerProvider {
	return p.tp
}

// InitGlobal configures the global OTel tracer provider for AMP, idempotent.
// A second call with any configuration is a no-op if already initialized.
// Returns (provider, nil) on the first call, or (existing, nil) on subsequent calls.
func InitGlobal(ctx context.Context, cfg *config.Config) (*Provider, error) {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalProvider != nil {
		if cfg != nil && cfg.Debug {
			slog.Debug("amp: already initialized, skipping")
		}
		return globalProvider, nil
	}

	p, err := New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	globalProvider = p
	return p, nil
}

// ShutdownGlobal flushes the global provider and resets it so Init can be
// called again. Intended for use in main() and tests.
func ShutdownGlobal(ctx context.Context) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalProvider == nil {
		return nil
	}
	err := globalProvider.Shutdown(ctx)
	globalProvider = nil
	return err
}

// buildResource creates the OTel resource with service name + component-uid.
func buildResource(ctx context.Context, cfg *config.Config) (*resource.Resource, error) {
	opts := []resource.Option{
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(semconv.ServiceNameKey.String("amp-agent")),
	}
	if cfg.ComponentUID != "" {
		opts = append(opts, resource.WithAttributes(
			attribute.Key(componentUIDKey).String(cfg.ComponentUID),
		))
	}
	return resource.New(ctx, opts...)
}

// staticCred returns a CredentialFunc that always returns the given key.
func staticCred(apiKey string) CredentialFunc {
	return func() string { return apiKey }
}
