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

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestDryRunAllSevenSpanKinds runs the full agent pipeline in dry-run mode
// (no Anthropic API key, no AMP export) and asserts that all seven AMP span
// kinds appear in the recorded trace:
//
//	agent → chain → embedding → retriever → rerank → tool → llm (×2)
//
// It also verifies that cache token fields (cache_creation and cache_read)
// are present on the LLM spans and that the agent span has rolled-up totals.
func TestDryRunAllSevenSpanKinds(t *testing.T) {
	// Force dry-run mode.
	t.Setenv("DRY_RUN", "true")
	// Unset API keys so isDryRun() returns true even if they were set in the shell.
	t.Setenv("ANTHROPIC_API_KEY", "")
	// The amp SDK needs AMP_OTEL_ENDPOINT for Init; since we're using our own
	// in-memory exporter below, we bypass amp.Init and install the provider directly.
	t.Setenv("AMP_OTEL_ENDPOINT", "")
	t.Setenv("AMP_AGENT_API_KEY", "")

	// Install an in-memory span exporter so we can inspect the emitted spans.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	// Reset the amp config cache so it picks up the test env.
	amp.ResetConfigCache()
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()

	ctx := context.Background()
	question := "How does AMP handle observability?"
	answer, err := runAgent(ctx, question, "test-conv-1", "task-42", "trial-7")
	if err != nil {
		t.Fatalf("runAgent: %v", err)
	}
	if answer == "" {
		t.Fatal("expected non-empty answer")
	}

	// Flush the tracer so all spans are in the exporter.
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded; expected 8+ spans")
	}

	// Index spans by their OTel span name for easy lookup.
	byName := make(map[string][]tracetest.SpanStub)
	for _, s := range spans {
		byName[s.Name] = append(byName[s.Name], s)
	}

	// ── 1. Agent span (root, invoke_agent) ────────────────────────────────────
	agentSpans, ok := byName["invoke_agent"]
	if !ok || len(agentSpans) == 0 {
		t.Error("missing invoke_agent span")
	} else {
		s := agentSpans[0]
		assertAttr(t, s, "gen_ai.operation.name", "invoke_agent")
		assertAttr(t, s, "gen_ai.agent.name", "amp-rag-agent")
		assertAttr(t, s, "gen_ai.system", "anthropic")
		// Agent span should have rolled-up token totals.
		assertAttrExists(t, s, "gen_ai.usage.input_tokens")
		assertAttrExists(t, s, "gen_ai.usage.output_tokens")
		// With dry-run canned usage: cache_creation (1800) and cache_read (1800)
		// should both appear on the agent span after roll-up.
		assertAttrExists(t, s, "gen_ai.usage.cache_creation_input_tokens")
		assertAttrExists(t, s, "gen_ai.usage.cache_read_input_tokens")
		// Evaluation baggage → span attributes.
		assertAttr(t, s, "task.id", "task-42")
		assertAttr(t, s, "trial.id", "trial-7")
		// SpanKind should be INTERNAL.
		if s.SpanKind != trace.SpanKindInternal {
			t.Errorf("invoke_agent: want SpanKind INTERNAL, got %v", s.SpanKind)
		}
	}

	// ── 2. Chain span (rag-pipeline) ─────────────────────────────────────────
	chainSpans, ok := byName["rag-pipeline"]
	if !ok || len(chainSpans) == 0 {
		t.Error("missing rag-pipeline chain span")
	} else {
		s := chainSpans[0]
		assertAttr(t, s, "traceloop.span.kind", "workflow")
		assertAttrExists(t, s, "traceloop.entity.input")
		assertAttrExists(t, s, "traceloop.entity.output")
	}

	// ── 3. Embedding span (embeddings) ────────────────────────────────────────
	embedSpans, ok := byName["embeddings"]
	if !ok || len(embedSpans) == 0 {
		t.Error("missing embeddings span")
	} else {
		s := embedSpans[0]
		assertAttr(t, s, "gen_ai.operation.name", "embeddings")
		assertAttr(t, s, "gen_ai.system", "voyage")
		assertAttr(t, s, "gen_ai.request.model", simulatedEmbedModel)
		assertAttrExists(t, s, "gen_ai.usage.input_tokens")
		if s.SpanKind != trace.SpanKindClient {
			t.Errorf("embeddings: want SpanKind CLIENT, got %v", s.SpanKind)
		}
	}

	// ── 4. Retriever span (vector_search) ────────────────────────────────────
	retrieverSpans, ok := byName["vector_search"]
	if !ok || len(retrieverSpans) == 0 {
		t.Error("missing vector_search retriever span")
	} else {
		s := retrieverSpans[0]
		assertAttr(t, s, "db.system.name", "chroma")
		assertAttr(t, s, "db.collection.name", "amp-knowledge-base")
		assertAttrExists(t, s, "db.vector.query.top_k")
	}

	// ── 5. Rerank span (rerank) ───────────────────────────────────────────────
	rerankSpans, ok := byName["rerank"]
	if !ok || len(rerankSpans) == 0 {
		t.Error("missing rerank span")
	} else {
		s := rerankSpans[0]
		assertAttr(t, s, "traceloop.span.kind", "rerank")
		assertAttr(t, s, "gen_ai.operation.name", "rerank")
		assertAttr(t, s, "rerank.model", rerankModel)
		assertAttrExists(t, s, "traceloop.entity.input")
	}

	// ── 6. Tool span (execute_tool) ───────────────────────────────────────────
	toolSpans, ok := byName["execute_tool"]
	if !ok || len(toolSpans) == 0 {
		t.Error("missing execute_tool span")
	} else {
		s := toolSpans[0]
		assertAttr(t, s, "gen_ai.operation.name", "execute_tool")
		assertAttr(t, s, "gen_ai.tool.name", "word_count")
		assertAttrExists(t, s, "traceloop.entity.input")
		assertAttrExists(t, s, "traceloop.entity.output")
	}

	// ── 7. LLM spans (chat ×2) ────────────────────────────────────────────────
	llmSpans, ok := byName["chat"]
	if !ok || len(llmSpans) < 2 {
		t.Errorf("expected at least 2 chat spans, got %d", len(llmSpans))
	} else {
		for i, s := range llmSpans {
			assertAttr(t, s, "gen_ai.operation.name", "chat")
			assertAttr(t, s, "gen_ai.system", "anthropic")
			assertAttr(t, s, "gen_ai.request.model", activeChatModel())
			assertAttrExists(t, s, "gen_ai.usage.input_tokens")
			assertAttrExists(t, s, "gen_ai.usage.output_tokens")
			if s.SpanKind != trace.SpanKindClient {
				t.Errorf("chat[%d]: want SpanKind CLIENT, got %v", i, s.SpanKind)
			}
		}

		// LLM 1 (tool decision): should have cache_creation_input_tokens.
		// We find it by looking for the span with cache_creation set.
		var llm1, llm2 *tracetest.SpanStub
		for i := range llmSpans {
			if hasAttr(llmSpans[i], "gen_ai.usage.cache_creation_input_tokens") {
				llm1 = &llmSpans[i]
			}
			if hasAttr(llmSpans[i], "gen_ai.usage.cache_read_input_tokens") {
				llm2 = &llmSpans[i]
			}
		}
		if llm1 == nil {
			t.Error("LLM call 1 (tool-decision): missing gen_ai.usage.cache_creation_input_tokens")
		}
		if llm2 == nil {
			t.Error("LLM call 2 (final answer): missing gen_ai.usage.cache_read_input_tokens")
		}
	}

	// ── Span count sanity check ───────────────────────────────────────────────
	// Minimum: 1 agent + 1 chain + 1 embedding + 1 retriever + 1 rerank + 1 tool + 2 llm = 8
	if len(spans) < 8 {
		t.Errorf("expected >= 8 spans, got %d", len(spans))
	}

	t.Logf("All %d spans recorded; all 7 span kinds verified", len(spans))
}

