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

// Package amp_test contains golden tests that assert AgentSpan emits exactly
// the attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "agent" kind.
package amp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestAgentSpan_GoldenContractKeys asserts that AgentSpan emits exactly the keys
// declared in the published contract for the "agent" kind:
//
//	gen_ai.operation.name  = "invoke_agent"  (observer discriminator)
//	gen_ai.agent.name                         (required)
//
// And optional keys when data is provided:
//
//	gen_ai.agent.description
//	gen_ai.system                  (framework)
//	gen_ai.request.model
//	gen_ai.system_instructions
//	gen_ai.conversation.id
//	gen_ai.agent.tools
//	gen_ai.input.messages
//	gen_ai.output.messages
func TestAgentSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span, result := amp.AgentSpan(ctx, amp.AgentInput{
		Name:               "rag-agent",
		Description:        "A retrieval-augmented generation agent",
		Framework:          "custom",
		RequestModel:       "claude-3-5-sonnet-20241022",
		SystemInstructions: "You are a helpful assistant.",
		ConversationID:     "conv-abc123",
		Tools:              []map[string]any{{"name": "search", "description": "Web search"}},
		InputMessages:      []map[string]any{{"role": "user", "content": "What is Go?"}},
	})
	result.OutputMessages = []map[string]any{{"role": "assistant", "content": "Go is a language."}}
	result.InputTokens = 20
	result.OutputTokens = 10
	span.End()
	_ = ctx

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required keys per contract (observer discriminator + agent name).
	requiredKeys := []string{
		"gen_ai.operation.name",
		"gen_ai.agent.name",
	}
	for _, k := range requiredKeys {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required contract key %q missing from agent span", k)
		}
	}

	// Required value constraints.
	if attrs["gen_ai.operation.name"] != "invoke_agent" {
		t.Errorf("gen_ai.operation.name = %q, want 'invoke_agent'", attrs["gen_ai.operation.name"])
	}
	if attrs["gen_ai.agent.name"] != "rag-agent" {
		t.Errorf("gen_ai.agent.name = %q, want 'rag-agent'", attrs["gen_ai.agent.name"])
	}

	// Optional keys present when data supplied.
	if attrs["gen_ai.agent.description"] != "A retrieval-augmented generation agent" {
		t.Errorf("gen_ai.agent.description = %q", attrs["gen_ai.agent.description"])
	}
	if attrs["gen_ai.system"] != "custom" {
		t.Errorf("gen_ai.system = %q, want 'custom'", attrs["gen_ai.system"])
	}
	if attrs["gen_ai.request.model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("gen_ai.request.model = %q", attrs["gen_ai.request.model"])
	}
	if attrs["gen_ai.system_instructions"] != "You are a helpful assistant." {
		t.Errorf("gen_ai.system_instructions = %q", attrs["gen_ai.system_instructions"])
	}
	if attrs["gen_ai.conversation.id"] != "conv-abc123" {
		t.Errorf("gen_ai.conversation.id = %q", attrs["gen_ai.conversation.id"])
	}
	if _, ok := attrs["gen_ai.agent.tools"]; !ok {
		t.Error("gen_ai.agent.tools should be present when Tools is set")
	}
	if _, ok := attrs["gen_ai.input.messages"]; !ok {
		t.Error("gen_ai.input.messages should be present when InputMessages is set")
	}
	if _, ok := attrs["gen_ai.output.messages"]; !ok {
		t.Error("gen_ai.output.messages should be present when OutputMessages is set")
	}

	// Span name must be "invoke_agent".
	if s.Name() != "invoke_agent" {
		t.Errorf("span name = %q, want 'invoke_agent'", s.Name())
	}
}

// TestAgentSpan_GoldenContractKeys_RequiredOnly asserts that required keys are
// present and optional ones are absent when no optional data is supplied.
func TestAgentSpan_GoldenContractKeys_RequiredOnly(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{
		Name: "minimal-agent",
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required keys must be present.
	for _, k := range []string{"gen_ai.operation.name", "gen_ai.agent.name"} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}

	// Optional keys must NOT be present when not set.
	for _, k := range []string{
		"gen_ai.agent.description",
		"gen_ai.system",
		"gen_ai.request.model",
		"gen_ai.system_instructions",
		"gen_ai.conversation.id",
		"gen_ai.agent.tools",
		"gen_ai.input.messages",
		"gen_ai.output.messages",
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("optional key %q should be absent when not provided, but was present", k)
		}
	}
}

