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

// Package main is the Anthropic-based RAG sample agent for WSO2 Agent Manager.
//
// It demonstrates manual instrumentation using the amp Go SDK. One /chat request
// produces a single trace covering all seven AMP span kinds:
//
//	invoke_agent (agent, root)
//	└── rag-pipeline (chain)
//	    ├── embeddings    (embedding)   simulated (Anthropic has no embeddings API)
//	    ├── vector_search (retriever)   in-memory cosine top-k
//	    ├── rerank        (rerank)      simulated
//	    ├── chat          (llm)         Anthropic: decides to call a tool
//	    ├── execute_tool  (tool)        real local function call
//	    └── chat          (llm)         Anthropic: final answer, shows cache tokens
//
// Prompt caching is enabled via a cache_control breakpoint on the system prompt.
// The first Anthropic call writes to the cache (cache_creation_input_tokens > 0);
// the second call — which includes the same system prompt — reads from it
// (cache_read_input_tokens > 0). Both usage values are mapped into amp.LLMUsage
// and show up on the respective LLM leaf spans, and roll up to the root agent span.
//
// Run modes:
//   - Default (DRY_RUN=true or ANTHROPIC_API_KEY unset): full 7-span trace with
//     canned/simulated LLM responses and token counts. No external calls needed.
//   - Live (ANTHROPIC_API_KEY set and DRY_RUN != "true"): real Anthropic API calls;
//     requires AMP_OTEL_ENDPOINT and AMP_AGENT_API_KEY to export traces.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	chatModelDefault    = "claude-sonnet-4-6" // current Sonnet; override via ANTHROPIC_MODEL env
	simulatedEmbedModel = "voyage-3-lite"     // simulated; Anthropic has no embeddings API
	rerankModel         = "rerank-1"          // simulated rerank
	topK                = 3
	maxTokens           = 1024
)

// activeChatModel returns the Anthropic chat model to use, preferring the
// ANTHROPIC_MODEL environment variable over the compiled-in default.
func activeChatModel() string {
	if m := os.Getenv("ANTHROPIC_MODEL"); m != "" {
		return m
	}
	return chatModelDefault
}

