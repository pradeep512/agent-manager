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

// Package amp_test contains golden tests that assert LLMSpan emits exactly the
// attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "llm" kind.
package amp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/usage"
)

// setupInMemoryProvider replaces the global OTel provider with an in-memory
// one backed by a SpanRecorder. Returns the recorder and a cleanup function.
func setupInMemoryProvider(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		tp.Shutdown(context.Background()) //nolint:errcheck
	})
	return rec
}

// attrMap builds a string→any map from a ReadOnlySpan's attributes.
func attrMap(span sdktrace.ReadOnlySpan) map[string]any {
	attrs := span.Attributes()
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[string(a.Key)] = a.Value.AsInterface()
	}
	return m
}

// lastSpan returns the last ended span from the recorder.
func lastSpan(t *testing.T, rec *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	return spans[len(spans)-1]
}

// TestLLMSpan_GoldenContractKeys asserts that LLMSpan emits exactly the
// required keys declared in the published contract for the "llm" kind:
//
//	gen_ai.operation.name  (required, "chat")
//	gen_ai.system          (required)
//	gen_ai.request.model   (required)
//	gen_ai.usage.input_tokens  (required, ≥ 0)
//	gen_ai.usage.output_tokens (required, ≥ 0)
//
// And optional keys when data is provided:
//
//	gen_ai.response.model
//	gen_ai.request.temperature
//	gen_ai.input.messages
//	gen_ai.output.messages
//	gen_ai.usage.cache_read_input_tokens
func TestLLMSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:         "anthropic",
		RequestModel:   "claude-3-5-sonnet-20241022",
		Temperature:    0.7,
		SetTemperature: true,
		InputMessages: []map[string]any{
			{"role": "user", "content": "Hello, what is 2+2?"},
		},
	})
	result.ResponseModel = "claude-3-5-sonnet-20241022"
	result.OutputMessages = []map[string]any{
		{"role": "assistant", "content": "2+2 equals 4."},
	}
	result.Usage = usage.LLMUsage{
		InputTokens:          10,
		OutputTokens:         8,
		CacheReadInputTokens: 5,
	}
	span.End()
	_ = ctx

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required keys per contract.
	requiredKeys := []string{
		"gen_ai.operation.name",
		"gen_ai.system",
		"gen_ai.request.model",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens",
	}
	for _, k := range requiredKeys {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required contract key %q missing from LLM span", k)
		}
	}

	// Check required value constraints.
	if attrs["gen_ai.operation.name"] != "chat" {
		t.Errorf("gen_ai.operation.name = %q, want 'chat'", attrs["gen_ai.operation.name"])
	}
	if attrs["gen_ai.system"] != "anthropic" {
		t.Errorf("gen_ai.system = %q, want 'anthropic'", attrs["gen_ai.system"])
	}
	if attrs["gen_ai.request.model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("gen_ai.request.model = %q", attrs["gen_ai.request.model"])
	}
	if attrs["gen_ai.usage.input_tokens"] != int64(10) {
		t.Errorf("gen_ai.usage.input_tokens = %v, want 10", attrs["gen_ai.usage.input_tokens"])
	}
	if attrs["gen_ai.usage.output_tokens"] != int64(8) {
		t.Errorf("gen_ai.usage.output_tokens = %v, want 8", attrs["gen_ai.usage.output_tokens"])
	}

	// Optional keys present when data supplied.
	if attrs["gen_ai.response.model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("gen_ai.response.model = %q", attrs["gen_ai.response.model"])
	}
	if _, ok := attrs["gen_ai.request.temperature"]; !ok {
		t.Error("gen_ai.request.temperature should be present when SetTemperature is true")
	}
	if _, ok := attrs["gen_ai.input.messages"]; !ok {
		t.Error("gen_ai.input.messages should be present when InputMessages is set")
	}
	if _, ok := attrs["gen_ai.output.messages"]; !ok {
		t.Error("gen_ai.output.messages should be present when OutputMessages is set")
	}
	if attrs["gen_ai.usage.cache_read_input_tokens"] != int64(5) {
		t.Errorf("gen_ai.usage.cache_read_input_tokens = %v, want 5", attrs["gen_ai.usage.cache_read_input_tokens"])
	}

	// Span name must be "chat".
	if s.Name() != "chat" {
		t.Errorf("span name = %q, want 'chat'", s.Name())
	}
}

