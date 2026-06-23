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

// TestRetrieverAttributes_RequiredKey asserts that RetrieverAttributes always
// emits db.system.name, which is the observer's discriminator for the
// retriever kind (opensearch/process.go → hasRetrieverAttributes).
func TestRetrieverAttributes_RequiredKey(t *testing.T) {
	a := semconv.RetrieverAttrs{
		VectorDB:   "pinecone",
		Collection: "docs",
		TopK:       5,
	}
	attrs := semconv.RetrieverAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	if _, ok := found["db.system.name"]; !ok {
		t.Error("required key 'db.system.name' missing")
	}
	if found["db.system.name"] != "pinecone" {
		t.Errorf("db.system.name = %v, want 'pinecone'", found["db.system.name"])
	}
}

// TestRetrieverAttributes_OptionalKeys asserts that optional keys are emitted
// when data is provided and absent when not.
func TestRetrieverAttributes_OptionalKeys(t *testing.T) {
	a := semconv.RetrieverAttrs{
		VectorDB:   "weaviate",
		Collection: "products",
		TopK:       10,
	}
	attrs := semconv.RetrieverAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	if found["db.collection.name"] != "products" {
		t.Errorf("db.collection.name = %v, want 'products'", found["db.collection.name"])
	}
	if found["db.vector.query.top_k"] != int64(10) {
		t.Errorf("db.vector.query.top_k = %v, want 10", found["db.vector.query.top_k"])
	}
}

// TestRetrieverAttributes_OptionalKeysAbsentWhenEmpty asserts that zero/empty
// optional fields are omitted from the output.
func TestRetrieverAttributes_OptionalKeysAbsentWhenEmpty(t *testing.T) {
	a := semconv.RetrieverAttrs{
		VectorDB: "qdrant",
		// Collection and TopK intentionally empty/zero.
	}
	attrs := semconv.RetrieverAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	absentWhenEmpty := []string{
		"db.collection.name",
		"db.vector.query.top_k",
	}
	for _, k := range absentWhenEmpty {
		if _, ok := found[k]; ok {
			t.Errorf("key %q should be absent when empty, but was present with value %v", k, found[k])
		}
	}
}
