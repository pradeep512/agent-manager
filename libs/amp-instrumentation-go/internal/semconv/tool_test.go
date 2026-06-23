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

// TestToolAttributes_RequiredKeys asserts that ToolAttributes emits
// gen_ai.operation.name = "execute_tool" and gen_ai.tool.name (required by the
// observer's hasToolAttributes path).
func TestToolAttributes_RequiredKeys(t *testing.T) {
	a := semconv.ToolAttrs{
		Name:        "get_weather",
		Description: "Fetches current weather",
		CallID:      "call_abc123",
		Input:       `{"location":"London"}`,
		Output:      `{"temp":15}`,
	}
	attrs := semconv.ToolAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	requiredKeys := []string{
		"gen_ai.operation.name",
		"gen_ai.tool.name",
	}
	for _, k := range requiredKeys {
		if _, ok := found[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}
	if found["gen_ai.operation.name"] != "execute_tool" {
		t.Errorf("gen_ai.operation.name = %v, want 'execute_tool'", found["gen_ai.operation.name"])
	}
	if found["gen_ai.tool.name"] != "get_weather" {
		t.Errorf("gen_ai.tool.name = %v, want 'get_weather'", found["gen_ai.tool.name"])
	}
}

// TestToolAttributes_OptionalKeys asserts that optional keys are emitted when data
// is provided and absent when not.
func TestToolAttributes_OptionalKeys(t *testing.T) {
	a := semconv.ToolAttrs{
		Name:        "search",
		Description: "web search",
		CallID:      "call_xyz",
		Input:       `{"query":"golang"}`,
		Output:      `{"results":[]}`,
	}
	attrs := semconv.ToolAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	// Description and call id should be present.
	if found["gen_ai.tool.description"] != "web search" {
		t.Errorf("gen_ai.tool.description = %v, want 'web search'", found["gen_ai.tool.description"])
	}
	if found["gen_ai.tool.call.id"] != "call_xyz" {
		t.Errorf("gen_ai.tool.call.id = %v, want 'call_xyz'", found["gen_ai.tool.call.id"])
	}

	// Layer-2 input/output keys.
	if found["traceloop.entity.input"] != `{"query":"golang"}` {
		t.Errorf("traceloop.entity.input = %v", found["traceloop.entity.input"])
	}
	if found["traceloop.entity.output"] != `{"results":[]}` {
		t.Errorf("traceloop.entity.output = %v", found["traceloop.entity.output"])
	}
}

// TestToolAttributes_OptionalKeysAbsentWhenEmpty asserts that empty/zero optional
// fields are omitted from the output.
func TestToolAttributes_OptionalKeysAbsentWhenEmpty(t *testing.T) {
	a := semconv.ToolAttrs{
		Name: "noop",
	}
	attrs := semconv.ToolAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	absentWhenEmpty := []string{
		"gen_ai.tool.description",
		"gen_ai.tool.call.id",
		"traceloop.entity.input",
		"traceloop.entity.output",
	}
	for _, k := range absentWhenEmpty {
		if _, ok := found[k]; ok {
			t.Errorf("key %q should be absent when empty, but was present with value %v", k, found[k])
		}
	}
}

// TestToolAttributes_OperationNameIsExecuteTool asserts the span name discriminator.
func TestToolAttributes_OperationNameIsExecuteTool(t *testing.T) {
	attrs := semconv.ToolAttributes(semconv.ToolAttrs{Name: "any"})
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.operation.name" {
			if kv.Value.AsString() != "execute_tool" {
				t.Errorf("gen_ai.operation.name = %q, want 'execute_tool'", kv.Value.AsString())
			}
			return
		}
	}
	t.Error("gen_ai.operation.name not found in attributes")
}
