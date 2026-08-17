"""Harbor installed-agent adapter for Azem Terminal-Bench runs."""

from __future__ import annotations

import fcntl
import os
import shlex
import shutil
import subprocess
import tempfile
import threading
from pathlib import Path

from harbor.agents.installed.base import BaseInstalledAgent, with_prompt_template
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

REPO_ROOT = Path(__file__).resolve().parents[2]
HOST_EVAL = REPO_ROOT / "dist" / "eval" / "azem-eval"
REMOTE_BIN = "/installed-agent/azem-eval"
REMOTE_XDG = "/installed-agent/xdg"
REMOTE_AUTH = f"{REMOTE_XDG}/azem/azem.db"
REMOTE_CONFIG = f"{REMOTE_XDG}/azem/config.yaml"
REMOTE_PROMPT = "/installed-agent/instruction.md"
REMOTE_CA = "/installed-agent/cacert.pem"
HOST_CA_CANDIDATES = (
    Path("/etc/ssl/cert.pem"),
    Path("/etc/ssl/certs/ca-certificates.crt"),
    Path("/etc/pki/tls/certs/ca-bundle.crt"),
)
ARCH_TO_GO = {
    "x86_64": "amd64",
    "amd64": "amd64",
    "aarch64": "arm64",
    "arm64": "arm64",
}
_AUTH_LOCK = threading.Lock()


