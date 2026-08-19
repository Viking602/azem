"""Harbor trial timeout helpers for the Azem installed-agent adapter.

Harbor wraps ``agent.run`` with ``asyncio.wait_for`` using the task's agent
timeout. The adapter must stop ``azem-eval`` before that deadline so Harbor
does not raise ``AgentTimeoutError`` while the process is still running.
"""

from __future__ import annotations

import json
import os
import shlex
from collections.abc import Mapping
from pathlib import Path
from typing import Any

# Leave slack so azem-eval can cancel and shut down before Harbor's wait_for.
WRAP_SEC = 30
EXEC_HEADROOM_SEC = 15
RETURN_HEADROOM_SEC = 10

_TIMEOUT_MARKERS = (
    "turn timed out",
    "command timed out after",
    "agent execution timed out after",
)


def parse_positive_seconds(value: Any) -> float | None:
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        seconds = float(value)
        return seconds if seconds > 0 else None
    text = str(value).strip()
    if not text:
        return None
    try:
        seconds = float(text)
    except ValueError:
        return None
    return seconds if seconds > 0 else None


def parse_task_agent_timeout_sec(text: str) -> float | None:
    """Read ``[agent] timeout_sec`` from a task.toml body."""
    in_agent = False
    for raw in text.splitlines():
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        if line.startswith("[") and line.endswith("]"):
            in_agent = line[1:-1].strip() == "agent"
            continue
        if not in_agent or not line.startswith("timeout_sec"):
            continue
        _, _, value = line.partition("=")
        return parse_positive_seconds(value.strip().strip("\"'"))
    return None


def split_timeouts(budget_sec: float) -> tuple[int, int]:
    """Return ``(azem-eval --timeout, docker exec timeout)`` inside *budget_sec*."""
    budget = int(budget_sec)
    if budget <= 0:
        raise ValueError(f"timeout budget must be positive, got {budget_sec!r}")
    wrap = WRAP_SEC
    exec_headroom = EXEC_HEADROOM_SEC
    if budget <= wrap + 5:
        wrap = max(2, budget // 5)
        exec_headroom = max(1, wrap // 2)
    eval_sec = max(1, budget - wrap)
    exec_sec = max(eval_sec, budget - exec_headroom)
    if exec_sec >= budget and budget > 1:
        exec_sec = budget - 1
    if eval_sec > exec_sec:
        eval_sec = exec_sec
    return eval_sec, exec_sec
def work_timeout_sec(budget_sec: float) -> float:
    """Bound adapter work so auth synchronization and return happen before Harbor."""
    return max(1.0, float(budget_sec) - RETURN_HEADROOM_SEC)


def cleanup_timeout_sec(budget_sec: float, elapsed_sec: float) -> float:
    """Return bounded cleanup time while preserving one second to return."""
    remaining = float(budget_sec) - float(elapsed_sec) - 1.0
    return max(0.0, min(float(RETURN_HEADROOM_SEC - 1), remaining))




def resolve_agent_timeout_sec(
    *,
    logs_dir: Path | None,
    explicit: Any = None,
    environ: Mapping[str, str] | None = None,
) -> float | None:
    """Resolve Harbor's agent-phase budget.

    Precedence: constructor/kwarg, ``AZEM_EVAL_TIMEOUT`` /
    ``HARBOR_AGENT_TIMEOUT_SEC``, then trial ``config.json`` plus the task's
    ``task.toml``. Harbor only injects ``agent_timeout_sec`` into the Oracle
    agent, so import-path adapters must recover the same number Harbor uses
    for ``asyncio.wait_for``.
    """
    if seconds := parse_positive_seconds(explicit):
        return seconds
    env = os.environ if environ is None else environ
    if seconds := parse_positive_seconds(
        env.get("AZEM_EVAL_TIMEOUT") or env.get("HARBOR_AGENT_TIMEOUT_SEC")
    ):
        return seconds
    return timeout_from_trial_dir(logs_dir)


def timeout_from_trial_dir(logs_dir: Path | None) -> float | None:
    if logs_dir is None:
        return None
    config_path = _trial_config_path(logs_dir)
    if config_path is None:
        return None
    try:
        config = json.loads(config_path.read_text())
    except (OSError, json.JSONDecodeError):
        return None
    if not isinstance(config, dict):
        return None
    return timeout_from_trial_config(config, task_toml=_task_toml_text(config))


def timeout_from_trial_config(
    config: Mapping[str, Any], *, task_toml: str | None
) -> float | None:
    agent = config.get("agent") if isinstance(config.get("agent"), Mapping) else {}
    override = parse_positive_seconds(agent.get("override_timeout_sec"))
    base = override
    if base is None and task_toml:
        base = parse_task_agent_timeout_sec(task_toml)
    if base is None:
        return None
    max_sec = parse_positive_seconds(agent.get("max_timeout_sec"))
    if max_sec is not None:
        base = min(base, max_sec)
    multiplier = config.get("agent_timeout_multiplier")
    if multiplier is None:
        multiplier = config.get("timeout_multiplier")
    if multiplier is None:
        multiplier = 1.0
    try:
        scale = float(multiplier)
    except (TypeError, ValueError):
        scale = 1.0
    if scale <= 0:
        scale = 1.0
    return base * scale


def is_eval_timeout_error(exc: BaseException) -> bool:
    text = str(exc).lower()
    return any(marker in text for marker in _TIMEOUT_MARKERS)


def azem_eval_command(
    *,
    binary: str,
    config: str,
    prompt: str,
    provider: str,
    model: str,
    reasoning: str,
    timeout_sec: int | None,
) -> str:
    timeout_flag = "--timeout 0" if timeout_sec is None else f"--timeout {int(timeout_sec)}s"
    return (
        f"{shlex.quote(binary)} "
        f"--workspace . "
        f"--config {shlex.quote(config)} "
        f"--prompt-file {shlex.quote(prompt)} "
        f"--provider {shlex.quote(provider)} "
        f"--model {shlex.quote(model)} "
        f"--reasoning {shlex.quote(reasoning)} "
        f"{timeout_flag} "
        f"--print-events"
    )


def _trial_config_path(logs_dir: Path) -> Path | None:
    for candidate in (logs_dir / "config.json", logs_dir.parent / "config.json"):
        if candidate.is_file():
            return candidate
    return None


def _task_toml_text(config: Mapping[str, Any]) -> str | None:
    task = config.get("task")
    if not isinstance(task, Mapping):
        return None
    path = task.get("path")
    if not path:
        return None
    toml_path = Path(str(path)) / "task.toml"
    try:
        return toml_path.read_text()
    except OSError:
        return None
