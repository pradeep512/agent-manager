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

// Package amp_test contains golden tests that assert ChainSpan emits exactly
// the attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "chain" kind.
//
// The chain kind has NO OTel gen_ai.* key — it is discriminated purely by the
// Layer-2 key traceloop.span.kind = "workflow", which the observer maps to the
// "chain" kind (process.go: case "task", "workflow": return SpanTypeChain).
package amp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestChainSpan_GoldenContractKeys asserts that ChainSpan emits the required
// Layer-2 discriminator key and input/output entity keys for the "chain" kind.
//
// The observer contract (process.go) resolves traceloop.span.kind = "workflow"
// to SpanTypeChain. No gen_ai.* key is used for this kind.
func TestChainSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span, result := amp.ChainSpan(ctx, amp.ChainInput{
		Name:  "retrieval-pipeline",
		Input: map[string]any{"query": "what is WSO2?", "top_k": 5},
	})
	result.Output = map[string]any{"answer": "WSO2 is an open-source company", "sources": []any{"doc1", "doc2"}}
	span.End()
	_ = ctx

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required discriminator key — maps to chain kind in the observer.
	if attrs["traceloop.span.kind"] != "workflow" {
		t.Errorf("traceloop.span.kind = %q, want 'workflow'", attrs["traceloop.span.kind"])
	}

	// Input entity key must be present when Input is provided.
	if _, ok := attrs["traceloop.entity.input"]; !ok {
		t.Error("traceloop.entity.input should be present when Input is set")
	}
	// Output entity key must be present when Output is set before End.
	if _, ok := attrs["traceloop.entity.output"]; !ok {
		t.Error("traceloop.entity.output should be present when result.Output is set")
	}

	// Span name must be the caller-provided name (mirrors Python chain_span).
	if s.Name() != "retrieval-pipeline" {
		t.Errorf("span name = %q, want 'retrieval-pipeline'", s.Name())
	}
}

// TestChainSpan_InputOutputFormat asserts the JSON shape of entity input/output
// matches the Python reference:
//
//	traceloop.entity.input  = {"input": <workflow_input>}
//	traceloop.entity.output = {"output": <workflow_output>}
func TestChainSpan_InputOutputFormat(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.ChainSpan(ctx, amp.ChainInput{
		Name:  "summariser",
		Input: map[string]any{"text": "long document content"},
	})
	result.Output = map[string]any{"summary": "short summary"}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// entity.input must be wrapped: {"input": ...}
	inputJSON, ok := attrs["traceloop.entity.input"].(string)
	if !ok {
		t.Fatal("traceloop.entity.input not found or not a string")
	}
	var inputEnvelope map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputEnvelope); err != nil {
		t.Fatalf("cannot unmarshal traceloop.entity.input: %v", err)
	}
	if _, ok := inputEnvelope["input"]; !ok {
		t.Errorf("traceloop.entity.input JSON should have top-level 'input' key, got: %s", inputJSON)
	}

	// entity.output must be wrapped: {"output": ...}
	outputJSON, ok := attrs["traceloop.entity.output"].(string)
	if !ok {
		t.Fatal("traceloop.entity.output not found or not a string")
	}
	var outputEnvelope map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &outputEnvelope); err != nil {
		t.Fatalf("cannot unmarshal traceloop.entity.output: %v", err)
	}
	if _, ok := outputEnvelope["output"]; !ok {
		t.Errorf("traceloop.entity.output JSON should have top-level 'output' key, got: %s", outputJSON)
	}
}

// TestChainSpan_SpanKindIsInternal asserts SpanKindInternal, matching the Python
// reference (chain_span uses SpanKind.INTERNAL).
func TestChainSpan_SpanKindIsInternal(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ChainSpan(ctx, amp.ChainInput{Name: "pipeline"})
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "internal" {
		t.Errorf("span kind = %q, want 'internal'", s.SpanKind().String())
	}
}