// TestAgentSpan_ContentRedaction asserts that AMP_TRACE_CONTENT=false redacts
// system instructions and message text but preserves roles, structure, and
// agent name/model.
func TestAgentSpan_ContentRedaction(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, result := amp.AgentSpan(ctx, amp.AgentInput{
		Name:               "secret-agent",
		Framework:          "custom",
		RequestModel:       "claude-3-5-sonnet-20241022",
		SystemInstructions: "Top secret system prompt.",
		ConversationID:     "conv-redact",
		InputMessages:      []map[string]any{{"role": "user", "content": "Tell me the secret."}},
	})
	result.OutputMessages = []map[string]any{{"role": "assistant", "content": "The secret is 42."}}
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Agent name, framework, model, and conversation id must be preserved.
	if attrs["gen_ai.agent.name"] != "secret-agent" {
		t.Errorf("agent name should be preserved, got %q", attrs["gen_ai.agent.name"])
	}
	if attrs["gen_ai.system"] != "custom" {
		t.Errorf("gen_ai.system should be preserved, got %q", attrs["gen_ai.system"])
	}
	if attrs["gen_ai.request.model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("model should be preserved, got %q", attrs["gen_ai.request.model"])
	}
	if attrs["gen_ai.conversation.id"] != "conv-redact" {
		t.Errorf("conversation.id should be preserved, got %q", attrs["gen_ai.conversation.id"])
	}

	// System instructions must be redacted.
	if attrs["gen_ai.system_instructions"] != "[redacted]" {
		t.Errorf("system_instructions should be '[redacted]', got %q", attrs["gen_ai.system_instructions"])
	}

	// Input messages must have content redacted, role preserved.
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

	// Output messages must have content redacted, role preserved.
	outputJSON, ok := attrs["gen_ai.output.messages"].(string)
	if !ok {
		t.Fatal("gen_ai.output.messages not found or not a string")
	}
	var outputMsgs []map[string]any
	if err := json.Unmarshal([]byte(outputJSON), &outputMsgs); err != nil {
		t.Fatalf("cannot unmarshal output messages: %v", err)
	}
	if len(outputMsgs) == 0 {
		t.Fatal("output messages slice is empty")
	}
	if outputMsgs[0]["content"] != "[redacted]" {
		t.Errorf("output content should be '[redacted]', got %q", outputMsgs[0]["content"])
	}
	if outputMsgs[0]["role"] != "assistant" {
		t.Errorf("output role should be preserved 'assistant', got %q", outputMsgs[0]["role"])
	}
}

// TestAgentSpan_SpanKindIsInternal asserts that the agent span uses
// SpanKindInternal, matching the Python reference (SpanKind.INTERNAL).
func TestAgentSpan_SpanKindIsInternal(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "any-agent"})
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "internal" {
		t.Errorf("span kind = %q, want 'internal'", s.SpanKind().String())
	}
}

// TestAgentSpan_Error asserts that span.Error marks the span with error status
// and sets the error.type attribute on an agent span.
func TestAgentSpan_Error(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "failing-agent"})
	span.Error(errors.New("agent invocation failed"))
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

// TestAgentSpan_ToolsSerializedToJSON asserts that the Tools slice is correctly
// serialised to JSON in gen_ai.agent.tools.
func TestAgentSpan_ToolsSerializedToJSON(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	tools := []map[string]any{
		{"name": "search", "description": "Web search"},
		{"name": "calculator", "description": "Math operations"},
	}
	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{
		Name:  "tool-agent",
		Tools: tools,
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	toolsJSON, ok := attrs["gen_ai.agent.tools"].(string)
	if !ok {
		t.Fatal("gen_ai.agent.tools not found or not a string")
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(toolsJSON), &decoded); err != nil {
		t.Fatalf("gen_ai.agent.tools is not valid JSON: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 tools, got %d", len(decoded))
	}
	if decoded[0]["name"] != "search" {
		t.Errorf("first tool name = %q, want 'search'", decoded[0]["name"])
	}
}

// TestAgentSpan_OutputMessages_AbsentWhenEmpty asserts that gen_ai.output.messages
// is absent when AgentResult.OutputMessages is nil/empty.
func TestAgentSpan_OutputMessages_AbsentWhenEmpty(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "no-output-agent"})
	// Do not set result.OutputMessages.
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	if _, ok := attrs["gen_ai.output.messages"]; ok {
		t.Error("gen_ai.output.messages should be absent when OutputMessages is empty")
	}
}
