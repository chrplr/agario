# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""The transport, independent of anything to do with the game."""

from __future__ import annotations

import gc
import subprocess

import pytest

from agario_gym.binary import find_binary
from agario_gym.engine import CommandFailed, Engine, EngineDied


@pytest.fixture
def engine():
    e = Engine(find_binary(), ["-food", "40", "-bots", "2", "-viruses", "2"])
    yield e
    e.close()


def test_the_handshake_describes_the_spaces(engine):
    meta = engine.meta
    assert meta["protocol"] == 1
    assert meta["game"] == "agario"
    # The client sizes its spaces from these; a zero is a silent shape bug.
    for key in ("frames", "max_blobs", "headings", "triggers",
                "k_food", "k_cells", "k_virus", "k_ejecta"):
        assert meta[key] > 0, key
    # Flags must reach the handshake or the two sides disagree about the world.
    assert meta["food"] == 40
    assert meta["bots"] == 2


def test_requests_and_answers_stay_paired(engine):
    engine.request({"cmd": "reset", "env_id": 0, "seed": 1})
    for _ in range(5):
        answer = engine.request({"cmd": "step", "env_id": 0, "action": [0, 0]})
        assert answer["ok"]
        assert "state" in answer


def test_a_failed_command_leaves_the_engine_usable(engine):
    with pytest.raises(CommandFailed) as excinfo:
        engine.request({"cmd": "step", "env_id": 99, "action": [0, 0]})
    assert excinfo.value.kind == "not_reset"

    # The engine must still work: a rejected command is not a transport failure.
    assert engine.request({"cmd": "reset", "env_id": 0, "seed": 1})["ok"]


def test_unknown_commands_are_reported_not_fatal(engine):
    with pytest.raises(CommandFailed) as excinfo:
        engine.request({"cmd": "teleport"})
    assert excinfo.value.kind == "unknown_cmd"
    assert engine.request({"cmd": "hello"})["ok"]


def test_close_is_idempotent_and_reaps(engine):
    proc = engine._proc
    engine.close()
    engine.close()
    assert engine.closed
    assert proc.poll() is not None


def test_using_a_closed_engine_raises(engine):
    engine.close()
    with pytest.raises(EngineDied):
        engine.request({"cmd": "hello"})


def test_garbage_collection_reaps_the_child():
    e = Engine(find_binary(), ["-food", "20", "-bots", "1"])
    proc = e._proc
    del e
    gc.collect()
    proc.wait(timeout=10)
    assert proc.poll() is not None


def test_closing_stdin_is_enough_to_stop_the_server():
    """The backstop that prevents orphans when a client dies without saying so."""
    e = Engine(find_binary(), ["-food", "20", "-bots", "1"])
    proc = e._proc
    proc.stdin.close()
    proc.wait(timeout=10)
    assert proc.poll() is not None
    e._closed = True  # already gone; keep the finalizer quiet


def test_a_dead_child_raises_rather_than_hanging():
    e = Engine(find_binary(), ["-food", "20", "-bots", "1"])
    e._proc.kill()
    e._proc.wait()
    with pytest.raises(EngineDied):
        e.request({"cmd": "hello"})


def test_context_manager_closes():
    with Engine(find_binary(), ["-food", "20", "-bots", "1"]) as e:
        proc = e._proc
        assert e.request({"cmd": "hello"})["ok"]
    assert proc.poll() is not None


def test_the_binary_is_findable():
    path = find_binary()
    assert subprocess.run([path, "-version"], capture_output=True).returncode == 0
