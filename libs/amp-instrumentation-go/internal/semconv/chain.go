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

// ChainAttrs holds the inputs needed to build a chain-span attribute set for
// the "chain" span kind.
//
// The chain / workflow kind has no OTel gen_ai.* discriminator key — the
// observer (traces-observer-service/opensearch/process.go) resolves the kind
// purely from the Layer-2 key:
//
//	traceloop.span.kind = "workflow"   →   SpanTypeChain
//
// Input and Output are wrapped in a JSON envelope before storage:
//
//	traceloop.entity.input  = {"input":  <workflow_input>}
//	traceloop.entity.output = {"output": <workflow_output>}
//
// This matches the Python reference (samples/manual-instrumentation-agent/
// instrumentation.py → chain_span).
type ChainAttrs struct {
	// Input is the workflow input payload. Optional; serialised to JSON as
	// {"input": <value>} and stored in traceloop.entity.input. Redacted when
	// AMP_TRACE_CONTENT=false.
	Input any
	// Output is the workflow output payload. Optional; serialised to JSON as
	// {"output": <value>} and stored in traceloop.entity.output. Redacted when
	// AMP_TRACE_CONTENT=false.
	Output any
}

// ChainAttributes returns the complete attribute set for a chain/workflow span.
//
// The returned slice follows the published contract order:
//  1. traceloop.span.kind = "workflow"  (required — observer discriminator → chain kind)
//  2. traceloop.entity.input            (optional — Layer 2; absent when Input is nil)
//  3. traceloop.entity.output           (optional — Layer 2; absent when Output is nil)
//
// The caller is responsible for JSON-serialising and redacting Input/Output
// before passing ChainAttrs; this function only appends non-empty string
// attributes. In practice the facade (amp.ChainSpan) handles serialisation and
// redaction so that callers only pass raw Go values.
func ChainAttributes(inputJSON, outputJSON string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("traceloop.span.kind", "workflow"),
	}
	if inputJSON != "" {
		attrs = append(attrs, attribute.String("traceloop.entity.input", inputJSON))
	}
	if outputJSON != "" {
		attrs = append(attrs, attribute.String("traceloop.entity.output", outputJSON))
	}
	return attrs
}
