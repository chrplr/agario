# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Reference policies, for reading a learned agent's numbers against something.

``run_random`` is the floor. ``run_greedy`` is the bar a learned policy has to
clear to be worth anything: it does the obvious thing — run from what can eat
you, otherwise eat the nearest thing you can — without any learning at all.
"""

from __future__ import annotations

import math
from typing import Any

import numpy as np

__all__ = ["run_random", "run_greedy", "greedy_action"]


def _episode(env, policy, *, seed: int | None, max_steps: int) -> dict[str, Any]:
    obs, info = env.reset(seed=seed)
    best = float(info["mass"])
    total = 0.0
    steps = 0

    for steps in range(1, max_steps + 1):
        action = policy(env, info)
        obs, reward, terminated, truncated, info = env.step(action)
        total += float(reward)
        best = max(best, float(info["mass"]))
        if terminated or truncated:
            break

    return {
        "steps": steps,
        "final_mass": float(info["mass"]),
        "best_mass": best,
        "reward": total,
        "survived": not bool(info.get("cells", 0) == 0),
        "time": float(info["time"]),
    }


def run_random(env, *, seed: int | None = None, max_steps: int = 1000) -> dict[str, Any]:
    """Uniformly random actions."""
    rng = np.random.default_rng(seed)

    def policy(env, _info):
        return np.array(
            [rng.integers(env.action_space.nvec[0]), rng.integers(env.action_space.nvec[1])]
        )

    return _episode(env, policy, seed=seed, max_steps=max_steps)


def greedy_action(state: dict[str, Any], headings: int) -> np.ndarray:
    """Flee the nearest threat, else chase the nearest prey, else the nearest food.

    Deliberately simple, and deliberately never splitting: splitting is the part
    of agario that needs judgement, so leaving it out keeps this an honest floor
    for a learned policy rather than a hand-tuned competitor.
    """
    threat = _nearest(state["cells"], lambda row: row[3] < 0)
    if threat is not None:
        angle = math.atan2(-threat[1], -threat[0])  # directly away
        return np.array([_bucket(angle, headings), 0])

    prey = _nearest(state["cells"], lambda row: row[3] > 0)
    target = prey if prey is not None else _nearest(state["food"], lambda row: True)
    if target is None:
        return np.array([0, 0])
    return np.array([_bucket(math.atan2(target[1], target[0]), headings), 0])


def _nearest(rows, predicate):
    """The first row passing predicate. Rows arrive sorted by distance already."""
    for row in rows:
        if row[2] > 0 and predicate(row):
            return row
    return None


def _bucket(angle: float, headings: int) -> int:
    """Round an angle in radians to the nearest compass heading."""
    return int(round(angle / (2 * math.pi) * headings)) % headings


def run_greedy(env, *, seed: int | None = None, max_steps: int = 1000) -> dict[str, Any]:
    """The heuristic above, using the raw state rather than the observation."""

    def policy(env, _info):
        return greedy_action(env.unwrapped._state, int(env.action_space.nvec[0]))

    return _episode(env, policy, seed=seed, max_steps=max_steps)
