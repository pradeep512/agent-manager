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

// Package amp_test contains golden tests that assert RetrieverSpan emits
// exactly the attribute keys declared in the published contract
// (traces-observer-service/cmd/gen-contract/contract.go) for the "retriever"
// kind.
package amp_test

import (
	"context"
	"errors"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestRetrieverSpan_GoldenContractKeys asserts that RetrieverSpan emits the
// required key declared in the published contract for the "retriever" kind:
//
//	db.system.name  (required — observer discriminator)
//
// And optional keys when data is provided:
//
//	db.collection.name
//	db.vector.query.top_k
func TestRetrieverSpan_GoldenContractKeys(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB:   "pinecone",
		Collection: "product-embeddings",
		TopK:       5,
	})
	span.End()
	_ = ctx

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required key per contract — this is what the observer's
	// hasRetrieverAttributes (process.go) keys off to resolve the span to
	// kind = retriever.
	if _, ok := attrs["db.system.name"]; !ok {
		t.Error("required contract key 'db.system.name' missing from retriever span")
	}
	if attrs["db.system.name"] != "pinecone" {
		t.Errorf("db.system.name = %q, want 'pinecone'", attrs["db.system.name"])
	}

	// Optional keys present when data supplied.
	if attrs["db.collection.name"] != "product-embeddings" {
		t.Errorf("db.collection.name = %q, want 'product-embeddings'", attrs["db.collection.name"])
	}
	if attrs["db.vector.query.top_k"] != int64(5) {
		t.Errorf("db.vector.query.top_k = %v, want 5", attrs["db.vector.query.top_k"])
	}

	// Span name must be "vector_search" (matching Python reference).
	if s.Name() != "vector_search" {
		t.Errorf("span name = %q, want 'vector_search'", s.Name())
	}
}

// TestRetrieverSpan_GoldenContractKeys_RequiredOnly asserts that required key
// is present and optional ones are absent when no optional data is supplied.
func TestRetrieverSpan_GoldenContractKeys_RequiredOnly(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB: "weaviate",
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Required key must be present.
	if _, ok := attrs["db.system.name"]; !ok {
		t.Error("required key 'db.system.name' missing")
	}

	// Optional keys must NOT be present when not set.
	for _, k := range []string{
		"db.collection.name",
		"db.vector.query.top_k",
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("optional key %q should be absent when not provided, but was set to %v", k, attrs[k])
		}
	}
}

// TestRetrieverSpan_ResolvesToRetrieverKind asserts that the span's
// db.system.name value is a known vector-DB system that the observer's
// hasRetrieverAttributes check (process.go) would recognise as
// kind = retriever.
//
// The observer's vectorDBSystems list includes:
// "pinecone", "weaviate", "qdrant", "milvus", "chroma", "chromadb", "pgvector".
func TestRetrieverSpan_ResolvesToRetrieverKind(t *testing.T) {
	// These are the values the observer recognises as vector-DB systems
	// (opensearch/process.go → vectorDBSystems).
	knownVectorDBSystems := map[string]bool{
		"pinecone":  true,
		"weaviate":  true,
		"qdrant":    true,
		"milvus":    true,
		"chroma":    true,
		"chromadb":  true,
		"pgvector":  true,
	}

	vectorDBs := []string{"pinecone", "weaviate", "qdrant"}
	for _, vdb := range vectorDBs {
		t.Run(vdb, func(t *testing.T) {
			rec := setupInMemoryProvider(t)

			ctx := context.Background()
			_, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
				VectorDB:   vdb,
				Collection: "test-collection",
				TopK:       3,
			})
			span.End()

			s := lastSpan(t, rec)
			attrs := attrMap(s)

			dbSystem, ok := attrs["db.system.name"].(string)
			if !ok {
				t.Fatal("db.system.name missing or not a string")
			}

			// The observer's hasRetrieverAttributes checks slices.Contains(vectorDBSystems, dbSystem).
			// Assert that the emitted value would pass that check.
			if !knownVectorDBSystems[dbSystem] {
				t.Errorf("db.system.name = %q is not in the observer's vectorDBSystems list; "+
					"observer will not resolve this span to 'retriever' kind", dbSystem)
			}
		})
	}
}

// TestRetrieverSpan_SpanKindIsClient asserts that the retriever span uses
// SpanKindClient, matching the Python reference (SpanKind.CLIENT).
func TestRetrieverSpan_SpanKindIsClient(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB: "qdrant",
	})
	span.End()

	s := lastSpan(t, rec)
	if s.SpanKind().String() != "client" {
		t.Errorf("span kind = %q, want 'client'", s.SpanKind().String())
	}
}

// TestRetrieverSpan_NoContentRedaction asserts that RetrieverSpan carries no
// free-form text content (matching the Python retriever_span reference which
// sets no content) and therefore no content-sensitive keys are emitted.
func TestRetrieverSpan_NoContentRedaction(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB:   "milvus",
		Collection: "articles",
		TopK:       10,
	})
	span.End()

	s := lastSpan(t, rec)
	attrs := attrMap(s)

	// Confirm that no gen_ai.* content keys are emitted — the retriever span
	// uses db.* attributes only.
	contentKeys := []string{
		"gen_ai.input.messages",
		"gen_ai.output.messages",
		"gen_ai.system_instructions",
		"traceloop.entity.input",
		"traceloop.entity.output",
	}
	for _, k := range contentKeys {
		if _, ok := attrs[k]; ok {
			t.Errorf("content key %q should not appear on a retriever span, but was set to %v", k, attrs[k])
		}
	}

	// The three db.* contract keys should be the only span attributes.
	allowedKeys := map[string]bool{
		"db.system.name":        true,
		"db.collection.name":    true,
		"db.vector.query.top_k": true,
	}
	for k := range attrs {
		if !allowedKeys[k] {
			t.Errorf("unexpected attribute %q on retriever span; only db.* contract keys are expected", k)
		}
	}
}

// TestRetrieverSpan_Error asserts that span.Error marks the span with error
// status and sets the error.type attribute on a retriever span.
func TestRetrieverSpan_Error(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB: "chroma",
	})
	span.Error(errors.New("connection refused"))
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