// systemPrompt is deliberately padded so Anthropic's prompt caching fires in
// live mode. The cache_control breakpoint is placed on this block.
// claude-sonnet-4-6 requires a 2048-token minimum per cacheable block; this
// prompt is sized to comfortably exceed that threshold (~2600+ tokens).
const systemPrompt = `You are a knowledgeable assistant that answers questions about the WSO2 Agent Manager (AMP) platform.

You have been provided with relevant context documents retrieved from a knowledge base. Use ONLY the provided context to answer the user's question. If the context does not contain enough information to answer the question, say so clearly and do not make up information.

AMP PLATFORM OVERVIEW:
WSO2 Agent Manager is an enterprise-grade platform for running, governing, observing, and evaluating AI agents at scale. It provides a unified control plane for managing the complete lifecycle of AI agents across development, staging, and production environments.

KEY CAPABILITIES:
1. Agent Lifecycle Management: Deploy, version, monitor, and retire AI agents through a unified control plane. Supports both platform-hosted (Kubernetes-native) and externally-hosted (self-managed) agents with equal observability.

2. Observability & Tracing: Captures every agent interaction — LLM calls, tool invocations, retrievals, reranking steps — as structured OpenTelemetry (OTel) traces conforming to the GenAI semantic conventions. Traces are stored in OpenSearch for long-term analysis and rendered in the AMP Console with rich visual context.

3. Instrumentation Paths:
   - Auto-instrumentation: Zero-code, platform-injected for Python agents using Traceloop SDK monkey-patching (sitecustomize.py). Works for LangChain, LlamaIndex, CrewAI, and other supported frameworks.
   - Manual instrumentation: Language-agnostic. The agent emits its own OTel spans against AMP's published contract. Required for Go agents, Ballerina agents, and Python agents on unsupported frameworks.

4. Evaluation Engine: Runs LLM-as-judge and deterministic evaluators over agent traces. Evaluators correlate to specific agent runs via W3C baggage task_id/trial_id headers, enabling offline evaluation without re-running the agent.

5. Multi-Tenancy & RBAC: Full role-based access control at the organization, project, and agent level. Audit logs capture all administrative actions.

INSTRUMENTATION CONTRACT:
The AMP instrumentation contract is layered:
- Layer 1: OTel GenAI semantic conventions (gen_ai.* keys, plus db.* for retriever spans)
- Layer 2: OpenLLMetry/Traceloop extension keys (traceloop.*) for chain/workflow, rerank, and tool I/O — gaps OTel has not yet standardized

TOKEN TRACKING:
AMP tracks prompt caching token economics separately:
- gen_ai.usage.input_tokens: raw uncached input tokens
- gen_ai.usage.output_tokens: generated tokens
- gen_ai.usage.cache_read_input_tokens: tokens read from Anthropic prompt cache (cheaper)
- gen_ai.usage.cache_creation_input_tokens: tokens written to prompt cache (upfront cost)

DEPLOYMENT:
Platform-hosted agents run inside the AMP Kubernetes cluster via Choreo's workload runtime. External agents export traces via OTLP/HTTP to the AMP gateway endpoint with an x-amp-api-key header. The env-injection trait auto-supplies AMP_OTEL_ENDPOINT and AMP_AGENT_API_KEY to platform-hosted agents.

SECURITY:
API keys are rotated via AMP Console key management. The Go SDK reads AMP_AGENT_API_KEY once at Init() (rotation requires restart in v1; designed for v2 file-watcher refresh without an API break).

EXTENDED ARCHITECTURE REFERENCE:

DEPLOYMENT ARCHITECTURES:
AMP supports two primary deployment patterns for agent connectivity.

Pattern A — Platform-Hosted (Internal) Agents:
The agent workload runs as a Kubernetes Deployment inside the AMP cluster, managed by Choreo's workload runtime. The env-injection trait automatically injects three environment variables into every container: AMP_OTEL_ENDPOINT (the cluster-internal OTLP HTTP collector URL), AMP_AGENT_API_KEY (a per-agent API key for the x-amp-api-key header), and AMP_TRACE_CONTENT (optional flag controlling content redaction, default true). The agent calls amp.Init() with no explicit configuration; the SDK reads these variables automatically. No inbound firewall rules are needed because all traffic flows outbound from agent to collector within the cluster network.

Pattern B — Externally-Hosted (Remote) Agents:
The agent runs outside the AMP cluster (on the user's own cloud, on-prem, or on a developer laptop). The user supplies AMP_OTEL_ENDPOINT, AMP_AGENT_API_KEY, and optionally AMP_COMPONENT_UID as environment variables. The amp.Init() SDK call reads these and configures an OTLP/HTTP exporter that sends spans to the AMP gateway. All spans carry the component-uid resource attribute so the gateway routes them to the correct agent's trace store. External agents must ensure outbound HTTPS to the AMP_OTEL_ENDPOINT host is permitted by their network policy.

OPENTELEMETRY SPAN CONTRACT (per kind):
Every AMP span must carry a minimum set of attributes to be rendered correctly by the observer:

agent spans: gen_ai.system (provider name), gen_ai.request.model, gen_ai.usage.input_tokens, gen_ai.usage.output_tokens, traceloop.span.kind="agent", traceloop.entity.name (agent name). Also carries cache token fields and optional gen_ai.agent.description and gen_ai.agent.conversation.id.

chain spans: traceloop.span.kind="workflow", traceloop.entity.name, traceloop.entity.input (JSON-encoded), traceloop.entity.output (JSON-encoded). Chain spans are the primary grouping unit for multi-step pipelines.

embedding spans: gen_ai.system, gen_ai.request.model, gen_ai.response.model, gen_ai.usage.input_tokens, traceloop.span.kind="embedding". The embedding input texts are set as traceloop.entity.input.

retriever spans: db.system (vector store type, e.g. "chroma"), db.collection.name, traceloop.span.kind="retriever". Per-hit attributes include gen_ai.retrieval.source.id and gen_ai.retrieval.source.score.

rerank spans: traceloop.span.kind="reranking", gen_ai.request.model, traceloop.entity.input (query JSON), candidate count in traceloop.entity.name.

tool spans: traceloop.span.kind="tool", traceloop.entity.name (tool name), traceloop.entity.input (JSON-encoded arguments), traceloop.entity.output (JSON-encoded result). The gen_ai.tool.call.id links the tool span to the LLM call that requested it.

llm spans: gen_ai.system, gen_ai.request.model, gen_ai.response.model, gen_ai.usage.input_tokens, gen_ai.usage.output_tokens, gen_ai.usage.cache_creation_input_tokens, gen_ai.usage.cache_read_input_tokens, traceloop.span.kind="llm", traceloop.entity.input (JSON messages), traceloop.entity.output (JSON messages). Temperature is set as gen_ai.request.temperature when explicitly configured.

PROMPT CACHING MECHANICS (ANTHROPIC-SPECIFIC):
Anthropic's prompt caching feature lets you designate a prefix of the conversation context (typically the system prompt) as cacheable by attaching a cache_control block of type "ephemeral" to the last system block. Anthropic caches that prefix server-side for up to 5 minutes (ephemeral tier). On the first call, the prefix is written to the cache; usage.cache_creation_input_tokens reflects the tokens written. On subsequent calls within the 5-minute window that send the same prefix, usage.cache_read_input_tokens is nonzero and cache_creation_input_tokens is 0 — the prefix is served from the cache at a reduced input-token cost.

Minimum token thresholds (model-dependent):
- claude-sonnet-4-6 and claude-opus-4-8: 2048-token minimum cacheable prefix
- Legacy claude-sonnet-4-5-20250929: 1024-token minimum cacheable prefix

The AMP Go SDK maps all four Anthropic usage fields (input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens) onto amp.LLMUsage and rolls them up to the root agent span automatically. The observer sums all four into TotalTokens at the trace level.

SDK INITIALIZATION AND SHUTDOWN:
The amp.Init(ctx) function performs the following steps in order:
1. Reads AMP_OTEL_ENDPOINT and appends /v1/traces for the OTLP HTTP exporter endpoint.
2. Reads AMP_AGENT_API_KEY and configures the x-amp-api-key request header.
3. Reads AMP_COMPONENT_UID (optional; required only for external agents that need to specify component identity directly).
4. Reads AMP_TRACE_CONTENT (default "true"); if "false", all content fields (prompts, completions, tool arguments and results) are replaced with "[redacted]" before export.
5. Registers a BatchSpanProcessor backed by the OTLP HTTP exporter with a 5-second flush interval.
6. Sets the global W3C trace-context and baggage propagators for cross-service trace continuation.
The amp.Shutdown(ctx) call flushes all pending spans and closes the exporter.

MULTI-TENANCY AND RBAC:
AMP uses a three-level hierarchy: organization → project → agent. Each level has its own RBAC roles. Organization admins have full access to all projects and agents, user management, and billing. Project owners can manage agents within a project, configure evaluators, and view traces. Agent viewers have read-only access to traces and evaluation results for a specific agent. API keys are scoped to a single agent; traces carrying a different component-uid than the key's registered agent are rejected at the gateway.

EVALUATION FRAMEWORK:
AMP evaluators are scheduled or on-demand jobs that scan a time range of agent traces and score each interaction. LLM-as-judge evaluators send the span's input and output to a judge model with a rubric prompt and return a numeric score plus textual rationale. Deterministic evaluators run code-based scorers (regex match, JSON schema validation, embedding similarity) without additional LLM cost. Both evaluator types use W3C baggage task_id and trial_id to correlate evaluation runs with specific agent traces. The amp.WithEvaluation(ctx, taskID, trialID) helper injects these values into the outgoing baggage so the evaluator can locate the correct trace without re-running the agent.

CONTENT REDACTION:
When AMP_TRACE_CONTENT=false, the SDK replaces all user-visible text fields with "[redacted]" before exporting spans. Affected fields include: traceloop.entity.input, traceloop.entity.output, gen_ai.prompt, gen_ai.completion. Structural metadata (roles, token counts, model names, tool names, span kinds, and timestamps) is always retained regardless of the redaction setting.

Instructions:
- Answer concisely using only the provided context.
- Use the word_count tool when asked about text length or word counts.
- Be specific about which AMP features apply to the user's question.
- If a question is outside the scope of the provided context, say so.`

