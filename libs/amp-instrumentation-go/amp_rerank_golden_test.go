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

// Package amp_test contains golden tests that assert RerankSpan emits exactly
// the attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "rerank" kind.
//
// The contract for rerank:
//
//	Kind: "rerank"
//	Attributes: [{Key: "traceloop.span.kind", Type: "string", Const: "rerank"}]
//
// The Python reference (samples/manual-instrumentation-agent/instrumentation.py
// → rerank_span) additionally sets:
//
//	gen_ai.operation.name = "rerank"   (de-facto, not standard OTel)
//	rerank.model                        (reranker model name)
//	gen_ai.request.model                (same model value)
//	traceloop.entity.input              (JSON: {query, candidate_count})
package amp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestRerankSpan_GoldenContractKeys asserts that RerankSpan emits exactly the
// keys declared in the published contract for the "rerank" kind:
//
//	traceloop.span.kind = "rerank"   (required — observer discriminator)
//
// And the de-facto signal keys from the Python reference:
//
//	gen_ai.operation.name = "rerank"
//	rerank.model
//	gen_ai.request.model
//	traceloop.entity.input (JSON: {"query":..., "candidate_count":...})
func TestRerankSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-english-v3.0",
		Query:          "What is WSO2 Agent Manager?",
		CandidateCount: 10,
	})
	span.End()
	_ = ctx

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required key per contract — this is what the observer's traceloop.span.kind
	// switch (process.go) keys off to resolve the span to kind = rerank.
	if attrs["traceloop.span.kind"] != "rerank" {
		t.Errorf("traceloop.span.kind = %q, want 'rerank'", attrs["traceloop.span.kind"])
	}

	// De-facto signal keys from the Python reference.
	if attrs["gen_ai.operation.name"] != "rerank" {
		t.Errorf("gen_ai.operation.name = %q, want 'rerank'", attrs["gen_ai.operation.name"])
	}
	if attrs["rerank.model"] != "rerank-english-v3.0" {
		t.Errorf("rerank.model = %q, want 'rerank-english-v3.0'", attrs["rerank.model"])
	}
	if attrs["gen_ai.request.model"] != "rerank-english-v3.0" {
		t.Errorf("gen_ai.request.model = %q, want 'rerank-english-v3.0'", attrs["gen_ai.request.model"])
	}

	// traceloop.entity.input must be present and carry query + candidate_count.
	inputJSON, ok := attrs["traceloop.entity.input"].(string)
	if !ok {
		t.Fatal("traceloop.entity.input missing or not a string")
	}
	var inputMap map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("traceloop.entity.input is not valid JSON: %v", err)
	}
	if inputMap["query"] != "What is WSO2 Agent Manager?" {
		t.Errorf("traceloop.entity.input[query] = %q, want 'What is WSO2 Agent Manager?'", inputMap["query"])
	}
	// candidate_count is stored as a JSON number; JSON Unmarshal gives float64.
	if candidateCount, ok := inputMap["candidate_count"].(float64); !ok || int(candidateCount) != 10 {
		t.Errorf("traceloop.entity.input[candidate_count] = %v, want 10", inputMap["candidate_count"])
	}

	// Span name must be "rerank" (matching Python reference).
	if s.Name() != "rerank" {
		t.Errorf("span name = %q, want 'rerank'", s.Name())
	}
}