// TestLLMSpan_GoldenContractKeys_RequiredOnly asserts that the required keys are
// present and have valid values even when no optional data is provided.
func TestLLMSpan_GoldenContractKeys_RequiredOnly(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "openai",
		RequestModel: "gpt-4o",
	})
	result.Usage = usage.LLMUsage{InputTokens: 5, OutputTokens: 3}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	for _, k := range []string{
		"gen_ai.operation.name",
		"gen_ai.system",
		"gen_ai.request.model",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens",
	} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}

	// Optional keys must NOT be present when not set.
	for _, k := range []string{
		"gen_ai.response.model",
		"gen_ai.request.temperature",
		"gen_ai.input.messages",
		"gen_ai.output.messages",
		"gen_ai.usage.cache_read_input_tokens",
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("optional key %q should be absent when not provided, but was set to %v", k, attrs[k])
		}
	}
}

// TestLLMSpan_ContentRedaction asserts that AMP_TRACE_CONTENT=false redacts
// message text but preserves roles, structure, tokens, and model name.
func TestLLMSpan_ContentRedaction(t *testing.T) {
	// Force content off by temporarily overriding the package-level cache.
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	// Reset the config cache so the next LLMSpan call reads the new env.
	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
		InputMessages: []map[string]any{
			{"role": "user", "content": "This is a secret prompt"},
		},
	})
	result.OutputMessages = []map[string]any{
		{"role": "assistant", "content": "This is a secret response"},
	}
	result.Usage = usage.LLMUsage{InputTokens: 10, OutputTokens: 5}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Input messages should have content redacted, role preserved.
	inputJSON, ok := attrs["gen_ai.input.messages"].(string)
	if !ok {
		t.Fatal("gen_ai.input.messages not found or not a string")
	}
	var inputMsgs []map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMsgs); err != nil {
		t.Fatalf("cannot unmarshal input messages: %v", err)
	}
	if len(inputMsgs) == 0 {
		t.Fatal("input messages slice is empty")
	}
	if inputMsgs[0]["content"] != "[redacted]" {
		t.Errorf("input content should be '[redacted]', got %q", inputMsgs[0]["content"])
	}
	if inputMsgs[0]["role"] != "user" {
		t.Errorf("input role should be preserved 'user', got %q", inputMsgs[0]["role"])
	}

	// Output messages should have content redacted.
	outputJSON, ok := attrs["gen_ai.output.messages"].(string)
	if !ok {
		t.Fatal("gen_ai.output.messages not found or not a string")
	}
	var outputMsgs []map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &outputMsgs); err != nil {
		t.Fatalf("cannot unmarshal output messages: %v", err)
	}
	if outputMsgs[0]["content"] != "[redacted]" {
		t.Errorf("output content should be '[redacted]', got %q", outputMsgs[0]["content"])
	}
	if outputMsgs[0]["role"] != "assistant" {
		t.Errorf("output role should be preserved, got %q", outputMsgs[0]["role"])
	}

	// Tokens and model must still be present.
	if attrs["gen_ai.usage.input_tokens"] != int64(10) {
		t.Errorf("tokens missing or wrong after redaction: input_tokens = %v", attrs["gen_ai.usage.input_tokens"])
	}
	if attrs["gen_ai.request.model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("model should be preserved after redaction: %q", attrs["gen_ai.request.model"])
	}
}

// TestLLMSpan_Error asserts that span.Error marks the span with error status
// and sets the error.type attribute.
func TestLLMSpan_Error(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
	})
	result.Usage = usage.LLMUsage{InputTokens: 0, OutputTokens: 0}
	span.Error(errors.New("model overloaded"))
	span.End()

	s := lastSpan(t, rec)

	if s.Status().Code != codes.Error {
		t.Errorf("expected error status code, got %v", s.Status().Code)
	}

	attrs := attrMap(s)
	if _, ok := attrs["error.type"]; !ok {
		t.Error("error.type attribute missing after span.Error()")
	}
}

// TestLLMSpan_SpanKindIsClient asserts that the LLM span uses SpanKindClient,
// matching the Python reference (SpanKind.CLIENT).
func TestLLMSpan_SpanKindIsClient(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
	})
	result.Usage = usage.LLMUsage{}
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "client" {
		t.Errorf("span kind = %q, want 'client'", s.SpanKind().String())
	}
}

