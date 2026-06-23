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

// RetrieverAttrs holds the inputs needed to build a retriever-span attribute
// set for the "retriever" span kind. Nil/zero optional fields are omitted.
//
// The contract uses OTel database semantic conventions (db.*), NOT gen_ai.*:
// the observer's hasRetrieverAttributes (opensearch/process.go) keys off
// db.system.name (or the legacy db.system) being a known vector-DB system
// name to resolve the span to the retriever kind.
//
// Retrieved documents are not extracted by the observer in v1, so they are
// not modelled here. There is no free-form text content in this span; no
// redaction is applied (matching the Python retriever_span reference).
type RetrieverAttrs struct {
	// VectorDB is the vector database system name (db.system.name). Required.
	// Must be a value recognised by the observer as a vector DB, e.g.
	// "pinecone", "weaviate", "qdrant", "milvus", "chroma", "chromadb",
	// "pgvector".
	VectorDB string
	// Collection is the collection / index name (db.collection.name). Optional.
	Collection string
	// TopK is the number of nearest neighbours requested
	// (db.vector.query.top_k). Optional; zero value omits the attribute.
	TopK int64
}

// RetrieverAttributes returns the complete attribute set for a vector-DB
// retrieval span. The span name must be "vector_search".
//
// The returned slice follows the published contract order:
//  1. db.system.name   (required — observer discriminator for retriever kind)
//  2. db.collection.name (optional)
//  3. db.vector.query.top_k (optional — omitted when zero)
func RetrieverAttributes(a RetrieverAttrs) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("db.system.name", a.VectorDB),
	}
	if a.Collection != "" {
		attrs = append(attrs, attribute.String("db.collection.name", a.Collection))
	}
	if a.TopK > 0 {
		attrs = append(attrs, attribute.Int64("db.vector.query.top_k", a.TopK))
	}
	return attrs
}
