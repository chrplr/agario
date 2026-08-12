# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""The client half of the JSON-lines protocol.

One :class:`Engine` owns one ``agario-env`` child process and speaks to it in
strict alternation: write one line, read one line. That is the whole transport.
Everything about the game itself is decided on the other side.
"""

from __future__ import annotations

import atexit
import itertools
import json
import subprocess
import weakref
from typing import Any, Iterable

__all__ = ["Engine", "EngineError", "EngineDied", "ProtocolError", "CommandFailed",
           "server_args", "PROTOCOL"]

# Bumped in lockstep with agarienv.Protocol on the Go side.
PROTOCOL = 1


class EngineError(RuntimeError):
    """Base class for transport failures."""


class EngineDied(EngineError):
    """The child process went away."""


class ProtocolError(EngineError):
    """The child said something the client cannot make sense of.

    This is unrecoverable by design: an out-of-step reply means the request and
    response streams no longer line up, and every later answer would be
    attributed to the wrong request. The engine is closed rather than resynced.
    """


class CommandFailed(EngineError):
    """The server answered ``ok: false``."""

    def __init__(self, kind: str, message: str, request: dict[str, Any]):
        super().__init__(f"{kind}: {message}")
        self.kind = kind
        self.message = message
        self.request = request


def _reap(proc: subprocess.Popen) -> None:
    """Best-effort termination, safe to call from a finalizer or at exit."""
    if proc.poll() is not None:
        return
    try:
        if proc.stdin is not None and not proc.stdin.closed:
            proc.stdin.close()
    except OSError:
        pass
    try:
        proc.wait(timeout=2)
        return
    except subprocess.TimeoutExpired:
        pass
    proc.terminate()
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()


class Engine:
    """A live ``agario-env`` process.

    The handshake runs in the constructor, so :attr:`meta` — the arena size, the
    frame skip, the action dimensions and the padding lengths — is available
    immediately and the caller never has to guess the spaces.
    """

    def __init__(
        self,
        binary: str,
        args: Iterable[str] = (),
        *,
        capture_stderr: bool = False,
    ):
        argv = [binary, *args]
        self._closed = False
        # stderr is inherited, not piped: a pipe nobody drains fills up and the
        # child blocks writing to it while we wait for stdout — a deadlock with
        # no symptom other than a hang.
        stderr = subprocess.PIPE if capture_stderr else None
        try:
            self._proc = subprocess.Popen(
                argv,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=stderr,
                # Binary pipes: text mode would translate newlines on Windows
                # and corrupt the framing.
                text=False,
                close_fds=True,
            )
        except OSError as exc:
            raise EngineDied(f"cannot start {argv[0]}: {exc}") from exc

        # A finalizer rather than __del__, which is unreliable during
        # interpreter shutdown; the atexit sweep covers processes still alive
        # when the interpreter goes down in an orderly way. The backstop for
        # everything else is on the Go side: it exits when its stdin closes.
        self._finalizer = weakref.finalize(self, _reap, self._proc)
        _LIVE.add(self)

        self._ids = itertools.count(1)
        answer = self.request({"cmd": "hello"})
        self.meta: dict[str, Any] = answer.get("meta") or {}
        if self.meta.get("protocol") != PROTOCOL:
            self.close()
            raise ProtocolError(
                f"{binary} speaks protocol {self.meta.get('protocol')}, "
                f"this client speaks {PROTOCOL}"
            )

    # ── The one operation ────────────────────────────────────────────────────

    def request(self, message: dict[str, Any]) -> dict[str, Any]:
        """Send one request and return its answer."""
        if self._closed:
            raise EngineDied("the engine is closed")

        request_id = next(self._ids)
        payload = dict(message, id=request_id)
        line = json.dumps(payload, separators=(",", ":")).encode() + b"\n"

        try:
            self._proc.stdin.write(line)
            self._proc.stdin.flush()
        except (BrokenPipeError, OSError) as exc:
            raise self._died(exc) from exc

        raw = self._proc.stdout.readline()
        if not raw:
            raise self._died("no answer")

        try:
            answer = json.loads(raw)
        except json.JSONDecodeError as exc:
            self.close()
            raise ProtocolError(f"unparseable answer: {raw!r}") from exc

        if answer.get("id") != request_id:
            self.close()
            raise ProtocolError(
                f"answer carries id {answer.get('id')}, expected {request_id} — "
                "the request and response streams are out of step"
            )
        if not answer.get("ok"):
            raise CommandFailed(
                answer.get("kind", "unknown"), answer.get("error", ""), payload
            )
        return answer

    def state(self, message: dict[str, Any]) -> dict[str, Any]:
        """Send a request that answers with one state, and return that state."""
        return self.request(message)["state"]

    def states(self, message: dict[str, Any]) -> list[dict[str, Any]]:
        """Send a batch request and return the list of states."""
        return self.request(message)["states"]

    def _died(self, reason: object) -> EngineDied:
        code = self._proc.poll()
        self.close()
        detail = f" (exit status {code})" if code is not None else ""
        return EngineDied(f"the environment server stopped responding{detail}: {reason}")

    # ── Lifecycle ────────────────────────────────────────────────────────────

    @property
    def closed(self) -> bool:
        return self._closed

    def close(self) -> None:
        """Shut the child down. Idempotent, and safe to call after a failure."""
        if self._closed:
            return
        self._closed = True
        _LIVE.discard(self)

        if self._proc.poll() is None:
            try:
                self._proc.stdin.write(b'{"id":0,"cmd":"close"}\n')
                self._proc.stdin.flush()
            except (BrokenPipeError, OSError, ValueError):
                pass
        self._finalizer()  # runs _reap once, whatever happened above

        for stream in (self._proc.stdin, self._proc.stdout, self._proc.stderr):
            if stream is not None:
                try:
                    stream.close()
                except OSError:
                    pass

    def __enter__(self) -> "Engine":
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()


# Engines still running when the interpreter exits. A WeakSet so an engine that
# is simply garbage collected is finalized the usual way instead.
_LIVE: "weakref.WeakSet[Engine]" = weakref.WeakSet()


@atexit.register
def _close_all() -> None:
    for engine in list(_LIVE):
        engine.close()


def server_args(
    *,
    frames: int | None = None,
    k_food: int | None = None,
    k_cells: int | None = None,
    k_virus: int | None = None,
    k_ejecta: int | None = None,
    view_scale: float | None = None,
    n_food: int | None = None,
    n_bots: int | None = None,
    n_viruses: int | None = None,
) -> list[str]:
    """Build the command line for :class:`Engine` from environment options.

    Anything left as None keeps the server's own default, which the handshake
    then reports back, so there is exactly one source of truth for each knob.
    """
    args: list[str] = []
    for flag, value in (
        ("-frames", frames),
        ("-k-food", k_food),
        ("-k-cells", k_cells),
        ("-k-virus", k_virus),
        ("-k-ejecta", k_ejecta),
        ("-view-scale", view_scale),
        ("-food", n_food),
        ("-bots", n_bots),
        ("-viruses", n_viruses),
    ):
        if value is not None:
            args += [flag, str(value)]
    return args
