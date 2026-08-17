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

Credentials come from `~/.config/azem/azem.db` (`accounts` and
`auth_credentials` only). Do not copy the full desktop database into
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

## Notes

- Eval turns force `approval_mode: yolo` and `shell_policy: allow`.
- A hanging `ask` / plan card fails the trial instead of waiting.
- One shell command is capped by `workspace.shell.max_wall_clock` (default
  `10m`). The model may request less with `wall_clock_seconds`.
- `--refresh-auth` and `--sync-auth-*` only touch `accounts` and
  `auth_credentials`. Syncing into the desktop database uses raw SQLite
  and does not take the runtime recovery fence.
- Do not disable TLS verification. Trials without a system CA store
  must receive the uploaded bundle.
