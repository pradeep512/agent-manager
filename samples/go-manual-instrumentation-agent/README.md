# Go Manual Instrumentation Sample Agent

A runnable Anthropic-based RAG agent written in Go that instruments itself by hand.
Instead of relying on auto-instrumentation, it emits OpenTelemetry GenAI spans
directly against WSO2 Agent Manager's **manual instrumentation contract** using the
`amp` Go SDK (`libs/amp-instrumentation-go`).

This is the Go counterpart of `samples/manual-instrumentation-agent/` (the Python
reference). It demonstrates:

- All **seven AMP span kinds** in one request.
- **Anthropic prompt caching** — the first LLM call writes to the cache
  (`cache_creation_input_tokens > 0`); the second reads from it
  (`cache_read_input_tokens > 0`). Both values map into `amp.LLMUsage` and roll up
  to the root agent span automatically.
- **`amp.WithEvaluation`** for optional evaluation correlation.
- Simulated embedding and rerank steps that make the trace deterministic and the
  sample runnable with only an Anthropic key (or even with no keys at all in
  dry-run mode).

## Trace shape

One agent turn produces one trace with this span tree:

```
invoke_agent    (agent, root)
└── rag-pipeline    (chain)
    ├── embeddings      (embedding)   simulated — Voyage AI model, canned vectors
    ├── vector_search   (retriever)   in-memory cosine top-k over knowledge base
    ├── rerank          (rerank)      simulated — keyword-overlap reorder
    ├── chat            (llm)         Anthropic: tool-decision call, cache write
    ├── execute_tool    (tool)        real local word_count function
    └── chat            (llm)         Anthropic: final answer, cache read
```

### What is real vs simulated

| Step | Real or simulated | Reason |
|---|---|---|
| Embedding | **Simulated** | Anthropic has no embeddings API. The span attributes are identical to what a real Voyage AI call would emit; the vector is a canned bag-of-words approximation. |
| Vector search | **Real logic** | In-memory cosine top-k over a static knowledge base — no external vector DB call but a real distance computation. |
| Rerank | **Simulated** | Keyword-overlap sort. Span attributes are contract-conformant. |
| LLM call 1 (tool decision) | **Real** (live) / **Simulated** (dry-run) | In live mode a real Anthropic API call with `cache_control` on the system prompt. In dry-run mode a hard-coded tool call with canned cache-creation tokens. |
| Tool execution | **Real** | A real local `word_count` function call. |
| LLM call 2 (final answer) | **Real** (live) / **Simulated** (dry-run) | In live mode a real Anthropic call; the same system-prompt prefix is now in the cache so `cache_read_input_tokens > 0`. In dry-run mode canned tokens demonstrate the cache-hit path. |

## Prompt caching

Both Anthropic LLM calls include the same large system prompt as an array system
block with `cache_control: {type: "ephemeral"}`. Anthropic caches the prompt prefix
server-side:

- **Call 1 (tool decision)**: cache miss → `cache_creation_input_tokens > 0`,
  `cache_read_input_tokens = 0`. The system prompt is stored in the ephemeral cache.
- **Call 2 (final answer)**: cache hit → `cache_creation_input_tokens = 0`,
  `cache_read_input_tokens > 0`. The cached prefix is reused, saving cost and latency.

The `amp` SDK maps these fields into `amp.LLMUsage.CacheCreationInputTokens` and
`amp.LLMUsage.CacheReadInputTokens`. They appear on each LLM leaf span as:

- `gen_ai.usage.cache_creation_input_tokens`
- `gen_ai.usage.cache_read_input_tokens`

The SDK accumulator rolls both values up to the root `invoke_agent` span, so the
agent span shows the trace-level cache totals.

## Prerequisites

- Go 1.22 or later.
- An **Anthropic API key** for live mode (real LLM calls).
- An **AMP agent registered** in the AMP Console for trace export. This gives you
  `AMP_OTEL_ENDPOINT` and `AMP_AGENT_API_KEY`.

