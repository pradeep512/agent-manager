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

// Package config reads and validates the AMP SDK environment configuration.
//
// Required env vars:
//   - AMP_OTEL_ENDPOINT   – base URL of the AMP OTLP gateway
//   - AMP_AGENT_API_KEY   – API key for x-amp-api-key header
//
// Optional env vars:
//   - AMP_TRACE_CONTENT   – "false" disables prompt/response content (default true)
//   - AMP_COMPONENT_UID   – sets openchoreo.dev/component-uid resource attribute
//   - AMP_DEBUG           – "true" enables debug logging
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Env variable names.
const (
	EnvEndpoint     = "AMP_OTEL_ENDPOINT"
	EnvAPIKey       = "AMP_AGENT_API_KEY"
	EnvTraceContent = "AMP_TRACE_CONTENT"
	EnvComponentUID = "AMP_COMPONENT_UID"
	EnvDebug        = "AMP_DEBUG"
)

// TracesSuffix is appended to AMP_OTEL_ENDPOINT to form the OTLP/HTTP traces URL.
const TracesSuffix = "/v1/traces"

// Config holds the resolved AMP SDK configuration.
type Config struct {
	// OtelEndpoint is the base AMP gateway URL (e.g. "https://gateway.example.com").
	OtelEndpoint string
	// APIKey is the AMP API key used for the x-amp-api-key header.
	APIKey string
	// TracesEndpoint is OtelEndpoint + "/v1/traces".
	TracesEndpoint string
	// TraceContent controls whether prompt/completion text is included in spans.
	// When false, text is replaced with "[redacted]".
	TraceContent bool
	// ComponentUID is the openchoreo.dev/component-uid resource attribute value.
	// May be empty for internal (platform-injected) agents where the value is
	// provided separately.
	ComponentUID string
	// Debug enables verbose SDK logging.
	Debug bool
}

// Load reads and validates the AMP configuration from the environment.
// Returns an error that names all missing required variables (not just the first).
func Load() (*Config, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvEndpoint)), "/")
	apiKey := strings.TrimSpace(os.Getenv(EnvAPIKey))

	var errs []string
	if endpoint == "" {
		errs = append(errs, fmt.Sprintf("environment variable %q is required but not set", EnvEndpoint))
	}
	if apiKey == "" {
		errs = append(errs, fmt.Sprintf("environment variable %q is required but not set", EnvAPIKey))
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}

	// Build the OTLP/HTTP traces endpoint: avoid duplicating the suffix.
	tracesEndpoint := endpoint
	if !strings.HasSuffix(tracesEndpoint, TracesSuffix) {
		tracesEndpoint += TracesSuffix
	}

	traceContent := true
	if strings.ToLower(strings.TrimSpace(os.Getenv(EnvTraceContent))) == "false" {
		traceContent = false
	}

	debug := strings.ToLower(strings.TrimSpace(os.Getenv(EnvDebug))) == "true"

	return &Config{
		OtelEndpoint:   endpoint,
		APIKey:         apiKey,
		TracesEndpoint: tracesEndpoint,
		TraceContent:   traceContent,
		ComponentUID:   strings.TrimSpace(os.Getenv(EnvComponentUID)),
		Debug:          debug,
	}, nil
}