class Azem(BaseInstalledAgent):
    """Installs a Linux azem-eval binary and runs one YOLO turn."""

    @staticmethod
    def name() -> str:
        return "azem"

    def get_version_command(self) -> str | None:
        return f"{REMOTE_BIN} -version"

    async def install(self, environment: BaseEnvironment) -> None:
        uname = await environment.exec(command="uname -m")
        goarch = ARCH_TO_GO.get((uname.stdout or "").strip(), "amd64")
        binary = REPO_ROOT / "dist" / "eval" / f"azem-eval-linux-{goarch}"
        if not binary.is_file():
            raise RuntimeError(
                f"missing {binary}; run `make azem-eval-linux` from the Azem repo"
            )
        await environment.upload_file(binary, REMOTE_BIN)
        await self.exec_as_root(environment, command=f"chmod 755 {shlex.quote(REMOTE_BIN)}")
        await self.exec_as_root(
            environment,
            command=f"mkdir -p {shlex.quote(REMOTE_XDG + '/azem')}",
        )
        await self._install_ca_bundle(environment)
        auth = self._prepare_host_auth()
        if auth is not None:
            await environment.upload_file(auth, REMOTE_AUTH)
            if environment.default_user is not None:
                owner = shlex.quote(str(environment.default_user))
                await self.exec_as_root(
                    environment,
                    command=f"chown -R {owner} {shlex.quote('/installed-agent')}",
                )

    @with_prompt_template
    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        provider, model = self._provider_and_model()
        await self._upload_config_text(
            environment,
            content=instruction,
            remote_path=REMOTE_PROMPT,
            filename="instruction.md",
        )
        env = {
            "XDG_CONFIG_HOME": REMOTE_XDG,
            "XDG_DATA_HOME": REMOTE_XDG,
            "XDG_STATE_HOME": REMOTE_XDG,
            "HOME": "/installed-agent",
            "SSL_CERT_FILE": REMOTE_CA,
            "SSL_CERT_DIR": "/etc/ssl/certs",
            "CURL_CA_BUNDLE": REMOTE_CA,
            "REQUESTS_CA_BUNDLE": REMOTE_CA,
            "GIT_SSL_CAINFO": REMOTE_CA,
        }
        command = (
            f"{shlex.quote(REMOTE_BIN)} "
            f"--workspace . "
            f"--config {shlex.quote(REMOTE_CONFIG)} "
            f"--prompt-file {shlex.quote(REMOTE_PROMPT)} "
            f"--provider {shlex.quote(provider)} "
            f"--model {shlex.quote(model)} "
            f"--reasoning {shlex.quote(os.environ.get('AZEM_EVAL_REASONING', 'high'))} "
            f"--timeout 0 "
            f"--print-events"
        )
        try:
            result = await self.exec_as_agent(environment, command=command, env=env)
            context.metadata = {
                "agent": "azem",
                "provider": provider,
                "model": model,
                "stdout_tail": (result.stdout or "")[-2000:],
            }
        finally:
            await self._sync_remote_auth(environment)

    async def _install_ca_bundle(self, environment: BaseEnvironment) -> None:
        host = host_ca_bundle()
        if host is None:
            return
        await environment.upload_file(host, REMOTE_CA)
        remote = shlex.quote(REMOTE_CA)
        await self.exec_as_root(
            environment,
            command=(
                "mkdir -p /etc/ssl/certs /etc/pki/tls/certs && "
                f"cp {remote} /etc/ssl/certs/ca-certificates.crt && "
                f"cp {remote} /etc/ssl/cert.pem && "
                f"cp {remote} /etc/pki/tls/certs/ca-bundle.crt && "
                f"chmod 644 {remote} /etc/ssl/certs/ca-certificates.crt "
                "/etc/ssl/cert.pem /etc/pki/tls/certs/ca-bundle.crt"
            ),
        )

    def _provider_and_model(self) -> tuple[str, str]:
        raw = (self.model_name or os.environ.get("AZEM_EVAL_MODEL") or "chatgpt/gpt-5.6-sol").strip()
        if "/" in raw:
            provider, model = raw.split("/", 1)
            return provider, model
        return os.environ.get("AZEM_EVAL_PROVIDER", "chatgpt"), raw

    def _prepare_host_auth(self) -> Path | None:
        with _AUTH_LOCK:
            dest = self._slim_auth_path()
            if dest is None:
                return None
            with self._auth_file_lock(dest):
                source = Path.home() / ".config" / "azem" / "azem.db"
                if source.is_file() and dest.is_file():
                    self._run_host_eval(
                        ["--sync-auth-from", str(source), "--sync-auth-to", str(dest)],
                        check=False,
                    )
                elif source.is_file() and not dest.is_file():
                    self._run_host_eval(
                        ["--export-auth-from", str(source), "--export-auth-to", str(dest)],
                        check=True,
                    )
                if not dest.is_file():
                    return None
                self._run_host_eval(["--refresh-auth", str(dest)], check=True)
                if source.is_file():
                    self._run_host_eval(
                        ["--sync-auth-from", str(dest), "--sync-auth-to", str(source)],
                        check=False,
                    )
                return dest

    async def _sync_remote_auth(self, environment: BaseEnvironment) -> None:
        dest = self._slim_auth_path()
        if dest is None:
            return
        tmp = Path(tempfile.mkdtemp(prefix="azem-eval-auth-"))
        try:
            local = tmp / "azem.db"
            try:
                await environment.download_file(REMOTE_AUTH, local)
            except Exception:
                return
            for suffix in ("-wal", "-shm"):
                try:
                    await environment.download_file(REMOTE_AUTH + suffix, tmp / f"azem.db{suffix}")
                except Exception:
                    pass
            if not local.is_file():
                return
            with _AUTH_LOCK:
                with self._auth_file_lock(dest):
                    self._run_host_eval(
                        ["--sync-auth-from", str(local), "--sync-auth-to", str(dest)],
                        check=False,
                    )
                    source = Path.home() / ".config" / "azem" / "azem.db"
                    if source.is_file():
                        self._run_host_eval(
                            ["--sync-auth-from", str(local), "--sync-auth-to", str(source)],
                            check=False,
                        )
        finally:
            shutil.rmtree(tmp, ignore_errors=True)

    def _slim_auth_path(self) -> Path | None:
        if explicit := os.environ.get("AZEM_EVAL_AUTH_DB"):
            path = Path(explicit)
            return path if path.is_file() or path.parent.is_dir() else None
        return REPO_ROOT / "dist" / "eval" / "azem-auth.db"

    def _host_eval_bin(self) -> Path:
        host_bin = HOST_EVAL
        if not host_bin.is_file():
            host_bin = REPO_ROOT / "azem-eval"
        if not host_bin.is_file():
            raise RuntimeError("build azem-eval first with `make azem-eval`")
        return host_bin

    def _run_host_eval(self, args: list[str], *, check: bool) -> None:
        subprocess.run([str(self._host_eval_bin()), *args], check=check)

    def _auth_file_lock(self, dest: Path):
        dest.parent.mkdir(parents=True, exist_ok=True)
        lock_path = dest.with_name(dest.name + ".lock")
        handle = open(lock_path, "a+")
        fcntl.flock(handle, fcntl.LOCK_EX)
        return _close_lock(handle)


def host_ca_bundle() -> Path | None:
    for path in HOST_CA_CANDIDATES:
        try:
            if path.is_file() and path.stat().st_size > 1024:
                return path
        except OSError:
            continue
    return None


class _close_lock:
    def __init__(self, handle) -> None:
        self.handle = handle

    def __enter__(self):
        return self.handle

    def __exit__(self, exc_type, exc, tb) -> None:
        try:
            fcntl.flock(self.handle, fcntl.LOCK_UN)
        finally:
            self.handle.close()