Dry-run mode (no keys needed) is described under [Running offline](#running-offline).

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `AMP_OTEL_ENDPOINT` | Yes (live) | AMP OTLP gateway base URL. Traces are exported to `$AMP_OTEL_ENDPOINT/v1/traces`. |
| `AMP_AGENT_API_KEY` | Yes (live) | API key sent as the `x-amp-api-key` header. Generated in the AMP Console. |
| `ANTHROPIC_API_KEY` | Yes (live) | Anthropic API key for real LLM calls. Absent → dry-run mode. |
| `AMP_TRACE_CONTENT` | No | `"false"` redacts prompt/response text in spans (default `"true"`). |
| `AMP_COMPONENT_UID` | No | `openchoreo.dev/component-uid` resource attribute. Set when running as an externally-hosted agent. |
| `AMP_DEBUG` | No | `"true"` enables verbose SDK logging. |
| `DRY_RUN` | No | `"true"` forces dry-run mode even when `ANTHROPIC_API_KEY` is set. |
| `AMP_TASK_ID` | No | Evaluation task ID for correlation via `amp.WithEvaluation`. |
| `AMP_TRIAL_ID` | No | Evaluation trial ID for correlation via `amp.WithEvaluation`. |

## Running live (real Anthropic calls + trace export)

Register the agent in the AMP Console and generate its API key. Then:

```bash
cd samples/go-manual-instrumentation-agent

export AMP_OTEL_ENDPOINT="<your-amp-otel-endpoint>"
export AMP_AGENT_API_KEY="<key-from-the-amp-console>"
export ANTHROPIC_API_KEY="<your-anthropic-key>"

go run .
```

The agent asks one hard-coded question, emits the full 8-span trace, and exits.
The trace appears in the AMP Console under the registered agent.

To ask a different question, edit the `question` variable in `main()` in `agent.go`.

## Running offline (dry-run mode)

No API keys needed. Dry-run mode is automatically enabled when `ANTHROPIC_API_KEY`
is unset, or when `DRY_RUN=true`:

```bash
cd samples/go-manual-instrumentation-agent

DRY_RUN=true go run .
```

This exercises the full 7-span path — all span kinds, all attributes, the cache
token roll-up — using canned LLM responses and canned token counts. No network
calls are made. If `AMP_OTEL_ENDPOINT` is also set, the spans are still exported
to AMP; without it, export is skipped and the spans are discarded.

## Running the tests

The test suite verifies the full 7-span pipeline in dry-run mode using an
in-memory OTel exporter. No external services needed:

```bash
cd samples/go-manual-instrumentation-agent
go test ./... -v
```

Tests verify:

- All eight expected spans are recorded (1 agent + 1 chain + 1 embedding +
  1 retriever + 1 rerank + 1 tool + 2 LLM).
- Each span has the discriminator attributes its span kind requires.
- The agent span shows rolled-up `cache_creation` and `cache_read` totals.
- Evaluation correlation (`task.id` / `trial.id`) appears on the agent span.
- The `word_count` tool produces correct results.
- Cosine similarity and keyword ranking produce meaningful results.

## Observe the traces

In the AMP Console, open the agent and go to **OBSERVABILITY → Traces**. Each
`go run .` invocation produces one trace with the full span tree above. Spans carry:

- Per-kind icons (agent, chain, embedding, retriever, rerank, tool, llm).
- Model and token chips including cache read/creation breakdowns.
- Prompt and response text (or `[redacted]` when `AMP_TRACE_CONTENT=false`).
- Error badges on failed runs (if any span calls `span.Error(err)`).

## Multi-service propagation

When your agent calls downstream services (e.g. a vector DB with its own traces),
use the standard `otelhttp` transport so trace context propagates automatically:

```go
import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

// Wrap the HTTP client transport.
httpClient := &http.Client{
    Transport: otelhttp.NewTransport(http.DefaultTransport),
}

// On the server side, wrap the handler.
http.Handle("/", otelhttp.NewHandler(myHandler, "myService"))
```

`amp.Init` installs the W3C TraceContext propagator globally, so any `otelhttp`
instrumented client/server pair connects its spans into the same trace without any
additional configuration.

## File guide

| File | Role |
|---|---|
| `agent.go` | The hand-written RAG agent: Anthropic client, 7-span pipeline, `main`. |
| `agent_test.go` | Offline tests: full 7-span verification, unit tests for helper functions. |
| `go.mod` | Module declaration with a `replace` directive to the local amp SDK. |
| `README.md` | This file. |

## Attribute coverage

Every span emitted follows the AMP instrumentation contract. The table below maps
each span kind to the Go SDK helper and key attributes:

| Span kind | amp helper | Key attributes |
|---|---|---|
| `agent` | `amp.AgentSpan` | `gen_ai.operation.name=invoke_agent`, `gen_ai.agent.name`, `gen_ai.system`, `gen_ai.request.model`, `gen_ai.system_instructions`, `gen_ai.conversation.id`, `gen_ai.agent.tools`, `gen_ai.input/output.messages`, `gen_ai.usage.*` (incl. cache) |
| `chain` | `amp.ChainSpan` | `traceloop.span.kind=workflow`, `traceloop.entity.input/output` |
| `embedding` | `amp.EmbeddingSpan` | `gen_ai.operation.name=embeddings`, `gen_ai.system`, `gen_ai.request/response.model`, `gen_ai.prompt.{i}.content`, `gen_ai.usage.input_tokens` |
| `retriever` | `amp.RetrieverSpan` | `db.system.name`, `db.collection.name`, `db.vector.query.top_k` |
| `rerank` | `amp.RerankSpan` | `traceloop.span.kind=rerank`, `gen_ai.operation.name=rerank`, `rerank.model`, `gen_ai.request.model`, `traceloop.entity.input` |
| `tool` | `amp.ToolSpan` | `gen_ai.operation.name=execute_tool`, `gen_ai.tool.name`, `gen_ai.tool.call.id`, `traceloop.entity.input/output` |
| `llm` | `amp.LLMSpan` | `gen_ai.operation.name=chat`, `gen_ai.system`, `gen_ai.request/response.model`, `gen_ai.request.temperature`, `gen_ai.input/output.messages`, `gen_ai.usage.*` (incl. `cache_creation` and `cache_read`) |

The authoritative attribute reference is the
[AMP instrumentation contract](https://wso2.github.io/agent-manager/docs/latest/components/amp-instrumentation/#the-contract).
