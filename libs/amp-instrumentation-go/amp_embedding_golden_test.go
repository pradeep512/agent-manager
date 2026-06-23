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

// Package amp_test contains golden tests that assert EmbeddingSpan emits
// exactly the attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "embedding"
// kind.
package amp_test

import (
	"context"
	"fmt"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestEmbeddingSpan_GoldenContractKeys asserts that EmbeddingSpan emits the
// required keys declared in the published contract for the "embedding" kind:
//
//	gen_ai.operation.name  (required, "embeddings")
//	gen_ai.system          (required)
//	gen_ai.request.model   (required — satisfies EmbeddingModelAnyOf)
//	gen_ai.usage.input_tokens (required, ≥ 0)
//
// And optional keys when data is provided:
//
//	gen_ai.response.model
//	gen_ai.prompt.0.content, gen_ai.prompt.1.content  (indexed embedded text)
func TestEmbeddingSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span, result := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "voyage",
		RequestModel: "voyage-3",
		Texts:        []string{"hello world", "foo bar"},
	})
	result.ResponseModel = "voyage-3"
	result.InputTokens = 4
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
	}
	for _, k := range requiredKeys {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required contract key %q missing from embedding span", k)
		}
	}

	// gen_ai.operation.name must be "embeddings" — this is what hasEmbeddingAttributes
	// in process.go keys off to resolve the span to the "embedding" kind.
	if attrs["gen_ai.operation.name"] != "embeddings" {
		t.Errorf("gen_ai.operation.name = %q, want 'embeddings'", attrs["gen_ai.operation.name"])
	}
	if attrs["gen_ai.system"] != "voyage" {
		t.Errorf("gen_ai.system = %q, want 'voyage'", attrs["gen_ai.system"])
	}
	if attrs["gen_ai.request.model"] != "voyage-3" {
		t.Errorf("gen_ai.request.model = %q, want 'voyage-3'", attrs["gen_ai.request.model"])
	}
	if attrs["gen_ai.usage.input_tokens"] != int64(4) {
		t.Errorf("gen_ai.usage.input_tokens = %v, want 4", attrs["gen_ai.usage.input_tokens"])
	}

	// Optional: response model set from result.
	if attrs["gen_ai.response.model"] != "voyage-3" {
		t.Errorf("gen_ai.response.model = %q, want 'voyage-3'", attrs["gen_ai.response.model"])
	}

	// Indexed embedded text: gen_ai.prompt.0.content and gen_ai.prompt.1.content.
	for i, want := range []string{"hello world", "foo bar"} {
		key := fmt.Sprintf("gen_ai.prompt.%d.content", i)
		if attrs[key] != want {
			t.Errorf("%s = %q, want %q", key, attrs[key], want)
		}
	}

	// Span name must be "embeddings".
	if s.Name() != "embeddings" {
		t.Errorf("span name = %q, want 'embeddings'", s.Name())
	}
}

// TestEmbeddingSpan_GoldenContractKeys_RequiredOnly asserts required keys are
// present and optional ones are absent when no optional data is supplied.
func TestEmbeddingSpan_GoldenContractKeys_RequiredOnly(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "openai",
		RequestModel: "text-embedding-3-small",
	})
	result.InputTokens = 0
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required keys must be present.
	for _, k := range []string{
		"gen_ai.operation.name",
		"gen_ai.system",
		"gen_ai.request.model",
		"gen_ai.usage.input_tokens",
	} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}

	// Optional keys must NOT be present when not set.
	for _, k := range []string{
		"gen_ai.response.model",
		"gen_ai.prompt.0.content",
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("optional key %q should be absent when not provided, but was set to %v", k, attrs[k])
		}
	}
}

// TestEmbeddingSpan_ContentRedaction asserts that AMP_TRACE_CONTENT=false
// redacts embedded text (gen_ai.prompt.*.content) but keeps tokens and model.
func TestEmbeddingSpan_ContentRedaction(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "voyage",
		RequestModel: "voyage-3",
		Texts:        []string{"top secret document text", "another secret"},
	})
	result.InputTokens = 8
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Embedded text must be redacted.
	for i := range []string{"", ""} {
		key := fmt.Sprintf("gen_ai.prompt.%d.content", i)
		if val, ok := attrs[key]; !ok {
			t.Errorf("expected %s to be present (as [redacted]), but it was absent", key)
		} else if val != "[redacted]" {
			t.Errorf("%s = %q, want '[redacted]'", key, val)
		}
	}

	// Tokens and model must still be present after redaction.
	if attrs["gen_ai.usage.input_tokens"] != int64(8) {
		t.Errorf("tokens missing or wrong after redaction: input_tokens = %v", attrs["gen_ai.usage.input_tokens"])
	}
	if attrs["gen_ai.request.model"] != "voyage-3" {
		t.Errorf("model should be preserved after redaction: %q", attrs["gen_ai.request.model"])
	}
	if attrs["gen_ai.system"] != "voyage" {
		t.Errorf("system should be preserved after redaction: %q", attrs["gen_ai.system"])
	}
}

// TestEmbeddingSpan_ResolvesToEmbeddingKind asserts that the span's
// gen_ai.operation.name = "embeddings" satisfies the observer's
// hasEmbeddingAttributes check (process.go), which distinguishes it from "llm"
// even though both kinds share gen_ai.prompt.* attributes.
func TestEmbeddingSpan_ResolvesToEmbeddingKind(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "openai",
		RequestModel: "text-embedding-3-large",
		Texts:        []string{"test document"},
	})
	result.InputTokens = 2
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	opName, ok := attrs["gen_ai.operation.name"].(string)
	if !ok {
		t.Fatal("gen_ai.operation.name missing or not a string")
	}

	// The observer's hasEmbeddingAttributes accepts "embedding" or "embeddings".
	// It checks embedding BEFORE llm in DetermineSpanType, so this is what routes
	// the span to kind=embedding rather than kind=llm.
	if opName != "embedding" && opName != "embeddings" {
		t.Errorf("gen_ai.operation.name = %q; observer will not resolve this to 'embedding' kind", opName)
	}
}

// TestEmbeddingSpan_SpanKindIsClient asserts that the embedding span uses
// SpanKindClient, matching the Python reference (SpanKind.CLIENT).
func TestEmbeddingSpan_SpanKindIsClient(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "voyage",
		RequestModel: "voyage-3",
	})
	result.InputTokens = 0
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "client" {
		t.Errorf("span kind = %q, want 'client'", s.SpanKind().String())
	}
}
