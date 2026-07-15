# Security Policy

## Release verification

Releases of this limited fork use fork-specific tags such as
`v7.2.79-fork.1`. The tags are signed, and the release includes an SSH-signed
manifest covering every platform archive plus `checksums.txt`. The manifest
records the full Git source commit used by the release workflow.

The release-manifest signing key has this fingerprint:

```text
SHA256:yLJtSegpLNiWyJYHeHI3MwP4qez0n+CF+K/EOHos6KY
```

The release-manifest signing identity is `callmemorgan` and its SSH signature
namespace is `cliproxyapi-release`. The signed Git tag uses the same key in the
`git` namespace. Verify the fingerprint independently before trusting the
included `release-signers` file.

From a checkout of the released tag, download all release assets into one
directory and run:

```bash
bin/verify-release-artifacts v7.2.79-fork.1 /path/to/downloaded/assets
```

The verifier rejects the release unless the Git tag and release manifest have
valid signatures from the pinned key and their respective identities and
namespaces, the manifest's source commit matches the tag, its asset set exactly
matches the release archives and checksum file, every size and SHA-256 digest
matches, and `checksums.txt` verifies all platform archives.

GitHub's “Verified” label on a tag or commit is useful but is not a substitute
for verifying the release manifest and downloaded artifacts.

## Scope and vulnerability reports

This fork has no commercial support or security-response SLA. Please report a
vulnerability in an explicitly documented downstream patch through GitHub's
private vulnerability reporting for `callmemorgan/CLIProxyAPI` when available.
Report vulnerabilities reproducible on unmodified CLIProxyAPI to upstream.

Never include provider credentials, OAuth tokens, API keys, prompts, request
content, or private configuration in a report.
