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

// Package amp is the Go instrumentation SDK for WSO2 Agent Manager.
//
// It provides helpers to emit fully-conformant AMP spans for Go agents.
//
// # Quick start
//
//	func main() {
//	    ctx := context.Background()
//	    if err := amp.Init(ctx); err != nil {
//	        log.Fatal(err)
//	    }
//	    defer amp.Shutdown(ctx)
//
//	    ctx, span, result := amp.LLMSpan(ctx, amp.LLMInput{
//	        System:        "anthropic",
//	        RequestModel:  "claude-3-5-sonnet-20241022",
//	        InputMessages: []map[string]any{{"role": "user", "content": "Hello"}},
//	    })
//	    defer span.End()
//
//	    // ... call the LLM ...
//	    result.OutputMessages = []map[string]any{{"role": "assistant", "content": "Hi!"}}
//	    result.InputTokens = 10
//	    result.OutputTokens = 5
//	}
//
// # Environment variables
//
//   - AMP_OTEL_ENDPOINT   – required; AMP gateway base URL
//   - AMP_AGENT_API_KEY   – required; API key for the x-amp-api-key header
//   - AMP_TRACE_CONTENT   – optional; "false" redacts prompt/response text (default "true")
//   - AMP_COMPONENT_UID  – optional; openchoreo.dev/component-uid resource attribute
//   - AMP_DEBUG           – optional; "true" enables verbose SDK logging
package amp

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/config"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/exporter"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/redact"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/usage"
)

const tracerName = "amp-instrumentation-go"

// Init configures the global OTel tracer provider to export spans to AMP.
//
// It reads AMP_OTEL_ENDPOINT and AMP_AGENT_API_KEY from the environment;
// returns a descriptive error if either is missing. Idempotent: a second call
// is a no-op and returns nil.
func Init(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, err = exporter.InitGlobal(ctx, cfg)
	return err
}

// Shutdown flushes all batched spans and tears down the tracer provider.
// Must be called before the process exits (typically via defer in main).
func Shutdown(ctx context.Context) error {
	return exporter.ShutdownGlobal(ctx)
}

// --------------------------------------------------------------------------
// LLM span
// --------------------------------------------------------------------------

// LLMInput holds the parameters for one LLM chat call. Required fields are
// System, RequestModel; all others are optional.
type LLMInput struct {
	// System is the AI provider / vendor (e.g. "anthropic", "openai").
	System string
	// RequestModel is the model identifier requested by the caller.
	RequestModel string
	// Temperature is the sampling temperature. Only set when SetTemperature is true.
	Temperature float64
	// SetTemperature must be true for Temperature to be emitted.
	SetTemperature bool
	// InputMessages is the list of chat messages sent to the model.
	InputMessages []map[string]any
}

// LLMResult is filled by the caller after the LLM returns.
type LLMResult struct {
	// ResponseModel is the model that actually answered (may differ from request).
	ResponseModel string
	// OutputMessages are the model's response messages.
	OutputMessages []map[string]any
	// Usage holds the token counts for this call.
	Usage usage.LLMUsage
}

// Span is a handle to an in-flight AMP span. The caller fills a result object
// and then calls End() to write the response attributes and close the span.
type Span struct {
	span          trace.Span
	result        *LLMResult
	traceContent  bool
	endAttributes func()
}

// End writes the response attributes recorded on the result handle and closes
// the underlying OTel span. It must be called exactly once, typically as:
//
//	ctx, s, result := amp.LLMSpan(ctx, input)
//	defer s.End()
func (s *Span) End() {
	if s.endAttributes != nil {
		s.endAttributes()
	}
	s.span.End()
}

// Error marks the span as failed with error status and sets error.type.
// It mirrors instrumentation.py's mark_error helper.
func (s *Span) Error(err error) {
	if err == nil {
		return
	}
	s.span.SetStatus(codes.Error, err.Error())
	s.span.SetAttributes(attribute.String("error.type", err.Error()))
}

// LLMSpan starts a chat span, returning (ctx, span, result). The caller fills
// result after the LLM returns, then calls span.End().
//
// Attributes written at Start:
//   - gen_ai.operation.name = "chat"
//   - gen_ai.system
//   - gen_ai.request.model
//   - gen_ai.request.temperature (if set)
//   - gen_ai.input.messages
//
// Attributes written at End (from result):
//   - gen_ai.response.model
//   - gen_ai.output.messages
//   - gen_ai.usage.input_tokens
//   - gen_ai.usage.output_tokens
//   - gen_ai.usage.cache_read_input_tokens (if non-zero)
//   - gen_ai.usage.cache_creation_input_tokens (if non-zero)
func LLMSpan(ctx context.Context, input LLMInput) (context.Context, *Span, *LLMResult) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "chat", trace.WithSpanKind(trace.SpanKindClient))

	// Write start-time attributes.
	otelSpan.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.system", input.System),
		attribute.String("gen_ai.request.model", input.RequestModel),
	)
	if input.SetTemperature {
		otelSpan.SetAttributes(attribute.Float64("gen_ai.request.temperature", input.Temperature))
	}
	if len(input.InputMessages) > 0 {
		otelSpan.SetAttributes(attribute.String("gen_ai.input.messages",
			redact.Messages(input.InputMessages, cfg.TraceContent)))
	}

	result := &LLMResult{}

	s := &Span{
		span:         otelSpan,
		result:       result,
		traceContent: cfg.TraceContent,
	}
	s.endAttributes = func() {
		if result.ResponseModel != "" {
			otelSpan.SetAttributes(attribute.String("gen_ai.response.model", result.ResponseModel))
		}
		if len(result.OutputMessages) > 0 {
			otelSpan.SetAttributes(attribute.String("gen_ai.output.messages",
				redact.Messages(result.OutputMessages, s.traceContent)))
		}
		// Always set usage — required by the observer.
		otelSpan.SetAttributes(
			attribute.Int64("gen_ai.usage.input_tokens", result.Usage.InputTokens),
			attribute.Int64("gen_ai.usage.output_tokens", result.Usage.OutputTokens),
		)
		if result.Usage.CacheReadInputTokens > 0 {
			otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.cache_read_input_tokens", result.Usage.CacheReadInputTokens))
		}
		if result.Usage.CacheCreationInputTokens > 0 {
			otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.cache_creation_input_tokens", result.Usage.CacheCreationInputTokens))
		}
	}

	return ctx, s, result
}

// --------------------------------------------------------------------------
// config cache (avoid re-reading env on every span)
// --------------------------------------------------------------------------

var cachedConfig *config.Config

// loadConfigCached returns the last successfully loaded config, or a default
// config if Init was never called. This is a best-effort fallback; callers
// should always call Init first.
func loadConfigCached() *config.Config {
	if cachedConfig != nil {
		return cachedConfig
	}
	cfg, err := config.Load()
	if err != nil {
		// Return a safe default that leaves content on.
		return &config.Config{TraceContent: true}
	}
	cachedConfig = cfg
	return cfg
}

// ResetConfigCache clears the package-level config cache so that the next span
// call re-reads the environment. Intended for use in tests only.
func ResetConfigCache() {
	cachedConfig = nil
}