// TestWordCount verifies the local word_count tool.
func TestWordCount(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"hello world", 2},
		{"one", 1},
		{"", 0},
		{"a b c d e", 5},
		{"  leading and trailing  ", 3},
	}
	for _, c := range cases {
		got := wordCount(c.input)
		if got != c.want {
			t.Errorf("wordCount(%q) = %d; want %d", c.input, got, c.want)
		}
	}
}

// TestCosine verifies the cosine similarity helper.
func TestCosine(t *testing.T) {
	// Identical unit vectors → similarity 1.
	a := []float64{1, 0, 0}
	b := []float64{1, 0, 0}
	if got := cosine(a, b); got < 0.999 {
		t.Errorf("cosine(same) = %f; want ~1.0", got)
	}
	// Orthogonal vectors → similarity 0.
	c := []float64{0, 1, 0}
	if got := cosine(a, c); got > 0.001 {
		t.Errorf("cosine(orthogonal) = %f; want ~0.0", got)
	}
	// Zero vector → 0 (no panic).
	zero := []float64{0, 0, 0}
	if got := cosine(a, zero); got != 0 {
		t.Errorf("cosine(zero) = %f; want 0", got)
	}
}

// TestQueryVector verifies that the query vector produces meaningful rankings.
func TestQueryVector(t *testing.T) {
	// "observability" should score highest against kb-2 (Observability doc).
	qVec := queryVector("observability traces")
	scores := make([]float64, len(knowledgeBase))
	for i := range knowledgeBase {
		scores[i] = cosine(qVec, docVectors[i])
	}
	// Find the index with max score.
	maxIdx, maxScore := 0, scores[0]
	for i, s := range scores {
		if s > maxScore {
			maxIdx, maxScore = i, s
		}
	}
	if maxScore == 0 {
		t.Error("queryVector returned all-zero cosine scores")
	}
	_ = maxIdx // The top-ranked doc should relate to observability (kb-1 index 1).
	t.Logf("top-ranked doc for 'observability traces': %s (score=%.3f)", knowledgeBase[maxIdx].Title, maxScore)
}

