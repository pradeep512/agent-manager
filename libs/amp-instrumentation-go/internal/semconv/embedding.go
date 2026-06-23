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

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
)

// EmbeddingAttrs holds the inputs needed to build an embedding-span attribute
// set for the "embedding" span kind. Nil/zero optional fields are omitted.
//
// The observer's hasEmbeddingAttributes (opensearch/process.go) keys off
// gen_ai.operation.name = "embeddings" (or "embedding") to resolve the span to
// the embedding kind — checked BEFORE llm because both share gen_ai.prompt.*.
type EmbeddingAttrs struct {
	// System is the AI provider / model vendor (gen_ai.system). Required.
	System string
	// RequestModel is the model requested by the caller (gen_ai.request.model). Required.
	RequestModel string
	// ResponseModel is the model that actually responded (gen_ai.response.model). Optional.
	ResponseModel string
	// Texts are the strings being embedded. Each is emitted as
	// gen_ai.prompt.{i}.content. Optional; empty slice omits all keys.
	Texts []string
	// InputTokens is gen_ai.usage.input_tokens. Required by observer.
	InputTokens int64
}

// EmbeddingAttributes returns the complete attribute set for an embedding span.
// The span name must be "embeddings" and gen_ai.operation.name is always
// "embeddings".
//
// The returned slice follows the published contract order:
//  1. gen_ai.operation.name = "embeddings"  (required — observer discriminator)
//  2. gen_ai.system                          (required)
//  3. gen_ai.request.model                   (required)
//  4. gen_ai.response.model                  (optional)
//  5. gen_ai.prompt.{i}.content              (optional — one per input text)
//  6. gen_ai.usage.input_tokens              (required)
func EmbeddingAttributes(a EmbeddingAttrs) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "embeddings"),
		attribute.String("gen_ai.system", a.System),
		attribute.String("gen_ai.request.model", a.RequestModel),
	}
	if a.ResponseModel != "" {
		attrs = append(attrs, attribute.String("gen_ai.response.model", a.ResponseModel))
	}
	for i, text := range a.Texts {
		attrs = append(attrs, attribute.String(fmt.Sprintf("gen_ai.prompt.%d.content", i), text))
	}
	attrs = append(attrs, attribute.Int64("gen_ai.usage.input_tokens", a.InputTokens))
	return attrs
}
