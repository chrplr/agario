# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Finding the ``agario-env`` binary that serves the simulation.

The Go side is the single source of truth for the rules, so the Python package
is useless without it. It is looked for in the places it plausibly is, and built
from source as a last resort when the checkout is at hand.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

__all__ = ["find_binary", "BinaryNotFound"]

_EXE = ".exe" if sys.platform == "win32" else ""
_BINARY_NAME = "agario-env" + _EXE
_GO_PACKAGE = "./cmd/agario-env"
_MODULE_LINE = "module agario"
_ENV_VAR = "AGARIO_ENV_BIN"


class BinaryNotFound(RuntimeError):
    """The environment server could not be located or built."""


def find_binary(explicit: str | os.PathLike[str] | None = None) -> str:
    """Return the path to the environment server.

    Tried in order: an explicit path, ``$AGARIO_ENV_BIN``, the ``PATH``, the
    repository checkout this package lives in, and finally a cached build from
    that checkout's sources.
    """
    if explicit is not None:
        path = Path(explicit)
        if not path.exists():
            raise BinaryNotFound(f"no such binary: {path}")
        return str(path)

    env = os.environ.get(_ENV_VAR)
    if env:
        if not Path(env).exists():
            raise BinaryNotFound(f"${_ENV_VAR} points at a missing file: {env}")
        return env

    on_path = shutil.which("agario-env")
    if on_path:
        return on_path

    repo = _find_repo()
    if repo is not None:
        built = repo / _BINARY_NAME
        if built.exists():
            return str(built)
        return _build(repo)

    raise BinaryNotFound(
        "cannot find the agario-env binary.\n"
        "Build it from the agario checkout:\n"
        f"    go build -o {_BINARY_NAME} {_GO_PACKAGE}\n"
        f"then put it on your PATH or point ${_ENV_VAR} at it."
    )


def _find_repo() -> Path | None:
    """Walk up from this file looking for the agario module root."""
    for parent in Path(__file__).resolve().parents:
        go_mod = parent / "go.mod"
        if go_mod.is_file() and _MODULE_LINE in go_mod.read_text(encoding="utf-8"):
            return parent
    return None


def _cache_dir() -> Path:
    base = os.environ.get("XDG_CACHE_HOME") or (Path.home() / ".cache")
    return Path(base) / "agario-gym"


def _build(repo: Path) -> str:
    """Build the server into the user cache, reusing it while it is current.

    Rebuilding is keyed on the Go sources' modification times: during
    development the binary must not silently lag behind an edited protocol,
    which is the one failure mode that produces confusing errors much later.
    """
    if shutil.which("go") is None:
        raise BinaryNotFound(
            f"found the agario checkout at {repo} but no Go toolchain to build "
            f"{_BINARY_NAME} with. Install Go, or point ${_ENV_VAR} at a "
            "prebuilt binary."
        )

    out = _cache_dir() / _BINARY_NAME
    newest = max(
        (p.stat().st_mtime for p in repo.rglob("*.go") if ".git" not in p.parts),
        default=0.0,
    )
    if out.exists() and out.stat().st_mtime >= newest:
        return str(out)

    out.parent.mkdir(parents=True, exist_ok=True)
    result = subprocess.run(
        ["go", "build", "-o", str(out), _GO_PACKAGE],
        cwd=repo,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise BinaryNotFound(
            f"go build {_GO_PACKAGE} failed in {repo}:\n{result.stderr.strip()}"
        )
    return str(out)
