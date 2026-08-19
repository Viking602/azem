"""Tests for Harbor agent-timeout discovery and slack."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from eval.harbor.timeouts import (
    EXEC_HEADROOM_SEC,
    RETURN_HEADROOM_SEC,
    WRAP_SEC,
    azem_eval_command,
    cleanup_timeout_sec,
    is_eval_timeout_error,
    parse_task_agent_timeout_sec,
    resolve_agent_timeout_sec,
    split_timeouts,
    timeout_from_trial_config,
    work_timeout_sec,
)


class ParseTaskTimeoutTest(unittest.TestCase):
    def test_reads_agent_section(self) -> None:
        text = """
[verifier]
timeout_sec = 120.0

[agent]
timeout_sec = 3600.0

[environment]
build_timeout_sec = 600.0
"""
        self.assertEqual(parse_task_agent_timeout_sec(text), 3600.0)

    def test_ignores_commented_and_other_sections(self) -> None:
        text = """
[agent]
# timeout_sec = 1
timeout_sec = 900.0 # seconds
"""
        self.assertEqual(parse_task_agent_timeout_sec(text), 900.0)


class SplitTimeoutsTest(unittest.TestCase):
    def test_leaves_slack_inside_harbor_budget(self) -> None:
        eval_sec, exec_sec = split_timeouts(900)
        self.assertEqual(eval_sec, 900 - WRAP_SEC)
        self.assertEqual(exec_sec, 900 - EXEC_HEADROOM_SEC)
        self.assertLess(eval_sec, exec_sec)
        self.assertLess(exec_sec, 900)

    def test_long_windows_task_budget(self) -> None:
        eval_sec, exec_sec = split_timeouts(3600)
        self.assertEqual(eval_sec, 3570)
        self.assertEqual(exec_sec, 3585)

    def test_tiny_budget_stays_inside(self) -> None:
        eval_sec, exec_sec = split_timeouts(10)
        self.assertGreaterEqual(eval_sec, 1)
        self.assertLessEqual(eval_sec, exec_sec)
        self.assertLess(exec_sec, 10)

    def test_outer_work_deadline_reserves_cleanup_and_return(self) -> None:
        self.assertEqual(work_timeout_sec(900), 900 - RETURN_HEADROOM_SEC)
        self.assertEqual(cleanup_timeout_sec(900, 885), RETURN_HEADROOM_SEC - 1)
        self.assertEqual(cleanup_timeout_sec(900, 899.5), 0)


class ResolveTimeoutTest(unittest.TestCase):
    def test_explicit_wins(self) -> None:
        got = resolve_agent_timeout_sec(
            logs_dir=None,
            explicit=1200,
            environ={"AZEM_EVAL_TIMEOUT": "900"},
        )
        self.assertEqual(got, 1200.0)

    def test_env_wins_over_trial(self) -> None:
        got = resolve_agent_timeout_sec(
            logs_dir=None,
            explicit=None,
            environ={"HARBOR_AGENT_TIMEOUT_SEC": "450"},
        )
        self.assertEqual(got, 450.0)

    def test_trial_config_and_task_toml(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            task = root / "task"
            task.mkdir()
            (task / "task.toml").write_text(
                '[verifier]\ntimeout_sec = 120.0\n\n[agent]\ntimeout_sec = 3600.0\n',
                encoding="utf-8",
            )
            trial = root / "trial"
            agent_dir = trial / "agent"
            agent_dir.mkdir(parents=True)
            (trial / "config.json").write_text(
                json.dumps(
                    {
                        "task": {"path": str(task)},
                        "agent": {"name": "eval.harbor.azem_agent:Azem"},
                    }
                ),
                encoding="utf-8",
            )
            got = resolve_agent_timeout_sec(
                logs_dir=agent_dir, explicit=None, environ={}
            )
            self.assertEqual(got, 3600.0)

    def test_override_and_multiplier_match_harbor(self) -> None:
        got = timeout_from_trial_config(
            {
                "timeout_multiplier": 2.0,
                "agent": {"override_timeout_sec": 400, "max_timeout_sec": 300},
            },
            task_toml="[agent]\ntimeout_sec = 900.0\n",
        )
        self.assertEqual(got, 600.0)


class TimeoutErrorTest(unittest.TestCase):
    def test_classifies_azem_eval_and_docker_timeouts(self) -> None:
        self.assertTrue(is_eval_timeout_error(RuntimeError("turn timed out")))
        self.assertTrue(
            is_eval_timeout_error(RuntimeError("Command timed out after 885 seconds"))
        )
        self.assertFalse(is_eval_timeout_error(RuntimeError("Connection timed out")))
        self.assertFalse(is_eval_timeout_error(RuntimeError("run failed: boom")))


class CommandTest(unittest.TestCase):
    def test_omits_unbounded_flag_when_budget_known(self) -> None:
        command = azem_eval_command(
            binary="/installed-agent/azem-eval",
            config="/installed-agent/xdg/azem/config.yaml",
            prompt="/installed-agent/instruction.md",
            provider="grok",
            model="grok-4.6",
            reasoning="xhigh",
            timeout_sec=870,
        )
        self.assertIn("--timeout 870s", command)
        self.assertNotIn("--timeout 0", command)

    def test_keeps_unbounded_only_when_budget_unknown(self) -> None:
        command = azem_eval_command(
            binary="/installed-agent/azem-eval",
            config="/cfg",
            prompt="/prompt",
            provider="chatgpt",
            model="gpt-5.6-sol",
            reasoning="high",
            timeout_sec=None,
        )
        self.assertIn("--timeout 0", command)


if __name__ == "__main__":
    unittest.main()
