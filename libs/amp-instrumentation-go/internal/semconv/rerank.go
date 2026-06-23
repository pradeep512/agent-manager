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

// RerankAttrs holds the inputs needed to build a rerank-span attribute set for
// the "rerank" span kind.
//
// Rerank is recognized as a kind only (no data card in v1). The observer keys
// it off the Layer-2 `traceloop.span.kind = rerank` attribute.
//
// The contract uses two layers:
//   - Layer 2 (traceloop.*): traceloop.span.kind = "rerank" (the discriminator),
//     traceloop.entity.input carrying the query and candidate count as JSON.
//   - De-facto signal keys from the Python reference (not standard OTel):
//     gen_ai.operation.name = "rerank", rerank.model, gen_ai.request.model.
//
// The observer's hasRerankAttributes (opensearch/process.go) additionally keys
// off gen_ai.operation.name = "rerank" and rerank.model as fallback signals.
type RerankAttrs struct {
	// Model is the reranker model identifier (rerank.model and gen_ai.request.model).
	// Required.
	Model string
	// Query is the text query for which candidates are reranked
	// (traceloop.entity.input → "query"). Redacted when AMP_TRACE_CONTENT=false.
	Query string
	// CandidateCount is the number of candidates provided to the reranker
	// (traceloop.entity.input → "candidate_count"). Optional; zero value omits it.
	CandidateCount int
	// EntityInput is the pre-serialised JSON for traceloop.entity.input. When
	// non-empty it is written verbatim (the facade constructs this after applying
	// redaction).
	EntityInput string
}

// RerankAttributes returns the complete attribute set for a reranking span.
// The span name must be "rerank" and SpanKind must be CLIENT.
//
// The returned slice follows the published contract + Python reference order:
//  1. traceloop.span.kind = "rerank"   (required — sole published contract key)
//  2. gen_ai.operation.name = "rerank" (de-facto signal; hasRerankAttributes)
//  3. rerank.model                     (de-facto signal; hasRerankAttributes)
//  4. gen_ai.request.model             (same value as rerank.model)
//  5. traceloop.entity.input           (JSON: {query, candidate_count})
func RerankAttributes(a RerankAttrs) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("traceloop.span.kind", "rerank"),
		attribute.String("gen_ai.operation.name", "rerank"),
		attribute.String("rerank.model", a.Model),
		attribute.String("gen_ai.request.model", a.Model),
	}
	if a.EntityInput != "" {
		attrs = append(attrs, attribute.String("traceloop.entity.input", a.EntityInput))
	}
	return attrs
}
