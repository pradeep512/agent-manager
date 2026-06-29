# e2e-verify — cache-token verification harness (#14)

Thin glue that verifies the #14 observer change (reads `cache_creation_input_tokens`
and totals cache read + creation) end-to-end. It does NOT re-test the observer math —
that is covered by `traces-observer-service/opensearch/process_test.go` and
`test/go-conformance`. These commands assert the *live wire*: that a cache-creation
attribute survives ingestion and that the observer surfaces + totals it.

Own Go module (like `test/go-conformance`), so it never couples to other components.

## Commands

### `opensearch-probe` — the anti-false-green gate (issue #17)
Reads an OpenSearch `_search` response and reports whether a span carries
`gen_ai.usage.cache_creation_input_tokens > 0`.

- `GATE OK` (exit 0) — proceed to the reconciler.
- `INCONCLUSIVE` (exit 2) — attribute absent/zero in all spans. NOT a fail: it means
  caching never fired (e.g. prompt under Anthropic's cache minimum) or the attribute was
  dropped in ingestion. Distinct exit code so an orchestrator can tell it apart from FAIL.

```bash
# offline (fixture)
go run ./cmd/opensearch-probe --file testdata/fixture_opensearch_search.json
# live
go run ./cmd/opensearch-probe --url https://localhost:9200 --index <traces-index> \
    --trace-id <traceId> --user admin --password <pw>
```

### `reconciler` — the authoritative proof (issue #18)
Reads the observer's `/api/v1/traces` + per-LLM-span detail and asserts (a) an LLM span
carries `cacheCreationInputTokens`, and (b) the trace `totalTokens` equals
`input + output + cacheRead + cacheCreation` (recomputed). Per-span `totalTokens` stays
input+output by design. Missing `cacheRead` is informational. Verdict: `PASS` / `FAIL`.

```bash
# offline (fixtures)
go run ./cmd/reconciler \
  --traces-file testdata/fixture_observer_traces.json \
  --llm-span-files testdata/fixture_observer_span_llm1_cache_write.json,testdata/fixture_observer_span_llm2_cache_read.json
# live
go run ./cmd/reconciler --observer-url http://localhost:9098 --bearer <jwt> \
  --org default --project default --agent <agent> --env default \
  --trace-id <traceId> --start <RFC3339> --end <RFC3339>
```

## Fixtures
`testdata/` holds the deterministic **dry-run** fixtures captured during issue #16
(canned but real-shaped). Live fixtures (`fixture_live_*`) are intentionally kept out of
git — they live only in the git-ignored `local-docs/e2e-verify/`.

## Expected results against the committed fixtures
- probe → `GATE OK`, `cache_creation_input_tokens = 1800`.
- reconciler → `PASS`, total `704 + 123 + 1800 + 1800 = 4427`.