// ── Knowledge base ────────────────────────────────────────────────────────────

// knowledgeDoc is one document in the in-memory knowledge base.
type knowledgeDoc struct {
	ID    string
	Title string
	Text  string
}

// knowledgeBase is the static knowledge store.
var knowledgeBase = []knowledgeDoc{
	{
		ID:    "kb-1",
		Title: "What AMP Is",
		Text:  "WSO2 Agent Manager is a platform to run, govern, observe, and evaluate AI agents at scale.",
	},
	{
		ID:    "kb-2",
		Title: "Observability",
		Text:  "AMP captures every agent interaction (LLM calls, tool calls, retrievals) as OpenTelemetry traces stored for analysis.",
	},
	{
		ID:    "kb-3",
		Title: "Auto-instrumentation",
		Text:  "Platform-hosted Python agents are auto-instrumented by an injected init container; externally-hosted agents use the amp-instrument CLI.",
	},
	{
		ID:    "kb-4",
		Title: "Manual instrumentation",
		Text:  "Agents on a framework the Traceloop SDK does not cover emit their own OpenTelemetry GenAI spans against AMP's published contract.",
	},
	{
		ID:    "kb-5",
		Title: "Evaluation",
		Text:  "AMP runs evaluators over agent traces; LLM-as-judge evaluators need the span input and output to be populated.",
	},
	{
		ID:    "kb-6",
		Title: "Prompt Caching",
		Text:  "Anthropic prompt caching reduces cost and latency for agents that reuse large system prompts or context blocks. AMP captures cache_creation and cache_read token counts on each LLM span.",
	},
	{
		ID:    "kb-7",
		Title: "Go SDK",
		Text:  "The amp Go SDK provides helpers for all seven AMP span kinds: agent, chain, embedding, retriever, rerank, tool, and llm. It runs on any Go agent without framework constraints.",
	},
}

