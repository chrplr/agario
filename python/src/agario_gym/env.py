# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""agario as a Gymnasium environment.

The simulation is the same one the playable game runs; it is served by the
``agario-env`` binary and driven here over the JSON-lines protocol. The server
reports facts, and everything an agent designer wants to change — the reward,
the observation tensor, the episode budget — lives on this side.
"""

from __future__ import annotations

import math
from typing import Any

import gymnasium
import numpy as np
from gymnasium import spaces

from . import obs as _obs
from .binary import find_binary
from .engine import Engine, server_args

__all__ = ["AgarioEnv", "REWARD_SCHEMES", "compute_reward"]

REWARD_SCHEMES = ("mass_delta", "sparse", "survival")


def compute_reward(
    before: dict[str, Any],
    after: dict[str, Any],
    *,
    scheme: str,
    kill_bonus: float,
    death_penalty: float,
    step_cost: float,
) -> float:
    """Price one step.

    ``mass_delta`` pays the log ratio of masses rather than the difference: mass
    spans three orders of magnitude here, so a plain difference makes the first
    minute of an episode worth nothing next to the last and the agent never
    learns to open. A log ratio makes doubling worth the same at 20 mass and at
    2000.
    """
    died = bool(after["dead"]) and not bool(before["dead"])
    reward = -death_penalty if died else 0.0

    if scheme == "sparse":
        return reward

    if scheme == "survival":
        return reward + (0.0 if after["dead"] else step_cost)

    before_mass = max(float(before["mass"]), 1e-6)
    after_mass = max(float(after["mass"]), 1e-6)
    if not after["dead"]:
        reward += math.log(after_mass / before_mass)
    reward += kill_bonus * int(after.get("kills") or 0)
    return reward


class AgarioEnv(gymnasium.Env):
    """One agar.io cell against the game's own bots.

    Actions are ``[heading, trigger]``: a compass direction to steer in, and
    whether to split, eject mass, or do neither. Observations come in two
    encodings, chosen with ``obs_mode``; see :mod:`agario_gym.obs`.
    """

    metadata = {"render_modes": ["ansi"], "render_fps": 30}

    def __init__(
        self,
        *,
        obs_mode: str = "vector",
        reward_scheme: str = "mass_delta",
        kill_bonus: float = 1.0,
        death_penalty: float = 5.0,
        step_cost: float = 0.01,
        n_food: int | None = None,
        n_bots: int | None = None,
        n_viruses: int | None = None,
        frames: int | None = None,
        k_food: int | None = None,
        k_cells: int | None = None,
        k_virus: int | None = None,
        k_ejecta: int | None = None,
        view_scale: float | None = None,
        render_mode: str | None = None,
        binary: str | None = None,
    ):
        if obs_mode not in _obs.OBS_MODES:
            raise ValueError(f"unknown obs_mode {obs_mode!r}, expected one of {_obs.OBS_MODES}")
        if reward_scheme not in REWARD_SCHEMES:
            raise ValueError(
                f"unknown reward_scheme {reward_scheme!r}, expected one of {REWARD_SCHEMES}"
            )

        self.obs_mode = obs_mode
        self.reward_scheme = reward_scheme
        self.kill_bonus = kill_bonus
        self.death_penalty = death_penalty
        self.step_cost = step_cost
        self.render_mode = render_mode

        self.engine = Engine(
            find_binary(binary),
            server_args(
                frames=frames,
                k_food=k_food,
                k_cells=k_cells,
                k_virus=k_virus,
                k_ejecta=k_ejecta,
                view_scale=view_scale,
                n_food=n_food,
                n_bots=n_bots,
                n_viruses=n_viruses,
            ),
        )
        meta = self.engine.meta

        # The spaces come from the handshake rather than from constants
        # duplicated here, so the two sides cannot drift apart.
        self.action_space = spaces.MultiDiscrete([meta["headings"], meta["triggers"]])
        self.observation_space = _obs.space_for(obs_mode, meta)

        self._state: dict[str, Any] | None = None

    # ── Gymnasium API ────────────────────────────────────────────────────────

    def reset(self, *, seed: int | None = None, options: dict[str, Any] | None = None):
        super().reset(seed=seed)  # seeds self.np_random

        options = options or {}
        # Unlike a puzzle game, the world itself is generated randomly, and that
        # generation happens in Go. So a concrete seed is drawn here, from the
        # generator Gymnasium just seeded, and sent across: reproducibility
        # still belongs to Python, but the mechanism has to be explicit.
        world_seed = options.get("seed")
        if world_seed is None:
            world_seed = int(self.np_random.integers(0, 2**63 - 1))

        request: dict[str, Any] = {"cmd": "reset", "env_id": 0, "seed": int(world_seed)}
        if "config" in options:
            request["config"] = options["config"]

        self._state = self.engine.state(request)
        return self._observation(), self._info()

    def step(self, action):
        if self._state is None:
            raise RuntimeError("step() before reset()")

        heading, trigger = (int(action[0]), int(action[1]))
        previous = self._state
        self._state = self.engine.state(
            {"cmd": "step", "env_id": 0, "action": [heading, trigger]}
        )

        reward = compute_reward(
            previous,
            self._state,
            scheme=self.reward_scheme,
            kill_bonus=self.kill_bonus,
            death_penalty=self.death_penalty,
            step_cost=self.step_cost,
        )
        terminated = bool(self._state["dead"])
        # Truncation is a TimeLimit decision; this class keeps no step budget.
        # agario has no natural end, so the registered ids all set one.
        return self._observation(), reward, terminated, False, self._info()

    def render(self):
        if self.render_mode != "ansi" or self._state is None:
            return None
        return summarize(self._state)

    def close(self):
        engine = getattr(self, "engine", None)
        if engine is not None:
            engine.close()

    def __enter__(self) -> "AgarioEnv":
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()

    def __del__(self):
        try:
            self.close()
        except Exception:
            pass

    # ── Helpers ──────────────────────────────────────────────────────────────

    def _observation(self) -> np.ndarray:
        return _obs.encode(self._state, self.obs_mode, self.engine.meta)

    def _info(self) -> dict[str, Any]:
        s = self._state
        return {
            "mass": s["mass"],
            "cells": s["self_count"],
            "rank": s["rank"],
            "n_owners": s["n_owners"],
            "top_mass": s["top_mass"],
            "kills": s["kills"],
            "mass_gain": s["mass_gain"],
            "time": s["time"],
            "can_split": s["can_split"],
            "can_eject": s["can_eject"],
            # Which triggers are legal right now. Named to match the convention
            # masking policies expect, though the heading is always free.
            "action_mask": np.array(
                [True, bool(s["can_split"]), bool(s["can_eject"])], dtype=bool
            ),
            "is_success": False,
        }

    def action_masks(self) -> np.ndarray:
        """Legal triggers, for a masking policy. Every heading is always legal."""
        if self._state is None:
            raise RuntimeError("action_masks() before reset()")
        return self._info()["action_mask"]


def summarize(state: dict[str, Any]) -> str:
    """A one-line human-readable digest of a state, used by render()."""
    near_food = sum(1 for f in state["food"] if f[2] > 0)
    threats = sum(1 for c in state["cells"] if c[2] > 0 and c[3] < 0)
    prey = sum(1 for c in state["cells"] if c[2] > 0 and c[3] > 0)
    return (
        f"t={state['time']:6.1f}s  mass={state['mass']:8.1f}  cells={state['self_count']:2d}  "
        f"rank={state['rank']}/{state['n_owners']}  "
        f"food={near_food:2d}  threats={threats:2d}  prey={prey:2d}"
        + ("  DEAD" if state["dead"] else "")
    )
