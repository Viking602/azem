# Terminal-Bench / Harbor

Azem runs unattended Terminal-Bench jobs through Harbor as an installed
agent. The harness starts a task container, Azem edits that container, then
Harbor's verifier scores the final filesystem. Azem does not grade itself.

## Build

```bash
make azem-eval-linux
```

This writes a host `azem-eval` (used to export a slim auth database) and
Linux amd64/arm64 binaries that Harbor copies into each trial container.

## Run

Credentials come from `~/.azem/azem.db` (`accounts` and
`auth_credentials` only), falling back to `~/.config/azem/azem.db` if
the new home has not been created yet. Do not copy the full desktop database into
trials.

```bash
export PYTHONPATH="$PWD"
harbor run \
  -d terminal-bench/terminal-bench-2 \
  -a eval.harbor.azem_agent:Azem \
  -m chatgpt/gpt-5.6-sol \
  -n 1 -k 1 \
  --yes \
  --allow-agent-host chatgpt.com \
  --allow-agent-host ab.chatgpt.com \
  --allow-agent-host api.x.ai \
  -i hello-world
```

Omit `-i` to run the whole dataset. Results land in `jobs/`.

Grok subscription evals should stay at `-n 1`. Each trial refreshes the
host token before upload and writes rotated credentials back afterward.
Two concurrent Grok trials can spend the same refresh token.

Bare task images often omit `ca-certificates`. The adapter copies the
host CA bundle into `/installed-agent/cacert.pem` and `/etc/ssl/certs`
so Grok/ChatGPT HTTPS can complete and Harbor can score the trial.

Adapter timeout helpers (no Harbor process required):

```bash
PYTHONPATH="$PWD" python3 -m unittest eval.harbor.timeout_test
```

## Durable trajectory and offline evaluation

`azem-eval` can export one session without opening the production runtime.
Set `--export-trajectory-db` and `--export-trajectory-session` together;
`--export-trajectory-blobs` overrides the default sibling `blobs` directory,
and `--export-trajectory-out` selects an atomic mode-0600 JSON file or `-` for
stdout. The database connection is read-only and query-only. Missing referenced
blob bytes or digest mismatches fail the export instead of producing a partial
trajectory.

`--baseline-out` writes the provider/model, static prompt, tool catalog,
repository commit/dirty state, dependency locks, validator versions, and task
prompt identity before a live eval turn. Replay/noise fixtures and the pinned
task registry live under `internal/eval/testdata`.

The remaining learning pipeline is library code, not a production background
trainer:

- `internal/eval` performs strict replay, incident labeling, paired-fixture
  validation, task synthesis, and isolated validators.
- `internal/routeeval` records shadow decisions and compares deterministic
  calibration baselines without changing the control route.
- `internal/training` builds consumption-lineage policies, compares adapter
  methods, evaluates boundary decisions, and produces the held-out ship gate.
- `internal/toollab` keeps generated tools non-installable through isolated
  checks and a separate human promotion record.
- `internal/adapterdeployment` attaches only a validated exact-model
  substitution to the existing provider route and supports rollback/kill.

## Notes

- Eval turns force `approval_mode: yolo` and `shell_policy: allow`.
- A hanging `ask` / plan card fails the trial instead of waiting.
- A suspension for the current run exits immediately with `run suspended` and
  the runtime reason. Unknown tool outcomes retain their reconciliation state;
  the evaluator does not replay them or wait for a misleading timeout. Harbor
  still scores the workspace independently of this harness error.
- The adapter honors Harbor's agent timeout (task.toml, an override, or
  `AZEM_EVAL_TIMEOUT`). It stops `azem-eval` before Harbor's `wait_for` so
  the verifier can score without `AgentTimeoutError`. Remaining time is
  injected as private tail context, not the static instruction prefix.
- Bounded main turns also receive one wrap-up steer in the last 90 seconds
  (at most 20% of their remaining budget at engine binding). It can interrupt
  ongoing reasoning safely so the model has time to save and verify its work;
  it does not extend the deadline or guarantee that the task will pass.
- One shell command is capped by `workspace.shell.max_wall_clock` (default
  `10m`). The model may request less with `wall_clock_seconds`.
- For existing stores, `--refresh-auth` and `--sync-auth-*` only touch `accounts` and
  `auth_credentials`. Syncing into the desktop database uses raw SQLite
  and does not take the runtime recovery fence.
- Do not disable TLS verification. Trials without a system CA store
  must receive the uploaded bundle.

A missing `--sync-auth-to` file is initialized through the normal SQLite store
before copying credentials. Existing destination files stay on the raw SQLite
path; credential sync does not migrate or reject a newer host schema. Newly
inserted accounts receive `sqlite:<provider>:<account>` references. Legacy bare
`sqlite` references are repaired even when no newer token needs copying; other
existing credential-store bindings are preserved.
