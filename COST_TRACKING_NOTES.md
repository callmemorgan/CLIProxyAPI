# Cost Tracking Notes

Updated: 2026-07-19

## Product context

The current reason to use Claude Code is the quality and maturity of its
workflow harness. Its team has implemented the agent workflow further than the
current Codex CLI, and Claude Code's system prompt is materially smaller and
more focused. The current Codex system prompt is viewed as too large, noisy,
and opinionated.

This is not necessarily a preference for Claude models. The proxy and
`claude-all` exist to keep Claude Code's workflow while allowing other models
and subscriptions to provide the compute.

## Theo's tracking setup

Theo Browne has described an off-the-shelf setup rather than a custom
multi-harness accounting system:

- [`ccusage`](https://github.com/ccusage/ccusage) is his primary local token
  and API-equivalent inference-cost tracker. He has said it appears most
  accurate in his manual comparisons.
- [CodexBar](https://github.com/steipete/CodexBar) provides convenient menu-bar
  usage visibility, especially for Codex.
- [TRMNL](https://usetrmnl.com/) is a physical display for summarized monthly
  token usage.

Primary references:

- [Theo on ccusage versus CodexBar](https://x.com/theo/status/2064254699709350019)
- [Theo's $1,100 API-equivalent inference estimate](https://x.com/theo/status/2064214943210324243)
- [Theo's CodexBar identification](https://x.com/theo/status/2013386218088919532)
- [Theo's TRMNL usage display](https://x.com/theo/status/2068130475525468610)
- [Theo's support for the ccusage author](https://x.com/theo/status/2060496307530461473)

The lesson is to support these tools and presentation surfaces where useful,
not to build a replacement for every one of them.

## Accounting semantics

Never expose one ambiguous `cost` number. Keep these concepts distinct:

1. **Actual billed cost**: provider charges or credits observed from an
   authoritative billing source.
2. **API-equivalent inference value**: tokens multiplied by dated public list
   prices, including cache reads and writes where the provider publishes them.
3. **Subscription quota consumption**: provider-defined windows, percentages,
   credits, and reset times. These often cannot be derived reliably from token
   counts.

Every displayed dollar value should carry its semantic type, currency, pricing
snapshot date, and whether it is authoritative or estimated.

## Current local proxy state

The installed proxy listens only on `127.0.0.1:8317`.

- API root: `http://127.0.0.1:8317/`
- Management panel when enabled: `http://127.0.0.1:8317/management.html`
- The management panel is currently disabled, so that URL returns `404`.
- Management API access is also currently unavailable because no management
  secret is configured.
- `usage-statistics-enabled` is currently `false`.

The fork already has useful foundations:

- Per-request runtime records include provider, concrete model, requested
  alias, auth identity, endpoint/source metadata, latency, failure state,
  reasoning effort, service tier, and input/output/reasoning/cache token counts.
- The management usage queue can expose those records, but it is an ephemeral
  in-memory stream with short retention. It is not a durable accounting ledger.
- `/v1/subscription-usage` exposes normalized authoritative quota windows for
  supported subscription providers.
- `/v1/models` exposes dated public list-price metadata.
- `/api/claude_cli/bootstrap` projects compatible token prices into Claude
  Code's `additional_model_costs` format.
- Recent request counts exist for API-key-backed routes, but they are not token
  cost history.

The public-price catalog also distinguishes exact model prices from internal
route aliases. Alias-derived records carry `pricing_basis` set to
`alias_list_equivalent` and a `mapped_from` public model ID. This prevents an
API-equivalent estimate from being mistaken for a directly published price for
the local alias.

Therefore, pricing metadata and request token capture already exist, but
durable cost tracking does not.

## Desired proxy direction

The proxy should become the authoritative ledger for traffic it routes while
remaining compatible with local tools such as `ccusage`.

For each completed request, persist at least:

- timestamp, request ID, client/API-key identity, endpoint, and project/session
  identity when available;
- requested alias, routed provider, concrete upstream model, auth identity,
  reasoning effort, and service tier;
- input, output, reasoning, cache-read, and cache-write tokens;
- latency, time to first token, terminal state, and failure metadata;
- actual billed cost when authoritative data exists;
- API-equivalent cost calculated from a versioned pricing snapshot;
- subscription quota observations as separate time-series data.

Useful read surfaces would include:

- daily, weekly, monthly, provider, model, project, and session summaries;
- an authenticated JSON API for the status line, menu-bar clients, and TRMNL;
- export formats that `ccusage` or a small adapter can consume;
- a compact live view and a durable historical view;
- clear badges or fields for `actual`, `estimated_list_price`, and
  `subscription_quota` values.

Prompt bodies, response bodies, OAuth tokens, and provider secrets should not
be part of the accounting ledger.
