# CLIProxyAPI — limited personal fork

This repository is Morgan's narrow downstream fork of
[`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI).
It exists to support a specific local multi-model Claude Code setup built
around [`callmemorgan/all-models-patch`](https://github.com/callmemorgan/all-models-patch).
It is not a new general-purpose distribution of CLIProxyAPI.

> [!IMPORTANT]
> **This fork is not sponsored.** Morgan receives no money, services, credits,
> referral fees, or other consideration from any model provider, API relay,
> account reseller, or other company mentioned by the upstream project. No
> company or provider has reviewed, approved, or endorsed this fork.

There are no affiliate or referral links in this README. Sponsor and affiliate
material published by the upstream project—including material still present in
inherited translated README files—belongs to upstream; it does not describe a
relationship with Morgan or this fork.

## What this fork is

Most of this codebase is upstream CLIProxyAPI. The fork keeps upstream's proxy,
authentication, translation, provider, account-selection, and SDK architecture,
then carries a small set of integration patches needed by `all-models-patch`:

- real provider model IDs through the Claude-compatible API;
- sanitized subscription-quota reporting;
- selected Claude context metadata used by the local harness; and
- focused operational diagnostics for reasoning settings and stream usage.

The established downstream patches, their implementation locations, and
maintenance notes are documented in [FORK.md](FORK.md). The Git history remains
the definitive record of the fork delta.

## What this fork is not

- It is not the official CLIProxyAPI repository.
- It is not affiliated with or endorsed by `router-for-me`.
- It is not a wholesale rewrite or an independent product roadmap.
- It is not intended to replace upstream for general users.
- It does not include commercial support, uptime promises, security response
  guarantees, or a compatibility SLA.
- It does not promise that every upstream feature, provider, release, or
  management UI will work with Morgan's patches at all times.

Maintenance is best-effort and driven by one concrete integration. Changes that
do not serve that integration will generally be left to upstream. The fork may
temporarily lag upstream, and downstream patches may be removed when upstream
ships equivalent behavior.

## Where to get help

Use the repository that owns the behavior you are asking about:

| Question or problem | Right place |
| --- | --- |
| General setup, provider support, authentication, configuration, or upstream behavior | [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) and the [upstream guides](https://help.router-for.me/) |
| A regression reproducible on unmodified upstream CLIProxyAPI | Upstream's issue tracker |
| One of the explicitly documented patches in [FORK.md](FORK.md) | This fork's issue tracker |
| The patched Claude Code client or local multi-model harness | [callmemorgan/all-models-patch](https://github.com/callmemorgan/all-models-patch) |

Please do not send upstream maintainers support requests for behavior introduced
only by this fork. Likewise, this fork is not a general support channel for
upstream CLIProxyAPI.

## Core upstream capabilities

Inherited upstream functionality includes:

- OpenAI-, Gemini-, and Claude-compatible API surfaces;
- OAuth-backed access for supported coding-model subscriptions;
- multiple-account selection and failover;
- streaming, tool use, and multimodal requests where providers support them;
- configurable OpenAI-compatible upstream providers; and
- an embeddable Go SDK.

Those capabilities are upstream work, not original features of this fork. See
the [upstream repository](https://github.com/router-for-me/CLIProxyAPI) for the
current supported-provider matrix and user documentation.

## Building this fork

This is primarily a source fork for Morgan's own deployment. If you deliberately
want to test it anyway, use Go 1.26 or newer:

```bash
git clone https://github.com/callmemorgan/CLIProxyAPI.git
cd CLIProxyAPI
cp config.example.yaml config.yaml
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

Review `config.yaml` before starting the server. In particular, bind to
`127.0.0.1` unless you intentionally want to expose the proxy to a network, and
replace the example API keys. Never commit provider credentials, OAuth tokens,
or a populated local config.

Authentication flags and configuration details are inherited from upstream;
consult the [upstream guides](https://help.router-for.me/) rather than assuming
this short README is complete operational documentation.

## Releases

This fork uses fork-specific release tags such as `v7.2.79-fork.2`; it does not
republish upstream version tags as though they were independent fork releases.
Every release tag is signed, and every platform archive plus `checksums.txt` is
bound to its full source commit by an SSH-signed release manifest.

See [SECURITY.md](SECURITY.md) for the pinned signer fingerprint, signature
namespace, trust boundaries, and end-to-end verification command. A checksum by
itself detects corruption but does not establish who published an artifact, so
unsigned checksum files are not treated as sufficient release verification.

## Contributing

Small fixes to the documented fork delta are welcome for discussion. Broad new
features and general CLIProxyAPI improvements should normally be proposed
upstream first. A pull request may be declined simply because maintaining it is
outside this fork's limited purpose.

Before proposing a downstream change, read [FORK.md](FORK.md) and confirm that
the behavior is specific to this fork. Code changes should pass:

```bash
go test ./...
go build -o test-output ./cmd/server && rm test-output
```

## License and attribution

CLIProxyAPI is upstream work by the CLIProxyAPI contributors. This repository
retains the upstream module path, history, copyright notices, and MIT
license. See [LICENSE](LICENSE).

Forking does not imply sponsorship, partnership, endorsement, or transfer of
upstream support obligations in either direction.
