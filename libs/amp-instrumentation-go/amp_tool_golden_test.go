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

// Package amp_test contains golden tests that assert ToolSpan emits exactly the
// attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "tool" kind.
package amp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestToolSpan_GoldenContractKeys asserts that ToolSpan emits exactly the keys
// declared in the published contract for the "tool" kind:
//
//	gen_ai.operation.name  = "execute_tool"   (required — observer discriminator)
//	gen_ai.tool.name                           (required)
//
// And optional keys when data is provided:
//
//	gen_ai.tool.description
//	gen_ai.tool.call.id
//	traceloop.entity.input
//	traceloop.entity.output
func TestToolSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span, result := amp.ToolSpan(ctx, amp.ToolInput{
		Name:        "get_weather",
		Description: "Fetches current weather for a location",
		CallID:      "call_abc123",
		Arguments:   map[string]any{"location": "London", "units": "celsius"},
	})
	result.Output = map[string]any{"temp": 15, "condition": "cloudy"}
	span.End()
	_ = ctx

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required keys per contract.
	requiredKeys := []string{
		"gen_ai.operation.name",
		"gen_ai.tool.name",
	}
	for _, k := range requiredKeys {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required contract key %q missing from tool span", k)
		}
	}

	// Required value constraints.
	if attrs["gen_ai.operation.name"] != "execute_tool" {
		t.Errorf("gen_ai.operation.name = %q, want 'execute_tool'", attrs["gen_ai.operation.name"])
	}
	if attrs["gen_ai.tool.name"] != "get_weather" {
		t.Errorf("gen_ai.tool.name = %q, want 'get_weather'", attrs["gen_ai.tool.name"])
	}

	// Optional keys present when data supplied.
	if attrs["gen_ai.tool.description"] != "Fetches current weather for a location" {
		t.Errorf("gen_ai.tool.description = %q", attrs["gen_ai.tool.description"])
	}
	if attrs["gen_ai.tool.call.id"] != "call_abc123" {
		t.Errorf("gen_ai.tool.call.id = %q", attrs["gen_ai.tool.call.id"])
	}
	if _, ok := attrs["traceloop.entity.input"]; !ok {
		t.Error("traceloop.entity.input should be present when Arguments is set")
	}
	if _, ok := attrs["traceloop.entity.output"]; !ok {
		t.Error("traceloop.entity.output should be present when Output is set")
	}

	// Span name must be "execute_tool".
	if s.Name() != "execute_tool" {
		t.Errorf("span name = %q, want 'execute_tool'", s.Name())
	}
}

// TestToolSpan_GoldenContractKeys_RequiredOnly asserts that required keys are
// present and optional ones are absent when no optional data is supplied.
func TestToolSpan_GoldenContractKeys_RequiredOnly(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ToolSpan(ctx, amp.ToolInput{
		Name: "noop_tool",
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required keys must be present.
	for _, k := range []string{"gen_ai.operation.name", "gen_ai.tool.name"} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}

	// Optional keys must NOT be present when not set.
	for _, k := range []string{
		"gen_ai.tool.description",
		"gen_ai.tool.call.id",
		"traceloop.entity.input",
		"traceloop.entity.output",
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("optional key %q should be absent when not provided, but was present", k)
		}
	}
}

// TestToolSpan_ContentRedaction asserts that AMP_TRACE_CONTENT=false redacts
// tool arguments and output but preserves tool name, description, and call id.
func TestToolSpan_ContentRedaction(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.ToolSpan(ctx, amp.ToolInput{
		Name:        "search",
		Description: "Web search tool",
		CallID:      "call_redact_test",
		Arguments:   map[string]any{"query": "secret keywords"},
	})
	result.Output = map[string]any{"results": []any{"secret result 1"}}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Tool name, description, and call id must be preserved (not content).
	if attrs["gen_ai.tool.name"] != "search" {
		t.Errorf("tool name should be preserved after redaction, got %q", attrs["gen_ai.tool.name"])
	}
	if attrs["gen_ai.tool.description"] != "Web search tool" {
		t.Errorf("tool description should be preserved after redaction, got %q", attrs["gen_ai.tool.description"])
	}
	if attrs["gen_ai.tool.call.id"] != "call_redact_test" {
		t.Errorf("tool call.id should be preserved after redaction, got %q", attrs["gen_ai.tool.call.id"])
	}

	// Input arguments must be redacted.
	inputJSON, ok := attrs["traceloop.entity.input"].(string)
	if !ok {
		t.Fatal("traceloop.entity.input not found or not a string")
	}
	var inputMap map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		t.Fatalf("cannot unmarshal traceloop.entity.input: %v", err)
	}
	if inputMap["query"] != "[redacted]" {
		t.Errorf("tool argument 'query' should be '[redacted]', got %q", inputMap["query"])
	}

	// Output must be redacted.
	outputJSON, ok := attrs["traceloop.entity.output"].(string)
	if !ok {
		t.Fatal("traceloop.entity.output not found or not a string")
	}
	var outputMap map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &outputMap); err != nil {
		t.Fatalf("cannot unmarshal traceloop.entity.output: %v", err)
	}
	// The results list items should be redacted.
	resultsList, ok := outputMap["results"].([]any)
	if !ok {
		t.Fatalf("output.results should be a list, got %T", outputMap["results"])
	}
	if len(resultsList) == 0 {
		t.Fatal("output.results should have one item")
	}
	if resultsList[0] != "[redacted]" {
		t.Errorf("output.results[0] should be '[redacted]', got %q", resultsList[0])
	}
}

// TestToolSpan_SpanKindIsInternal asserts that the tool span uses SpanKindInternal,
// matching the Python reference (SpanKind.INTERNAL).
func TestToolSpan_SpanKindIsInternal(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ToolSpan(ctx, amp.ToolInput{Name: "any_tool"})
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "internal" {
		t.Errorf("span kind = %q, want 'internal'", s.SpanKind().String())
	}
}

// TestToolSpan_Error asserts that span.Error marks the span with error status
// and sets the error.type attribute on a tool span.
func TestToolSpan_Error(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.ToolSpan(ctx, amp.ToolInput{Name: "failing_tool"})
	span.Error(errors.New("tool execution failed"))
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
