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

package semconv

import "go.opentelemetry.io/otel/attribute"

// ToolAttrs holds the inputs needed to build a tool-span attribute set for the
// "tool" span kind. Nil/zero optional fields are omitted from the output.
//
// The contract uses two layers:
//   - Layer 1 (gen_ai.*): gen_ai.operation.name, gen_ai.tool.name,
//     gen_ai.tool.description, gen_ai.tool.call.id
//   - Layer 2 (traceloop.*): traceloop.entity.input, traceloop.entity.output
//
// The observer's hasToolAttributes() keys off gen_ai.operation.name =
// "execute_tool" or gen_ai.tool.name. ExtractToolExecutionDetails reads
// traceloop.entity.input / traceloop.entity.output for the input/output panels.
type ToolAttrs struct {
	// Name is the tool / function name (gen_ai.tool.name). Required.
	Name string
	// Description is the tool's human-readable description (gen_ai.tool.description). Optional.
	Description string
	// CallID is the tool call identifier from the LLM response (gen_ai.tool.call.id). Optional.
	CallID string
	// Input is the JSON-encoded tool arguments (traceloop.entity.input). Optional.
	Input string
	// Output is the JSON-encoded tool result (traceloop.entity.output). Optional.
	Output string
}

// ToolAttributes returns the complete attribute set for a tool execution span.
// The span name must be "execute_tool" and gen_ai.operation.name is always
// "execute_tool".
//
// The returned slice follows the published contract order:
//  1. gen_ai.operation.name = "execute_tool"  (required — observer discriminator)
//  2. gen_ai.tool.name                         (required)
//  3. gen_ai.tool.description                  (optional)
//  4. gen_ai.tool.call.id                      (optional)
//  5. traceloop.entity.input                   (optional — Layer 2)
//  6. traceloop.entity.output                  (optional — Layer 2)
func ToolAttributes(a ToolAttrs) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "execute_tool"),
		attribute.String("gen_ai.tool.name", a.Name),
	}
	if a.Description != "" {
		attrs = append(attrs, attribute.String("gen_ai.tool.description", a.Description))
	}
	if a.CallID != "" {
		attrs = append(attrs, attribute.String("gen_ai.tool.call.id", a.CallID))
	}
	if a.Input != "" {
		attrs = append(attrs, attribute.String("traceloop.entity.input", a.Input))
	}
	if a.Output != "" {
		attrs = append(attrs, attribute.String("traceloop.entity.output", a.Output))
	}
	return attrs
}
