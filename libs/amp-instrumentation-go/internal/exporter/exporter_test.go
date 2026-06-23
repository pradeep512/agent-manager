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

package exporter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/config"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/exporter"
)

// testServer starts a minimal HTTP server that captures the last request path
// and x-amp-api-key header, and returns 200 OK.
func testServer(t *testing.T) (baseURL string, gotPath *string, gotAPIKey *string) {
	t.Helper()
	path := ""
	key := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		key = r.Header.Get("x-amp-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &path, &key
}

func minimalConfig(endpoint string) *config.Config {
	return &config.Config{
		OtelEndpoint:   endpoint,
		APIKey:         "test-api-key",
		TracesEndpoint: endpoint + "/v1/traces",
		TraceContent:   true,
		ComponentUID:   "test-component-uid",
	}
}

func TestNew_SetsTracesEndpointWithSuffix(t *testing.T) {
	baseURL, gotPath, _ := testServer(t)
	cfg := minimalConfig(baseURL)

	ctx := context.Background()
	p, err := exporter.New(ctx, cfg)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}
	defer p.Shutdown(ctx) //nolint:errcheck

	// Emit a span so the exporter has something to send.
	tracer := p.TracerProvider().Tracer("test")
	_, span := tracer.Start(ctx, "test-span")
	span.End()

	// Force-flush by shutting down synchronously.
	if err := p.Shutdown(ctx); err != nil {
		t.Logf("shutdown: %v", err)
	}

	// The exporter should have POSTed to /v1/traces.
	if !strings.HasSuffix(*gotPath, "/v1/traces") {
		t.Errorf("expected /v1/traces suffix, got path %q", *gotPath)
	}
}

func TestNew_SendsAPIKeyHeader(t *testing.T) {
	baseURL, _, gotAPIKey := testServer(t)
	cfg := minimalConfig(baseURL)
	cfg.APIKey = "my-secret-key"
	cfg.TracesEndpoint = baseURL + "/v1/traces"

	ctx := context.Background()
	p, err := exporter.New(ctx, cfg)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}
	defer p.Shutdown(ctx) //nolint:errcheck

	tracer := p.TracerProvider().Tracer("test")
	_, span := tracer.Start(ctx, "test-span")
	span.End()

	if err := p.Shutdown(ctx); err != nil {
		t.Logf("shutdown: %v", err)
	}

	if *gotAPIKey != "my-secret-key" {
		t.Errorf("expected x-amp-api-key = 'my-secret-key', got %q", *gotAPIKey)
	}
}

func TestInitGlobal_Idempotent(t *testing.T) {
	// Reset global state before and after.
	ctx := context.Background()
	_ = exporter.ShutdownGlobal(ctx)
	t.Cleanup(func() { _ = exporter.ShutdownGlobal(ctx) })

	baseURL, _, _ := testServer(t)
	cfg := minimalConfig(baseURL)

	p1, err := exporter.InitGlobal(ctx, cfg)
	if err != nil {
		t.Fatalf("first InitGlobal: %v", err)
	}

	// Second call must return the same provider (idempotency).
	p2, err := exporter.InitGlobal(ctx, cfg)
	if err != nil {
		t.Fatalf("second InitGlobal: %v", err)
	}
	if p1 != p2 {
		t.Error("expected same provider from second InitGlobal call")
	}
}

func TestInitGlobal_InstallsW3CPropagator(t *testing.T) {
	ctx := context.Background()
	_ = exporter.ShutdownGlobal(ctx)
	t.Cleanup(func() { _ = exporter.ShutdownGlobal(ctx) })

	baseURL, _, _ := testServer(t)
	cfg := minimalConfig(baseURL)

	if _, err := exporter.InitGlobal(ctx, cfg); err != nil {
		t.Fatalf("InitGlobal: %v", err)
	}

	// The global propagator must support W3C traceparent header extraction.
	prop := otel.GetTextMapPropagator()
	_, ok := prop.(propagation.TextMapPropagator)
	if !ok {
		t.Error("expected a TextMapPropagator to be installed globally")
	}

	// Verify TraceContext field is in the composite by extracting a traceparent.
	carrier := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	extractedCtx := prop.Extract(context.Background(), carrier)
	if extractedCtx == context.Background() {
		t.Error("expected traceparent to be extracted by W3C propagator")
	}
}

func TestShutdown_FlushesSpans(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalConfig(srv.URL)
	ctx := context.Background()

	p, err := exporter.New(ctx, cfg)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}

	tracer := p.TracerProvider().Tracer("test")
	_, span := tracer.Start(ctx, "flush-test")
	span.End()

	// Before shutdown, the batch may not have flushed yet.
	if err := p.Shutdown(ctx); err != nil {
		t.Logf("shutdown error: %v", err)
	}

	// After Shutdown, at least one request should have been sent.
	if received.Load() == 0 {
		t.Error("expected at least one export request after Shutdown")
	}
}
