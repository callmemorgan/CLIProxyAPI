# callmemorgan/CLIProxyAPI fork

This repository is a downstream fork of
[`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI).
It retains upstream's provider support and API compatibility while adding the
runtime behavior needed by
[`callmemorgan/all-models-patch`](https://github.com/callmemorgan/all-models-patch),
a patched Claude Code multi-model harness.

The fork is not a general rewrite. Its changes are intentionally limited to
model identity, model metadata, subscription quota reporting, and operational
diagnostics. Provider authentication, request translation, account selection,
and the rest of the proxy remain upstream architecture.

## Fork delta at a glance

| Area | Fork behavior | Primary files |
| --- | --- | --- |
| Claude-compatible model discovery | Advertise real provider model IDs instead of synthetic Claude-shaped aliases | `internal/util/claude_model.go`, `sdk/api/handlers/claude/code_handlers.go` |
| Claude response identity | Return the exact model ID requested by the client in streaming and non-streaming responses | `sdk/api/handlers/claude/code_handlers.go` |
| Legacy session compatibility | Continue decoding old `claude-fable-5-dd-<reversed-id>` values for routing | `internal/util/claude_model.go` |
| Subscription quotas | Expose sanitized Codex, Grok, Antigravity, and Kimi quota windows | `internal/api/handlers/management/subscription_usage.go` |
| Codex window labels | Classify 5-hour and weekly windows from their duration rather than primary/secondary order | `internal/api/handlers/management/subscription_usage.go` |
| Claude context metadata | Advertise 1M-token context windows for the Claude routes used by the local harness | `internal/registry/models/models.json` |
| Public list-price metadata | Project a curated, dated public-price snapshot into OpenAI/xAI model discovery and Claude Code bootstrap metadata | `internal/pricing/`, `sdk/api/handlers/openai/openai_handlers.go`, `internal/api/server.go` |
| Reasoning diagnostics | Log the resolved upstream reasoning effort and whether it was configured | `internal/runtime/executor/helps/usage_helpers.go` |
| xAI stream diagnostics | Log terminal stream state, translated completion markers, and token usage | `internal/runtime/executor/xai_executor.go` |

## Real model IDs through the Claude API

Upstream's Claude-compatible `/v1/models` handler rewrote non-Claude model IDs
into this compatibility form:

```text
claude-fable-5-dd-<reversed-real-model-id>
```

That made non-Anthropic models pass Claude Code's model filter, but the encoded
value leaked into settings and session metadata. A session using Gemini, GPT,
Grok, or Kimi could therefore appear to be running a fictional Claude model,
and resume could fail because stock Claude Code did not recognize the saved
identifier.

This fork instead:

1. Returns provider model IDs unchanged from the Claude-compatible
   `GET /v1/models` endpoint.
2. Routes requests using the exact requested model ID.
3. Rewrites both streaming and non-streaming response bodies to report that
   same client-visible ID, even when an upstream provider returns an internal
   model version.
4. Retains only the decoder for old synthetic IDs so existing settings and
   saved sessions continue to route during migration.

The corresponding Claude Code client filter is handled by
`all-models-patch`; the proxy no longer has to disguise every model as Claude.

## Subscription quota endpoint

The fork adds an authenticated endpoint alongside the other `/v1` routes:

```http
GET /v1/subscription-usage
GET /v1/subscription-usage?providers=codex,grok,antigravity,kimi
```

Supported provider selectors are:

- `codex`
- `grok`
- `antigravity` or `agy`
- `kimi`

The handler reuses credentials already loaded by CLIProxyAPI. It does not
return access tokens, account secrets, prompts, or request history. Its output
contains only normalized quota state:

```json
{
  "fetchedAt": "2026-07-14T00:00:00Z",
  "providers": {
    "codex": {
      "mode": "authoritative",
      "fetchedAt": "2026-07-14T00:00:00Z",
      "state": "available",
      "windows": [
        {
          "id": "5h",
          "label": "Codex 5h",
          "used": 42,
          "limit": 100,
          "usedPercent": 42,
          "resetAt": "2026-07-14T03:00:00Z"
        }
      ]
    }
  }
}
```

Provider reads run concurrently. When an authoritative quota request fails,
the response preserves observed availability information and includes a
sanitized provider error rather than failing the whole request.

### Codex window classification

Codex responses expose `primary` and `secondary` windows, but their order is
not a stable statement that primary means 5-hour and secondary means weekly.
The fork inspects `limit_window_seconds`:

- six hours or less is labeled `Codex 5h`
- six days or more is labeled `Codex weekly`
- intermediate or missing durations retain the upstream fallback label

The endpoint is consumed by `claude-all-usage`, which writes a sanitized cache
for the local status line.

## Public list-price metadata

The fork keeps a curated public list-price snapshot outside the remotely
refreshed model registry. Authenticated OpenAI-compatible model discovery adds
normalized price metadata, including xAI-compatible flat fields where the
public contract has an exact equivalent. Claude Code receives compatible text
and cache rates through authenticated `GET /api/claude_cli/bootstrap` under
`additional_model_costs`.

Prices are list-price equivalents, not observed subscription spend or billing
records. Exact aliases without published rates, subscription-only routes, and
models discovered after the snapshot are explicitly marked unavailable instead
of receiving inferred prices. The snapshot records its date and source URL per
model.

## Claude context metadata

The local model registry advertises a 1,000,000-token context length for these
Claude entries:

- Claude 4.5 Sonnet
- Claude 4.6 Sonnet
- Claude 4.5 Opus
- Claude 4.1 Opus
- Claude 4 Opus
- Claude 4 Sonnet
- Claude 3.7 Sonnet

The change affects discovery metadata only. The `all-models-patch` launcher
maintains its own route-validated context and compaction map and remains the
authority for Claude Code's client-side context behavior.

## Resolved reasoning-effort logs

After request translation, the shared usage reporter emits an
`upstream request: resolved settings` structured log entry containing:

- `provider`
- resolved `model`
- `requested_model`
- `upstream_format`
- `reasoning_effort`
- `reasoning_configured`
- `service_tier`, when present
- `request_id`, when present

This reports the value actually sent upstream, not merely the alias or suffix
the client requested. When no explicit effort is present, the log records
`reasoning_effort=default` and `reasoning_configured=false`.

No prompt content or credentials are added to these logs.

## xAI stream terminal diagnostics

The xAI executor emits one `xai stream terminal` structured record per stream.
It captures:

- whether `response.completed` arrived
- whether upstream usage was present
- input, output, and cached token counts
- whether translated `message_delta` and `message_stop` events were emitted
- whether the request context was canceled
- whether the stream scanner failed
- a normalized terminal outcome

Possible outcomes include:

```text
completed_with_usage
completed_without_usage
completed_translation_incomplete
completed_downstream_canceled
canceled_before_completed
scan_error_before_completed
disconnected_before_completed
```

These fields diagnose missing token reporting and incomplete Claude-compatible
stream translation without logging response content.

## Current fork commits

The functional fork patches are:

| Commit | Change |
| --- | --- |
| `7277dad4` | Advertise Claude context windows |
| `e14798e9` | Expose subscription quota windows |
| `63abcf13` | Report resolved upstream reasoning effort |
| `8165a639` | Diagnose xAI stream token reporting |
| `fb187552` | Classify Codex windows by duration |
| `b839d015` | Preserve real model IDs end to end |
| `150220fd` | Read Grok credit usage from unified billing |
| `0660b59a` | Expose public list-price metadata |

To inspect the live relationship with upstream:

```bash
git fetch upstream
git rev-list --left-right --count upstream/main...main
git cherry -v upstream/main main
git diff --stat upstream/main...main
```

At the time this document was written, the fork contained all commits from
`upstream/main` and carried only the functional patches listed above plus an
upstream merge commit.

## Maintaining the fork

When updating from upstream:

1. Fetch and merge `upstream/main` without rewriting the fork's published
   history.
2. Run `git cherry -v upstream/main main` to detect patches that upstream has
   adopted independently.
3. Re-run the focused tests for every retained delta.
4. Run the full Go test suite and required compile check from `AGENTS.md`.
5. Build and install the forked binary, then verify `/v1/models`,
   `/v1/subscription-usage`, streaming responses, and `all-models-patch`
   against the live service.

If upstream adopts an equivalent patch, prefer removing the fork delta during
the next upstream merge instead of maintaining two implementations.