// TestChainSpan_NoInputOutput asserts that entity.input and entity.output are
// absent when no Input/Output is provided.
func TestChainSpan_NoInputOutput(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ChainSpan(ctx, amp.ChainInput{Name: "empty-chain"})
	// result.Output is not set
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Discriminator must always be present.
	if attrs["traceloop.span.kind"] != "workflow" {
		t.Errorf("traceloop.span.kind = %q, want 'workflow'", attrs["traceloop.span.kind"])
	}

	// Input/output absent when not provided.
	if _, ok := attrs["traceloop.entity.input"]; ok {
		t.Error("traceloop.entity.input should be absent when Input is nil/empty")
	}
	if _, ok := attrs["traceloop.entity.output"]; ok {
		t.Error("traceloop.entity.output should be absent when result.Output is nil")
	}
}

// TestChainSpan_ContentRedaction asserts that AMP_TRACE_CONTENT=false redacts
// the input and output string values but preserves structure (keys, shape).
func TestChainSpan_ContentRedaction(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.ChainSpan(ctx, amp.ChainInput{
		Name:  "redaction-test",
		Input: map[string]any{"secret_query": "classified question"},
	})
	result.Output = map[string]any{"secret_answer": "classified response"}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// entity.input must be present but string values redacted.
	inputJSON, ok := attrs["traceloop.entity.input"].(string)
	if !ok {
		t.Fatal("traceloop.entity.input not found or not a string")
	}
	var inputEnvelope map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputEnvelope); err != nil {
		t.Fatalf("cannot unmarshal traceloop.entity.input: %v", err)
	}
	// The outer "input" key must exist (structure preserved).
	innerInput, ok := inputEnvelope["input"].(map[string]any)
	if !ok {
		t.Fatalf("traceloop.entity.input['input'] should be a map, got: %T", inputEnvelope["input"])
	}
	// The string value should be redacted.
	if innerInput["secret_query"] != "[redacted]" {
		t.Errorf("input 'secret_query' should be '[redacted]', got %q", innerInput["secret_query"])
	}

	// entity.output must be present but string values redacted.
	outputJSON, ok := attrs["traceloop.entity.output"].(string)
	if !ok {
		t.Fatal("traceloop.entity.output not found or not a string")
	}
	var outputEnvelope map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &outputEnvelope); err != nil {
		t.Fatalf("cannot unmarshal traceloop.entity.output: %v", err)
	}
	innerOutput, ok := outputEnvelope["output"].(map[string]any)
	if !ok {
		t.Fatalf("traceloop.entity.output['output'] should be a map, got: %T", outputEnvelope["output"])
	}
	if innerOutput["secret_answer"] != "[redacted]" {
		t.Errorf("output 'secret_answer' should be '[redacted]', got %q", innerOutput["secret_answer"])
	}
}

// TestChainSpan_ObserverKindResolution verifies the traceloop.span.kind=workflow
// value is exactly what the observer's switch statement matches to SpanTypeChain:
//
//	case "task", "workflow": return SpanTypeChain
//
// This is a contract assertion — if this value changes, the chain kind breaks.
func TestChainSpan_ObserverKindResolution(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ChainSpan(ctx, amp.ChainInput{Name: "observer-check"})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	kind, ok := attrs["traceloop.span.kind"].(string)
	if !ok {
		t.Fatal("traceloop.span.kind not found or not a string")
	}
	// The observer (process.go) maps "task" and "workflow" to SpanTypeChain.
	// We emit "workflow" to match the Python reference.
	if kind != "workflow" {
		t.Errorf("traceloop.span.kind = %q; observer maps 'workflow' -> SpanTypeChain; this will break kind resolution", kind)
	}
}

// TestChainSpan_Error asserts that span.Error marks the span with error status
// and sets the error.type attribute on a chain span.
func TestChainSpan_Error(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ChainSpan(ctx, amp.ChainInput{Name: "failing-chain"})
	span.Error(errors.New("pipeline step failed"))
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