// TestLLMSpan_FullAnthropicUsageShape verifies that a real-world Anthropic
// usage response (including cache_creation, ephemeral split, service_tier, and
// inference_geo) is correctly emitted on the leaf LLM span, and that
// gen_ai.usage.input_tokens remains the raw uncached value.
//
// This is the top-level acceptance-criteria test for issue #10.
func TestLLMSpan_FullAnthropicUsageShape(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
	})
	// Populate the full Anthropic usage shape from issue #10.
	result.Usage = usage.LLMUsage{
		InputTokens:                         1,
		OutputTokens:                        38,
		CacheReadInputTokens:                6270,
		CacheCreationInputTokens:            222,
		CacheCreationEphemeral1hInputTokens: 0,   // zero → should be omitted
		CacheCreationEphemeral5mInputTokens: 222,
		ServiceTier:                         "standard",
		InferenceGeo:                        "global",
	}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// --- Raw-uncached input rule ---
	if attrs["gen_ai.usage.input_tokens"] != int64(1) {
		t.Errorf("input_tokens = %v, want 1 (raw uncached)", attrs["gen_ai.usage.input_tokens"])
	}
	// input_tokens must NOT equal cache_read (would indicate folding).
	if attrs["gen_ai.usage.input_tokens"] == attrs["gen_ai.usage.cache_read_input_tokens"] {
		t.Error("input_tokens must be raw uncached, not equal to cache_read_input_tokens")
	}

	// --- Standard keys ---
	if attrs["gen_ai.usage.output_tokens"] != int64(38) {
		t.Errorf("output_tokens = %v, want 38", attrs["gen_ai.usage.output_tokens"])
	}
	if attrs["gen_ai.usage.cache_read_input_tokens"] != int64(6270) {
		t.Errorf("cache_read_input_tokens = %v, want 6270", attrs["gen_ai.usage.cache_read_input_tokens"])
	}
	if attrs["gen_ai.usage.cache_creation_input_tokens"] != int64(222) {
		t.Errorf("cache_creation_input_tokens = %v, want 222", attrs["gen_ai.usage.cache_creation_input_tokens"])
	}

	// --- Ephemeral split keys (issue #10) ---
	// 5m tier present (non-zero).
	if attrs["gen_ai.usage.cache_creation.ephemeral_5m_input_tokens"] != int64(222) {
		t.Errorf("ephemeral_5m_input_tokens = %v, want 222", attrs["gen_ai.usage.cache_creation.ephemeral_5m_input_tokens"])
	}
	// 1h tier absent (zero).
	if _, ok := attrs["gen_ai.usage.cache_creation.ephemeral_1h_input_tokens"]; ok {
		t.Error("ephemeral_1h_input_tokens should be absent when zero")
	}

	// --- Metadata keys (issue #10) ---
	if attrs["gen_ai.anthropic.service_tier"] != "standard" {
		t.Errorf("service_tier = %v, want 'standard'", attrs["gen_ai.anthropic.service_tier"])
	}
	if attrs["gen_ai.anthropic.inference_geo"] != "global" {
		t.Errorf("inference_geo = %v, want 'global'", attrs["gen_ai.anthropic.inference_geo"])
	}
}

// TestLLMSpan_ExtendedKeysAbsentWhenNotSet verifies that the new issue-#10 keys
// (ephemeral split, service_tier, inference_geo) are NOT emitted when not set,
// so callers that don't use Anthropic are unaffected.
func TestLLMSpan_ExtendedKeysAbsentWhenNotSet(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "openai",
		RequestModel: "gpt-4o",
	})
	// Minimal usage — no extended fields.
	result.Usage = usage.LLMUsage{InputTokens: 10, OutputTokens: 5}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	extendedKeys := []string{
		"gen_ai.usage.cache_creation.ephemeral_1h_input_tokens",
		"gen_ai.usage.cache_creation.ephemeral_5m_input_tokens",
		"gen_ai.anthropic.service_tier",
		"gen_ai.anthropic.inference_geo",
	}
	for _, k := range extendedKeys {
		if _, ok := attrs[k]; ok {
			t.Errorf("extended key %q should be absent when not set, but was present: %v", k, attrs[k])
		}
	}
}

// TestLLMSpan_AttributeKeyType_Integer asserts that token usage attributes are
// emitted as integers (not strings or floats), matching the contract spec.
func TestLLMSpan_AttributeKeyType_Integer(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
	})
	result.Usage = usage.LLMUsage{InputTokens: 42, OutputTokens: 17}
	span.End()

	s := lastSpan(t, rec)

	// Find the raw attribute.KeyValue to check the type.
	rawAttrs := s.Attributes()
	tokenKeys := map[attribute.Key]bool{
		"gen_ai.usage.input_tokens":  false,
		"gen_ai.usage.output_tokens": false,
	}
	for _, a := range rawAttrs {
		if _, ok := tokenKeys[a.Key]; ok {
			if a.Value.Type() != attribute.INT64 {
				t.Errorf("attribute %q type = %v, want INT64", a.Key, a.Value.Type())
			}
			tokenKeys[a.Key] = true
		}
	}
	for k, found := range tokenKeys {
		if !found {
			t.Errorf("token key %q not found in span attributes", k)
		}
	}
}
