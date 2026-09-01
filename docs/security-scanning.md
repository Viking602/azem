# Security scanning

Last verified: 2026-08-30

Azem Security is a native, provider-independent source audit runtime. It uses
Azem's configured provider drivers, application-owned scan/workers,
request-scoped direct Venat engines through the shared durable runtime,
governed tools, Subagents, SQLite, and desktop/TUI events. Production scanning
does not invoke Codex CLI, `@openai/codex-sdk`, Node, or Python.

## Architecture

`internal/securityscan` owns scan domain state, immutable target snapshots,
Standard and Deep coordination, canonical artifacts, finding identity,
remediation records, exports, and publication receipts. `internal/app` owns
provider resolution and builds a background automation engine around the shared
`ProviderRuntime` and `agent.Service`. Security runs never claim
`Service.activeRun`, so a scan and an interactive conversation may execute at
the same time.

The model can submit semantic drafts and progress only through host-bound
`security.*` tools. Scan ID, worker ID, target identity, output directory,
completion, stable finding IDs, artifact hashes, and database state remain host
owned. Tool arguments cannot select another scan or target.

## Targets and snapshots

Supported targets are repository, scoped paths, committed Git refs, and the
working tree. Deep mode accepts repository and scoped-path targets.

Preflight canonicalizes the repository, sanitizes Git environment overrides,
rejects repository-local Git executables, resolves immutable revisions, and
creates a private read-only snapshot below:

```text
<Azem data>/security-scans/<target-id>/<scan-id>/
```

Audit tools are rooted at the snapshot. Diff scans expose changed paths as the
review scope while retaining unchanged source for data-flow context. A generated
immutable diff artifact records exact hunks plus deleted/base content; untracked
paths are listed and read from the snapshot. Its digest is checked before
finalization and the sealed copy is retained as `artifacts/diff.patch`. On
completion Azem recomputes file, diff, and revision identity. A changed live
target forces partial coverage and blocks remediation until a new scan.

## Standard scans

A Standard scan runs one complete audit. The parent builds a threat model and
may launch only the built-in `security-baseline` and
`security-investigator` roles. Child profiles are forced to the audit's
read-only tool allowlist, cannot nest, and share the per-worker subagent quota.
Progress paths count only when a successful `coding.read_file` receipt exists
for that worker or its constrained child and the path belongs to the host
inventory. A host finalization check downgrades any complete claim with missing
receipts to partial coverage.

The host allows at most two contract corrections after the initial rejected
draft. An accepted draft is immutable. The agent cannot complete the scan.

## Deep scans

Deep Scan schedules independent complete Standard audits and one serial semantic
reducer. Defaults are four workers, three investigator subagents per audit,
four consecutive no-new reductions, three consecutive errors, forty discovery
runs, and a 96-hour absolute scan deadline. Native scans do not set a Token or
tool-call hard ceiling, so partial usage accounting cannot terminate a long
review between requests. Provider/account limits still apply. Workers and
subagents are each bounded to 32; discovery runs are bounded to 1000.

The coordinator persists every worker, reducer, accepted draft, and usage charge
before the next dependent step. Restart recovery cancels stale in-process
worker rows, reloads accepted artifacts, adopts the latest aggregate, and
continues after the last reduced audit without reusing attempt identities. It
stops on saturation, configured run cap, absolute deadline, cancellation, or
repeated unrecoverable errors. If a later reducer fails, the last valid
aggregate is sealed as a partial result instead of discarding completed work.
Zero completed source reviews never becomes a complete no-findings result.

## Artifacts and finding identity

Azem writes `scan-manifest.json`, `findings.json`, `coverage.json`, `report.md`,
and `exports/results.sarif`. Diff scans additionally retain
`artifacts/diff.patch`. JSON documents use contract version `1.0` and producer
`azem-security`. JSON Schema Draft 2020-12 validation, safe relative paths,
atomic writes, SHA-256 records, and post-write revalidation guard the artifact
boundary.

Finding fingerprints retain the `codex-security/v1` material so imported and
native results can preserve root-issue identity. Azem derives `findingId` and
scan-bound `occurrenceId`; model-provided identities are replaced.

## Remediation

Patching requires a completed, non-stale scan and a clean Git checkout. Azem
creates an isolated `azem-security/patch-*` branch/worktree. One fixer handles
one finding, may change only files named by the host-accepted finding
locations, reports every changed path, and cannot claim verification. The host
compares the reported files with the Git diff. A separate read-only verifier
must read every changed file and provide nonempty evidence; Go patches also
require a successful governed `coding.go_test` receipt. Failed or inconclusive
attempts reset to the pre-finding checkpoint. Only independently verified
changes are committed. GitHub publication disables repository Git hooks,
sanitizes Git environment overrides, pins the verified commit, and operates on
the scanned repository.

## UI and commands

The desktop Security page lists scans, coverage, worker progress, findings,
locations, remediation evidence, patch verification, and export paths. It
exposes blocked-scan resume, explicit cancellation and patch confirmations,
visible errors, semantic lists, textual status, keyboard focus, and separate
scrolling for finding navigation and details. Only a short status sentence is a
polite live region; reduced-motion and forced-colors rules are included.

TUI commands:

```text
/security scan [standard|deep]
/security scans
/security show <scan-id>
/security findings <scan-id>
/security cancel <scan-id>
/security resume <scan-id>
/security patch <occurrence-id>
/security triage <occurrence-id> <open|false-positive|already-fixed|wont-fix>
/security patch-pr <occurrence-id>
/security export <scan-id> <json|csv|sarif>
/security publish <scan-id>
/security reconcile-publication <scan-id> <occurrence-id> <published|retry>
```

## Publication

Linear or another tracker is selected only by
`security.publication_tool`. Destination, base arguments, and title/description
field names are also host configuration; action payloads cannot select another
driver or schema. Desktop JavaScript cannot invoke publication or PR creation.
The explicit TUI commands create durable governed actions. Publication
atomically claims each occurrence before the MCP call and never automatically
replays `publishing` or failed rows. After checking the tracker, the user must
explicitly reconcile a claim as `published` or release it with `retry`.
Secrets remain MCP environment/keyring references.

## Verification

Run the native package tests first, then the complete project checks:

```bash
go test ./internal/securityscan ./internal/app ./internal/store/sqlite ./internal/githubpr ./internal/tui
make contracts-check
make architecture-check
make test
make test-gpui
```

Desktop changes additionally require building and launching the real GPUI app
and exercising scan list, start, progress, cancellation, finding detail, export,
and keyboard navigation.