// Precomputed simulated embedding vectors for each KB document.
// These are canned unit vectors — one non-zero component per document at a
// unique index — so cosine similarity picks the most relevant documents in a
// deterministic way without any external embedding API call. The query vector
// is built the same way based on keyword matching.
var docVectors [][]float64

func init() {
	// Build one simple bag-of-words embedding per document (dimension = len(KB))
	// so cosine similarity produces meaningful rankings without an external call.
	dim := len(knowledgeBase)
	docVectors = make([][]float64, dim)
	for i := range knowledgeBase {
		v := make([]float64, dim)
		v[i] = 1.0
		docVectors[i] = v
	}
}

// ── Anthropic API wire types ──────────────────────────────────────────────────

type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type systemBlock struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type messageContent struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
}

type message struct {
	Role    string           `json:"role"`
	Content []messageContent `json:"content"`
}

type toolInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]toolPropDef `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type toolPropDef struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema toolInputSchema `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    []systemBlock `json:"system,omitempty"`
	Messages  []message     `json:"messages"`
	Tools     []tool        `json:"tools,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type anthropicContent struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ── Anthropic client ──────────────────────────────────────────────────────────

type anthropicClient struct {
	apiKey  string
	httpCli *http.Client
}

func newAnthropicClient(apiKey string) *anthropicClient {
	return &anthropicClient{
		apiKey:  apiKey,
		httpCli: &http.Client{Timeout: 120 * time.Second},
	}
}

// call sends one Anthropic Messages API request and returns the response.
func (c *anthropicClient) call(ctx context.Context, req anthropicRequest) (*anthropicResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: new request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := c.httpCli.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read body: %w", err)
	}

	var ar anthropicResponse
	if err := json.Unmarshal(respBytes, &ar); err != nil {
		return nil, fmt.Errorf("anthropic: parse response: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("anthropic error (%s): %s", ar.Error.Type, ar.Error.Message)
	}
	return &ar, nil
}

// ── Vector math ───────────────────────────────────────────────────────────────

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ── Tool implementation ───────────────────────────────────────────────────────

// wordCount counts words in text. This is the local tool the model can call.
func wordCount(text string) int {
	return len(strings.Fields(text))
}

// ── Query vector (simulated) ──────────────────────────────────────────────────

// queryVector builds a simulated query embedding by checking which KB documents
// have keyword overlap with the query. This is a bag-of-words approximation that
// produces meaningful cosine rankings without an external embedding API call.
func queryVector(query string) []float64 {
	qWords := tokenize(query)
	dim := len(knowledgeBase)
	v := make([]float64, dim)
	for i, doc := range knowledgeBase {
		dWords := tokenize(doc.Title + " " + doc.Text)
		overlap := 0
		for _, w := range qWords {
			for _, d := range dWords {
				if w == d {
					overlap++
					break
				}
			}
		}
		v[i] = float64(overlap)
	}
	return v
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var words []string
	var buf strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			buf.WriteRune(r)
		} else if buf.Len() > 0 {
			words = append(words, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		words = append(words, buf.String())
	}
	return words
}

// ── Agent ─────────────────────────────────────────────────────────────────────

// isDryRun returns true when the agent should simulate LLM calls instead of
// making real Anthropic API calls. Dry-run mode is enabled when ANTHROPIC_API_KEY
// is not set or when DRY_RUN=true is explicitly set.
func isDryRun() bool {
	if os.Getenv("DRY_RUN") == "true" {
		return true
	}
	return os.Getenv("ANTHROPIC_API_KEY") == ""
}

// runAgent executes one agent turn and emits a fully-instrumented trace covering
// all seven AMP span kinds. It mirrors the Python manual-instrumentation-agent
// but uses Anthropic instead of OpenAI, with prompt caching enabled.
//
// With a real ANTHROPIC_API_KEY:
//   - The first LLM call (tool-decision) writes to the cache (cache_creation > 0).
//   - The second LLM call (final answer) reads from the cache (cache_read > 0).
//
// In dry-run mode (no API key or DRY_RUN=true), both LLM calls are simulated with
// canned usage values that exercise the full amp.LLMUsage token path.
func runAgent(ctx context.Context, question, conversationID string, taskID, trialID string) (string, error) {
	dryRun := isDryRun()

	// Optional evaluation correlation: attaches task_id/trial_id to W3C baggage.
	if taskID != "" && trialID != "" {
		ctx = amp.WithEvaluation(ctx, taskID, trialID)
	}

	// Tool definitions exposed to the model.
	toolDefs := []map[string]any{
		{
			"name":        "word_count",
			"description": "Counts the words in a piece of text.",
		},
	}

	// Root agent span.
	ctx, agentSpan, agentResult := amp.AgentSpan(ctx, amp.AgentInput{
		Name:               "amp-rag-agent",
		Description:        "Answers questions about WSO2 Agent Manager from a knowledge base.",
		Framework:          "anthropic",
		RequestModel:       activeChatModel(),
		SystemInstructions: systemPrompt,
		ConversationID:     conversationID,
		InputMessages:      []map[string]any{{"role": "user", "content": question}},
		Tools:              toolDefs,
	})
	defer agentSpan.End()

	answer, err := ragPipeline(ctx, question, toolDefs, dryRun)
	if err != nil {
		agentSpan.Error(err)
		return "", err
	}

	agentResult.OutputMessages = []map[string]any{{"role": "assistant", "content": answer}}
	return answer, nil
}

// ragPipeline runs the full retrieval-augmented generation pipeline and emits
// spans for all inner steps. It is the Go equivalent of agent.py's _rag_pipeline.
func ragPipeline(ctx context.Context, question string, toolDefs []map[string]any, dryRun bool) (string, error) {
	// Chain span wraps the entire RAG pipeline.
	ctx, chainSpan, chainResult := amp.ChainSpan(ctx, amp.ChainInput{
		Name:  "rag-pipeline",
		Input: question,
	})
	defer chainSpan.End()

	// ── Step 1: Embedding ─────────────────────────────────────────────────────
	// Anthropic has no embeddings API. We simulate the embedding call with a
	// canned vector + canned usage. The span attributes are identical to what a
	// real embedding call would emit; only the implementation is simulated.
	ctx, embedSpan, embedResult := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "voyage",
		RequestModel: simulatedEmbedModel,
		Texts:        []string{question},
	})
	qVec := queryVector(question)
	embedResult.ResponseModel = simulatedEmbedModel
	embedResult.InputTokens = int64(len(strings.Fields(question)) + 4) // simulated token count
	embedSpan.End()

	// ── Step 2: Retriever (in-memory cosine top-k) ────────────────────────────
	ctx, retrieverSpan := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB:   "chroma",
		Collection: "amp-knowledge-base",
		TopK:       topK,
	})
	type scoredDoc struct {
		doc   knowledgeDoc
		score float64
	}
	scored := make([]scoredDoc, len(knowledgeBase))
	for i, doc := range knowledgeBase {
		scored[i] = scoredDoc{doc: doc, score: cosine(qVec, docVectors[i])}
	}
	// Simple sort: bubble sort for clarity (tiny slice).
	for i := 0; i < len(scored)-1; i++ {
		for j := 0; j < len(scored)-1-i; j++ {
			if scored[j].score < scored[j+1].score {
				scored[j], scored[j+1] = scored[j+1], scored[j]
			}
		}
	}
	hits := make([]knowledgeDoc, 0, topK)
	for i := 0; i < topK && i < len(scored); i++ {
		hits = append(hits, scored[i].doc)
	}
	retrieverSpan.End()

	// ── Step 3: Rerank (simulated) ────────────────────────────────────────────
	ctx, rerankSpan := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          rerankModel,
		Query:          question,
		CandidateCount: len(hits),
	})
	// Simulated rerank: sort by keyword overlap (deterministic).
	qWords := tokenize(question)
	for i := 0; i < len(hits)-1; i++ {
		for j := 0; j < len(hits)-1-i; j++ {
			scoreA := keywordOverlap(qWords, tokenize(hits[j].Text))
			scoreB := keywordOverlap(qWords, tokenize(hits[j+1].Text))
			if scoreA < scoreB {
				hits[j], hits[j+1] = hits[j+1], hits[j]
			}
		}
	}
	rerankSpan.End()

	// Build context text for the LLM.
	var contextLines []string
	for _, h := range hits {
		contextLines = append(contextLines, fmt.Sprintf("- %s: %s", h.Title, h.Text))
	}
	contextText := strings.Join(contextLines, "\n")

	baseMessages := []message{
		{
			Role: "user",
			Content: []messageContent{
				{
					Type: "text",
					Text: fmt.Sprintf("Context:\n%s\n\nQuestion: %s", contextText, question),
				},
			},
		},
	}

	// ── Step 4: LLM call 1 — tool decision ───────────────────────────────────
	// The model is given tools and the context. We expect it to call word_count
	// on the context. In dry-run mode this is simulated with a hard-coded tool
	// call so the trace is deterministic (matching the Python sample's approach).
	callID := fmt.Sprintf("toolu_%s", truncateStr(fmt.Sprintf("%x", time.Now().UnixNano()), 8))

	// Build amp LLM input messages (for the span attribute).
	llm1InputMsgs := []map[string]any{
		{
			"role":    "user",
			"content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", contextText, question),
		},
	}

	ctx, llm1Span, llm1Result := amp.LLMSpan(ctx, amp.LLMInput{
		System:         "anthropic",
		RequestModel:   activeChatModel(),
		InputMessages:  llm1InputMsgs,
		Temperature:    0.3,
		SetTemperature: true,
	})

	var toolCallInput map[string]any
	if dryRun {
		// Simulated: hard-code tool call + canned usage.
		// cache_creation_input_tokens > 0 because this is the first call with
		// the cached system prompt (the cache is being written).
		toolCallInput = map[string]any{"text": contextText}
		llm1Result.ResponseModel = activeChatModel()
		llm1Result.OutputMessages = []map[string]any{
			{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					{
						"id":   callID,
						"type": "function",
						"function": map[string]any{
							"name":      "word_count",
							"arguments": fmt.Sprintf(`{"text": %q}`, contextText),
						},
					},
				},
			},
		}
		llm1Result.Usage = amp.LLMUsage{
			InputTokens:              320,
			OutputTokens:             28,
			CacheCreationInputTokens: 1800, // system prompt being cached
			CacheReadInputTokens:     0,    // first call: no cache hit yet
		}
	} else {
		// Real Anthropic call with prompt caching enabled.
		client := newAnthropicClient(os.Getenv("ANTHROPIC_API_KEY"))
		resp, err := client.call(ctx, anthropicRequest{
			Model:     activeChatModel(),
			MaxTokens: maxTokens,
			System: []systemBlock{
				{
					Type: "text",
					Text: systemPrompt,
					// cache_control here enables Anthropic prompt caching on the system prompt.
					// The first call writes to the cache (cache_creation_input_tokens > 0).
					// Subsequent calls with the same prefix read from it (cache_read_input_tokens > 0).
					CacheControl: &cacheControl{Type: "ephemeral"},
				},
			},
			Messages: baseMessages,
			Tools: []tool{
				{
					Name:        "word_count",
					Description: "Counts the words in a piece of text.",
					InputSchema: toolInputSchema{
						Type: "object",
						Properties: map[string]toolPropDef{
							"text": {Type: "string", Description: "The text to count words in."},
						},
						Required: []string{"text"},
					},
				},
			},
		})
		if err != nil {
			llm1Span.Error(err)
			llm1Span.End()
			return "", fmt.Errorf("LLM call 1 failed: %w", err)
		}

		// Extract tool call from response.
		llm1Result.ResponseModel = resp.Model
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				callID = block.ID
				toolCallInput = block.Input
				llm1Result.OutputMessages = []map[string]any{
					{
						"role": "assistant",
						"tool_calls": []map[string]any{
							{"id": block.ID, "name": block.Name, "input": block.Input},
						},
					},
				}
				break
			}
			if block.Type == "text" && block.Text != "" {
				// Model answered directly without a tool call.
				llm1Result.OutputMessages = []map[string]any{
					{"role": "assistant", "content": block.Text},
				}
			}
		}

		// Map Anthropic usage → amp.LLMUsage (full cache fidelity).
		llm1Result.Usage = amp.LLMUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		}

		if toolCallInput == nil {
			toolCallInput = map[string]any{"text": contextText}
		}
	}
	llm1Span.End()

	// ── Step 5: Tool execution ────────────────────────────────────────────────
	// Real local function call: word_count on the retrieved context.
	toolInputText, _ := toolCallInput["text"].(string)
	if toolInputText == "" {
		toolInputText = contextText
	}

	ctx, toolSpan, toolResult := amp.ToolSpan(ctx, amp.ToolInput{
		Name:        "word_count",
		Description: "Counts the words in a piece of text.",
		CallID:      callID,
		Arguments:   map[string]any{"text": toolInputText},
	})
	count := wordCount(toolInputText)
	toolResult.Output = map[string]any{"word_count": count}
	toolSpan.End()

	// ── Step 6: LLM call 2 — final answer ────────────────────────────────────
	// Feed the tool result back to the model. Because the system prompt is the
	// same, this call reads from the prompt cache — cache_read_input_tokens > 0.
	toolResultContent, _ := json.Marshal(map[string]any{"word_count": count})

	// Build the full conversation: user → assistant (tool call) → tool result.
	llm2Messages := append(baseMessages, //nolint:gocritic // intentional append-to-copy
		message{
			Role: "assistant",
			Content: []messageContent{
				{
					Type:  "tool_use",
					ID:    callID,
					Name:  "word_count",
					Input: map[string]any{"text": toolInputText},
				},
			},
		},
		message{
			Role: "user",
			Content: []messageContent{
				{
					Type:      "tool_result",
					ToolUseID: callID,
					Content:   string(toolResultContent),
				},
			},
		},
	)

	llm2InputMsgs := []map[string]any{
		{
			"role":    "user",
			"content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", contextText, question),
		},
		{
			"role": "assistant",
			"tool_calls": []map[string]any{
				{"id": callID, "name": "word_count", "input": toolCallInput},
			},
		},
		{
			"role":    "tool",
			"tool_id": callID,
			"content": string(toolResultContent),
		},
	}

	_, llm2Span, llm2Result := amp.LLMSpan(ctx, amp.LLMInput{
		System:         "anthropic",
		RequestModel:   activeChatModel(),
		InputMessages:  llm2InputMsgs,
		Temperature:    0.3,
		SetTemperature: true,
	})
	defer llm2Span.End()

	var answer string
	if dryRun {
		// Simulated: canned answer + canned usage.
		// cache_read_input_tokens > 0 because this call hits the same system-prompt
		// prefix as the first call, which was cached above.
		answer = fmt.Sprintf(
			"Based on the retrieved context, %s The context contains %d words.",
			"WSO2 Agent Manager (AMP) is a platform for running, governing, observing, and evaluating AI agents at scale. "+
				"It supports both auto-instrumentation (for Python agents) and manual instrumentation (for Go, Ballerina, and custom frameworks). "+
				"AMP captures every agent interaction as OpenTelemetry traces with full token-usage breakdowns including prompt caching costs.",
			count,
		)
		llm2Result.ResponseModel = activeChatModel()
		llm2Result.OutputMessages = []map[string]any{
			{"role": "assistant", "content": answer},
		}
		llm2Result.Usage = amp.LLMUsage{
			InputTokens:              368,
			OutputTokens:             95,
			CacheCreationInputTokens: 0,    // no new cache writes on this call
			CacheReadInputTokens:     1800, // reading from the cache written by call 1
		}
	} else {
		// Real Anthropic call — the system prompt cache hit is expected here.
		client := newAnthropicClient(os.Getenv("ANTHROPIC_API_KEY"))
		resp, err := client.call(ctx, anthropicRequest{
			Model:     activeChatModel(),
			MaxTokens: maxTokens,
			System: []systemBlock{
				{
					Type:         "text",
					Text:         systemPrompt,
					CacheControl: &cacheControl{Type: "ephemeral"},
				},
			},
			Messages: llm2Messages,
		})
		if err != nil {
			llm2Span.Error(err)
			return "", fmt.Errorf("LLM call 2 failed: %w", err)
		}

		for _, block := range resp.Content {
			if block.Type == "text" {
				answer += block.Text
			}
		}
		if answer == "" {
			answer = "[no text response]"
		}

		llm2Result.ResponseModel = resp.Model
		llm2Result.OutputMessages = []map[string]any{
			{"role": "assistant", "content": answer},
		}

		// Map Anthropic usage → amp.LLMUsage.
		// On the second call with the same cached system prompt, Anthropic sets
		// cache_read_input_tokens > 0 and cache_creation_input_tokens = 0.
		// Both values surface on the LLM span and roll up to the root agent span.
		llm2Result.Usage = amp.LLMUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		}
	}

	chainResult.Output = answer
	return answer, nil
}

// keywordOverlap counts how many query words appear in doc words.
func keywordOverlap(qWords, dWords []string) int {
	set := make(map[string]struct{}, len(dWords))
	for _, w := range dWords {
		set[w] = struct{}{}
	}
	count := 0
	for _, w := range qWords {
		if _, ok := set[w]; ok {
			count++
		}
	}
	return count
}

// truncateStr truncates s to n characters.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Ensure we don't cut in the middle of a UTF-8 rune.
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	ctx := context.Background()

	// Init the amp SDK (reads AMP_OTEL_ENDPOINT + AMP_AGENT_API_KEY from env).
	// In dry-run mode the SDK still needs these for Init; if they are absent we
	// skip Init and run in pure simulation mode (no OTel export).
	dryRun := isDryRun()
	skipExport := dryRun && (os.Getenv("AMP_OTEL_ENDPOINT") == "" || os.Getenv("AMP_AGENT_API_KEY") == "")

	if !skipExport {
		if err := amp.Init(ctx); err != nil {
			log.Fatalf("amp.Init: %v", err)
		}
		defer func() {
			if err := amp.Shutdown(ctx); err != nil {
				log.Printf("amp.Shutdown: %v", err)
			}
		}()
	} else {
		log.Println("[dry-run] AMP export skipped (AMP_OTEL_ENDPOINT / AMP_AGENT_API_KEY not set)")
	}

	mode := "live (real Anthropic calls)"
	if dryRun {
		mode = "dry-run (simulated LLM responses)"
	}
	log.Printf("Starting amp-rag-agent in %s mode", mode)

	question := "How does AMP handle observability and what token tracking does it support?"
	conversationID := fmt.Sprintf("demo-%d", time.Now().Unix())

	// Optional: demonstrate evaluation correlation.
	taskID := os.Getenv("AMP_TASK_ID")
	trialID := os.Getenv("AMP_TRIAL_ID")

	log.Printf("Question: %s", question)
	answer, err := runAgent(ctx, question, conversationID, taskID, trialID)
	if err != nil {
		log.Fatalf("runAgent: %v", err)
	}
	log.Printf("Answer: %s", answer)
}
