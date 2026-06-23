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

package config_test

import (
	"strings"
	"testing"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/config"
)

// cleanEnv clears all AMP env vars using t.Setenv (which auto-restores on cleanup).
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		config.EnvEndpoint,
		config.EnvAPIKey,
		config.EnvTraceContent,
		config.EnvComponentUID,
		config.EnvDebug,
	} {
		t.Setenv(k, "")
	}
}

func TestLoad_MissingEndpoint(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvAPIKey, "test-key")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when AMP_OTEL_ENDPOINT is missing")
	}
	if !strings.Contains(err.Error(), config.EnvEndpoint) {
		t.Errorf("error should mention %q, got: %v", config.EnvEndpoint, err)
	}
}

func TestLoad_MissingAPIKey(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when AMP_AGENT_API_KEY is missing")
	}
	if !strings.Contains(err.Error(), config.EnvAPIKey) {
		t.Errorf("error should mention %q, got: %v", config.EnvAPIKey, err)
	}
}

func TestLoad_BothMissing_ErrorMentionsBoth(t *testing.T) {
	cleanEnv(t)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when both required vars are missing")
	}
	if !strings.Contains(err.Error(), config.EnvEndpoint) {
		t.Errorf("error should mention %q, got: %v", config.EnvEndpoint, err)
	}
	if !strings.Contains(err.Error(), config.EnvAPIKey) {
		t.Errorf("error should mention %q, got: %v", config.EnvAPIKey, err)
	}
}

func TestLoad_TracesEndpointAppended(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "test-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TracesEndpoint != "https://otel.example.com/v1/traces" {
		t.Errorf("unexpected TracesEndpoint: %q", cfg.TracesEndpoint)
	}
}

func TestLoad_TracesEndpointNotDuplicated(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com/v1/traces")
	t.Setenv(config.EnvAPIKey, "test-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TracesEndpoint != "https://otel.example.com/v1/traces" {
		t.Errorf("TracesEndpoint should not duplicate suffix, got: %q", cfg.TracesEndpoint)
	}
}

func TestLoad_TracesEndpointTrailingSlashStripped(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com/")
	t.Setenv(config.EnvAPIKey, "test-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TracesEndpoint != "https://otel.example.com/v1/traces" {
		t.Errorf("unexpected TracesEndpoint: %q", cfg.TracesEndpoint)
	}
}

func TestLoad_TraceContentDefaultTrue(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "test-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.TraceContent {
		t.Error("TraceContent should default to true")
	}
}

func TestLoad_TraceContentFalse(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "test-key")
	t.Setenv(config.EnvTraceContent, "false")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TraceContent {
		t.Error("TraceContent should be false when AMP_TRACE_CONTENT=false")
	}
}

func TestLoad_TraceContentFalseCaseInsensitive(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "test-key")
	t.Setenv(config.EnvTraceContent, "FALSE")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TraceContent {
		t.Error("TraceContent should be false for 'FALSE'")
	}
}

func TestLoad_DebugFlag(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "test-key")
	t.Setenv(config.EnvDebug, "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Debug {
		t.Error("Debug should be true when AMP_DEBUG=true")
	}
}

func TestLoad_ComponentUID(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "test-key")
	t.Setenv(config.EnvComponentUID, "my-component-uid")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ComponentUID != "my-component-uid" {
		t.Errorf("unexpected ComponentUID: %q", cfg.ComponentUID)
	}
}

func TestLoad_APIKeyAndEndpointPreserved(t *testing.T) {
	cleanEnv(t)
	t.Setenv(config.EnvEndpoint, "https://otel.example.com")
	t.Setenv(config.EnvAPIKey, "my-secret-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OtelEndpoint != "https://otel.example.com" {
		t.Errorf("unexpected OtelEndpoint: %q", cfg.OtelEndpoint)
	}
	if cfg.APIKey != "my-secret-key" {
		t.Errorf("unexpected APIKey: %q", cfg.APIKey)
	}
}
