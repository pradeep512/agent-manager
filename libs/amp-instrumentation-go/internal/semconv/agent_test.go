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

package semconv_test

import (
	"testing"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/semconv"
)

// TestAgentAttributes_RequiredKeys asserts that AgentAttributes emits
// gen_ai.operation.name = "invoke_agent" and gen_ai.agent.name (required by the
// observer's hasAgentAttributes path).
func TestAgentAttributes_RequiredKeys(t *testing.T) {
	a := semconv.AgentAttrs{
		Name:               "my-agent",
		Description:        "A helpful assistant",
		Framework:          "custom",
		RequestModel:       "claude-3-5-sonnet-20241022",
		SystemInstructions: "You are a helpful assistant.",
		ConversationID:     "conv-123",
		Tools:              `[{"name":"search"}]`,
		InputMessages:      `[{"role":"user","content":"Hello"}]`,
		OutputMessages:     `[{"role":"assistant","content":"Hi"}]`,
	}
	attrs := semconv.AgentAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	requiredKeys := []string{
		"gen_ai.operation.name",
		"gen_ai.agent.name",
	}
	for _, k := range requiredKeys {
		if _, ok := found[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}
	if found["gen_ai.operation.name"] != "invoke_agent" {
		t.Errorf("gen_ai.operation.name = %v, want 'invoke_agent'", found["gen_ai.operation.name"])
	}
	if found["gen_ai.agent.name"] != "my-agent" {
		t.Errorf("gen_ai.agent.name = %v, want 'my-agent'", found["gen_ai.agent.name"])
	}
}

// TestAgentAttributes_OptionalKeys asserts that optional keys are emitted when
// data is provided and absent when not.
func TestAgentAttributes_OptionalKeys(t *testing.T) {
	a := semconv.AgentAttrs{
		Name:               "my-agent",
		Description:        "A helpful assistant",
		Framework:          "custom",
		RequestModel:       "claude-3-5-sonnet-20241022",
		SystemInstructions: "You are helpful.",
		ConversationID:     "conv-456",
		Tools:              `[{"name":"search"}]`,
		InputMessages:      `[{"role":"user","content":"Hello"}]`,
		OutputMessages:     `[{"role":"assistant","content":"Hi"}]`,
	}
	attrs := semconv.AgentAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	// All optional keys should be present when data is supplied.
	if found["gen_ai.agent.description"] != "A helpful assistant" {
		t.Errorf("gen_ai.agent.description = %v", found["gen_ai.agent.description"])
	}
	if found["gen_ai.system"] != "custom" {
		t.Errorf("gen_ai.system = %v, want 'custom'", found["gen_ai.system"])
	}
	if found["gen_ai.request.model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("gen_ai.request.model = %v", found["gen_ai.request.model"])
	}
	if found["gen_ai.system_instructions"] != "You are helpful." {
		t.Errorf("gen_ai.system_instructions = %v", found["gen_ai.system_instructions"])
	}
	if found["gen_ai.conversation.id"] != "conv-456" {
		t.Errorf("gen_ai.conversation.id = %v", found["gen_ai.conversation.id"])
	}
	if found["gen_ai.agent.tools"] != `[{"name":"search"}]` {
		t.Errorf("gen_ai.agent.tools = %v", found["gen_ai.agent.tools"])
	}
	if found["gen_ai.input.messages"] != `[{"role":"user","content":"Hello"}]` {
		t.Errorf("gen_ai.input.messages = %v", found["gen_ai.input.messages"])
	}
	if found["gen_ai.output.messages"] != `[{"role":"assistant","content":"Hi"}]` {
		t.Errorf("gen_ai.output.messages = %v", found["gen_ai.output.messages"])
	}
}

// TestAgentAttributes_OptionalKeysAbsentWhenEmpty asserts that empty/zero optional
// fields are omitted from the output.
func TestAgentAttributes_OptionalKeysAbsentWhenEmpty(t *testing.T) {
	a := semconv.AgentAttrs{
		Name: "minimal-agent",
	}
	attrs := semconv.AgentAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	absentWhenEmpty := []string{
		"gen_ai.agent.description",
		"gen_ai.system",
		"gen_ai.request.model",
		"gen_ai.system_instructions",
		"gen_ai.conversation.id",
		"gen_ai.agent.tools",
		"gen_ai.input.messages",
		"gen_ai.output.messages",
	}
	for _, k := range absentWhenEmpty {
		if _, ok := found[k]; ok {
			t.Errorf("key %q should be absent when empty, but was present with value %v", k, found[k])
		}
	}
}

// TestAgentAttributes_OperationNameIsInvokeAgent asserts the span name discriminator.
func TestAgentAttributes_OperationNameIsInvokeAgent(t *testing.T) {
	attrs := semconv.AgentAttributes(semconv.AgentAttrs{Name: "any"})
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.operation.name" {
			if kv.Value.AsString() != "invoke_agent" {
				t.Errorf("gen_ai.operation.name = %q, want 'invoke_agent'", kv.Value.AsString())
			}
			return
		}
	}
	t.Error("gen_ai.operation.name not found in attributes")
}
