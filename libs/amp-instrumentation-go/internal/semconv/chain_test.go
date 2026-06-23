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

// TestChainAttributes_DiscriminatorKey asserts that ChainAttributes always
// emits traceloop.span.kind = "workflow", which the observer maps to SpanTypeChain
// (process.go: case "task", "workflow": return SpanTypeChain).
func TestChainAttributes_DiscriminatorKey(t *testing.T) {
	attrs := semconv.ChainAttributes("", "")
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	if found["traceloop.span.kind"] != "workflow" {
		t.Errorf("traceloop.span.kind = %q, want 'workflow'", found["traceloop.span.kind"])
	}
}

// TestChainAttributes_InputOutputPresent asserts that entity keys are emitted
// when non-empty JSON strings are provided.
func TestChainAttributes_InputOutputPresent(t *testing.T) {
	inputJSON := `{"input":{"query":"hello"}}`
	outputJSON := `{"output":{"answer":"world"}}`
	attrs := semconv.ChainAttributes(inputJSON, outputJSON)

	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	if found["traceloop.entity.input"] != inputJSON {
		t.Errorf("traceloop.entity.input = %q, want %q", found["traceloop.entity.input"], inputJSON)
	}
	if found["traceloop.entity.output"] != outputJSON {
		t.Errorf("traceloop.entity.output = %q, want %q", found["traceloop.entity.output"], outputJSON)
	}
}

// TestChainAttributes_InputOutputAbsentWhenEmpty asserts that entity keys are
// omitted when empty strings are passed (nil/zero input/output case).
func TestChainAttributes_InputOutputAbsentWhenEmpty(t *testing.T) {
	attrs := semconv.ChainAttributes("", "")
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	if _, ok := found["traceloop.entity.input"]; ok {
		t.Error("traceloop.entity.input should be absent when inputJSON is empty")
	}
	if _, ok := found["traceloop.entity.output"]; ok {
		t.Error("traceloop.entity.output should be absent when outputJSON is empty")
	}
}