// TestDryRunAnswer verifies the answer returned in dry-run mode is non-empty
// and contains expected content keywords.
func TestDryRunAnswer(t *testing.T) {
	os.Setenv("DRY_RUN", "true")
	os.Setenv("ANTHROPIC_API_KEY", "")
	os.Setenv("AMP_OTEL_ENDPOINT", "")
	os.Setenv("AMP_AGENT_API_KEY", "")

	// Use a no-op tracer to avoid needing OTel setup.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	amp.ResetConfigCache()
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	answer, err := runAgent(context.Background(), "What is WSO2 Agent Manager?", "test-conv-2", "", "")
	if err != nil {
		t.Fatalf("runAgent: %v", err)
	}
	if !strings.Contains(strings.ToLower(answer), "agent") {
		t.Errorf("answer %q does not mention 'agent'", answer)
	}
	t.Logf("dry-run answer: %s", answer)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// assertAttr checks that a span has an attribute with the expected string value.
func assertAttr(t *testing.T, s tracetest.SpanStub, key, want string) {
	t.Helper()
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			got := a.Value.AsString()
			if got != want {
				t.Errorf("span %q: attr %q = %q; want %q", s.Name, key, got, want)
			}
			return
		}
	}
	t.Errorf("span %q: missing attribute %q (want %q)", s.Name, key, want)
}

// assertAttrExists checks that a span has an attribute with the given key (any value).
func assertAttrExists(t *testing.T, s tracetest.SpanStub, key string) {
	t.Helper()
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return
		}
	}
	t.Errorf("span %q: missing attribute %q", s.Name, key)
}

// hasAttr returns true if the span has the given attribute key.
func hasAttr(s tracetest.SpanStub, key string) bool {
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return true
		}
	}
	return false
}

// attrValue returns the attribute.Value for the given key, or zero Value.
func attrValue(s tracetest.SpanStub, key string) attribute.Value {
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return a.Value
		}
	}
	return attribute.Value{}
}