// TestRerankSpan_ResolvesToRerankKind asserts that the observer would resolve
// this span to kind = "rerank" via the traceloop.span.kind attribute.
//
// The observer's process.go traceloop.span.kind switch (case "rerank") and
// hasRerankAttributes (gen_ai.operation.name == "rerank" || rerank.model set)
// both resolve to the rerank kind.
func TestRerankSpan_ResolvesToRerankKind(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-multilingual-v3.0",
		Query:          "test query",
		CandidateCount: 5,
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Primary discriminator: traceloop.span.kind = "rerank"
	// (process.go → case "rerank" in the traceloop.span.kind switch)
	spanKind, ok := attrs["traceloop.span.kind"].(string)
	if !ok {
		t.Fatal("traceloop.span.kind missing or not a string")
	}
	if spanKind != "rerank" {
		t.Errorf("traceloop.span.kind = %q; observer will not resolve to 'rerank' kind", spanKind)
	}

	// Secondary discriminator: gen_ai.operation.name = "rerank"
	// (process.go → hasRerankAttributes checks opName == "rerank")
	opName, _ := attrs["gen_ai.operation.name"].(string)
	if opName != "rerank" {
		t.Errorf("gen_ai.operation.name = %q; hasRerankAttributes will not match", opName)
	}

	// Tertiary discriminator: rerank.model set
	// (process.go → hasRerankAttributes checks rerank.model)
	if _, ok := attrs["rerank.model"]; !ok {
		t.Error("rerank.model missing; hasRerankAttributes fallback will not match")
	}
}

// TestRerankSpan_ContentRedaction asserts that AMP_TRACE_CONTENT=false redacts
// the query inside traceloop.entity.input but keeps candidate_count (a number)
// and the model name (not free-text content).
func TestRerankSpan_ContentRedaction(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-english-v3.0",
		Query:          "secret query text",
		CandidateCount: 7,
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Model names are NOT content — they must be preserved.
	if attrs["rerank.model"] != "rerank-english-v3.0" {
		t.Errorf("rerank.model should be preserved after redaction, got %q", attrs["rerank.model"])
	}
	if attrs["gen_ai.request.model"] != "rerank-english-v3.0" {
		t.Errorf("gen_ai.request.model should be preserved after redaction, got %q", attrs["gen_ai.request.model"])
	}

	// traceloop.entity.input must still be present (structure preserved) but
	// the query value must be "[redacted]".
	inputJSON, ok := attrs["traceloop.entity.input"].(string)
	if !ok {
		t.Fatal("traceloop.entity.input not found or not a string after redaction")
	}
	var inputMap map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("traceloop.entity.input is not valid JSON after redaction: %v", err)
	}

	// Query (string) must be redacted.
	if inputMap["query"] != "[redacted]" {
		t.Errorf("traceloop.entity.input[query] should be '[redacted]', got %q", inputMap["query"])
	}

	// candidate_count (number) must be preserved — numbers are not content.
	if candidateCount, ok := inputMap["candidate_count"].(float64); !ok || int(candidateCount) != 7 {
		t.Errorf("traceloop.entity.input[candidate_count] = %v, want 7 (numbers preserved under redaction)", inputMap["candidate_count"])
	}
}

// TestRerankSpan_SpanKindIsClient asserts that the rerank span uses
// SpanKindClient, matching the Python reference (SpanKind.CLIENT).
func TestRerankSpan_SpanKindIsClient(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-english-v3.0",
		Query:          "test query",
		CandidateCount: 3,
	})
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "client" {
		t.Errorf("span kind = %q, want 'client'", s.SpanKind().String())
	}
}

// TestRerankSpan_Error asserts that span.Error marks the span with error
// status and sets the error.type attribute on a rerank span.
func TestRerankSpan_Error(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-english-v3.0",
		Query:          "test",
		CandidateCount: 2,
	})
	span.Error(errors.New("rerank service unavailable"))
	span.End()

	s := lastSpan(t, rec)

	status := s.Status()
	if status.Code.String() != "Error" {
		t.Errorf("expected error status code, got %v", status.Code)
	}
	attrs := attrMap(s)
	if _, ok := attrs["error.type"]; !ok {
		t.Error("error.type attribute missing after span.Error()")
	}
}

// TestRerankSpan_TraceloopSpanKindIsOnlyContractKey asserts that
// traceloop.span.kind is always "rerank" — the sole key declared in the
// published contract for this kind.
func TestRerankSpan_TraceloopSpanKindIsOnlyContractKey(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-english-v3.0",
		Query:          "query for contract check",
		CandidateCount: 4,
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// The sole published contract key for rerank kind.
	if v, ok := attrs["traceloop.span.kind"]; !ok || v != "rerank" {
		t.Errorf("traceloop.span.kind = %v, want 'rerank' (the published contract key)", v)
	}
}
