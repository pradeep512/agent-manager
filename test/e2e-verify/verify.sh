#!/usr/bin/env bash
# verify.sh — one-command e2e cache-token verification orchestrator (issue #19)
#
# Wires the two harness tools into a single verdict for the #14 observer change:
#
#   sample /chat ──▶ traceId ──▶ opensearch-probe (GATE) ──▶ reconciler (PROOF) ──▶ verdict
#
# Verdicts (and exit codes):
#   PASS         (0) — gate found cache_creation > 0 AND the observer total reconciles.
#   INCONCLUSIVE (2) — gate found no cache_creation (caching never fired, or the attribute
#                      was dropped in ingestion). The reconciler is NOT run. Not a FAIL.
#   FAIL         (1) — gate OK but the observer total/field is wrong (a real #14 regression),
#                      or a setup/usage error.
#
# It reuses cmd/opensearch-probe (#17) and cmd/reconciler (#18) as-is — no duplicated math.
#
# ----------------------------------------------------------------------------
# Modes
# ----------------------------------------------------------------------------
# Offline (deterministic self-test against committed dry-run fixtures):
#   ./verify.sh --offline
#
# Live (against a running Agent Manager + your host observer):
#   Required env:
#     OPENSEARCH_URL   e.g. https://localhost:9200
#     OS_INDEX         traces index name, e.g. otel-traces-2026-06-29
#     OS_USER          OpenSearch basic-auth user (default: admin)
#     OS_PASSWORD      OpenSearch basic-auth password
#     OBSERVER_URL     e.g. http://localhost:9098
#     BEARER           JWT for the observer /api/v1/* routes
#     AGENT            agent name registered in the Console/CLI
#     TRACE_START      RFC3339 window start (e.g. 2026-06-29T00:00:00Z)
#     TRACE_END        RFC3339 window end
#   Trace source (one of):
#     TRACE_ID         a traceId you already produced, OR
#     SAMPLE_CMD       a command that runs the sample and prints a line containing the
#                      traceId (the script extracts the first 32-hex-char token).
#
#   OPENSEARCH_URL=https://localhost:9200 OS_INDEX=otel-traces-2026-06-29 \
#   OS_PASSWORD=... OBSERVER_URL=http://localhost:9098 BEARER=... AGENT=e2e-cache-agent \
#   TRACE_START=2026-06-29T00:00:00Z TRACE_END=2026-06-29T23:59:59Z TRACE_ID=<id> \
#     ./verify.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

PROBE="go run ./cmd/opensearch-probe"
RECON="go run ./cmd/reconciler"

say() { printf '\n=== %s ===\n' "$*"; }

# ----------------------------------------------------------------------------
# Offline mode — fixtures
# ----------------------------------------------------------------------------
if [[ "${1:-}" == "--offline" ]]; then
    say "OFFLINE — gate (opensearch-probe) against fixture"
    $PROBE --file testdata/fixture_opensearch_search.json
    gate=$?
    if [[ $gate -eq 2 ]]; then
        say "VERDICT: INCONCLUSIVE — gate found no cache_creation; reconciler skipped"
        exit 2
    elif [[ $gate -ne 0 ]]; then
        say "VERDICT: FAIL — probe errored (exit $gate)"
        exit 1
    fi

    say "OFFLINE — proof (reconciler) against fixtures"
    $RECON \
        --traces-file testdata/fixture_observer_traces.json \
        --llm-span-files testdata/fixture_observer_span_llm1_cache_write.json,testdata/fixture_observer_span_llm2_cache_read.json
    proof=$?
    if [[ $proof -eq 0 ]]; then
        say "VERDICT: PASS"
        exit 0
    fi
    say "VERDICT: FAIL — reconciler did not reconcile (exit $proof)"
    exit 1
fi

# ----------------------------------------------------------------------------
# Live mode
# ----------------------------------------------------------------------------
: "${OPENSEARCH_URL:?set OPENSEARCH_URL (or use --offline)}"
: "${OS_INDEX:?set OS_INDEX}"
: "${OS_PASSWORD:?set OS_PASSWORD}"
: "${OBSERVER_URL:?set OBSERVER_URL}"
: "${BEARER:?set BEARER}"
: "${AGENT:?set AGENT}"
: "${TRACE_START:?set TRACE_START (RFC3339)}"
: "${TRACE_END:?set TRACE_END (RFC3339)}"
OS_USER="${OS_USER:-admin}"

# 1. Obtain a traceId — either provided, or by running the sample.
if [[ -z "${TRACE_ID:-}" ]]; then
    if [[ -z "${SAMPLE_CMD:-}" ]]; then
        echo "ERROR: set TRACE_ID, or SAMPLE_CMD to trigger the sample" >&2
        exit 1
    fi
    say "Triggering sample: $SAMPLE_CMD"
    sample_out="$(eval "$SAMPLE_CMD" 2>&1)"
    echo "$sample_out"
    TRACE_ID="$(printf '%s\n' "$sample_out" | grep -oiE '[0-9a-f]{32}' | head -1)"
    if [[ -z "$TRACE_ID" ]]; then
        echo "ERROR: could not extract a 32-hex traceId from the sample output" >&2
        exit 1
    fi
fi
echo "traceId = $TRACE_ID"

# 2. GATE — opensearch-probe
say "GATE — opensearch-probe (raw OpenSearch)"
$PROBE --url "$OPENSEARCH_URL" --index "$OS_INDEX" --trace-id "$TRACE_ID" \
       --user "$OS_USER" --password "$OS_PASSWORD"
gate=$?
if [[ $gate -eq 2 ]]; then
    say "VERDICT: INCONCLUSIVE — cache_creation absent/zero in OpenSearch; reconciler skipped"
    echo "  (caching never fired, or the attribute was dropped in ingestion — not a #14 FAIL)"
    exit 2
elif [[ $gate -ne 0 ]]; then
    say "VERDICT: FAIL — probe errored (exit $gate)"
    exit 1
fi

# 3. PROOF — reconciler
say "PROOF — reconciler (observer JSON)"
$RECON --observer-url "$OBSERVER_URL" --bearer "$BEARER" \
       --agent "$AGENT" --trace-id "$TRACE_ID" \
       --start "$TRACE_START" --end "$TRACE_END"
proof=$?
if [[ $proof -eq 0 ]]; then
    say "VERDICT: PASS — #14 verified end-to-end for trace $TRACE_ID"
    exit 0
fi
say "VERDICT: FAIL — observer did not reconcile cache totals (exit $proof)"
exit 1
