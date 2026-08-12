# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Many worlds in one server process.

The server addresses worlds by ``env_id`` and accepts batched reset and step
commands, so N environments cost one child process and one round trip per step
instead of N of each. Behaviour matches :class:`~gymnasium.vector.SyncVectorEnv`
over :class:`~agario_gym.env.AgarioEnv`, which the test suite asserts step for
step.
"""

from __future__ import annotations

from typing import Any, Sequence

import numpy as np
from gymnasium import spaces
from gymnasium.utils import seeding
from gymnasium.vector import AutoresetMode, VectorEnv
from gymnasium.vector.utils import batch_space

from . import obs as _obs
from .binary import find_binary
from .engine import Engine, server_args
from .env import REWARD_SCHEMES, compute_reward

__all__ = ["AgarioVectorEnv"]


class AgarioVectorEnv(VectorEnv):
    """A batched agario environment backed by one ``agario-env`` process."""

    metadata = {"render_modes": [], "autoreset_mode": AutoresetMode.NEXT_STEP}

    def __init__(
        self,
        num_envs: int = 1,
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
        max_episode_steps: int | None = None,
        binary: str | None = None,
    ):
        if obs_mode not in _obs.OBS_MODES:
            raise ValueError(f"unknown obs_mode {obs_mode!r}, expected one of {_obs.OBS_MODES}")
        if reward_scheme not in REWARD_SCHEMES:
            raise ValueError(
                f"unknown reward_scheme {reward_scheme!r}, expected one of {REWARD_SCHEMES}"
            )

        self.num_envs = num_envs
        self.obs_mode = obs_mode
        self.reward_scheme = reward_scheme
        self.kill_bonus = kill_bonus
        self.death_penalty = death_penalty
        self.step_cost = step_cost
        # There is no TimeLimit wrapper for vector environments, so the budget
        # is applied here. agario never ends on its own, so without one a
        # batched episode would run until the process was killed.
        self.max_episode_steps = max_episode_steps

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

        self.single_action_space = spaces.MultiDiscrete([meta["headings"], meta["triggers"]])
        self.single_observation_space = _obs.space_for(obs_mode, meta)
        self.action_space = batch_space(self.single_action_space, num_envs)
        self.observation_space = batch_space(self.single_observation_space, num_envs)

        self._env_ids = list(range(num_envs))
        # One generator per world, seeded the way SyncVectorEnv seeds its
        # sub-environments (seed + i), so the two agree episode for episode.
        self._rngs: list[np.random.Generator] = []
        self._states: list[dict[str, Any]] = []
        # Autoreset follows the NEXT_STEP convention: the step after a terminal
        # one resets that slot and returns its first observation, rather than
        # resetting eagerly and hiding the terminal observation.
        self._autoreset = np.zeros(num_envs, dtype=bool)
        self._elapsed = np.zeros(num_envs, dtype=np.int64)
        self._config = None

    # ── Gymnasium vector API ─────────────────────────────────────────────────

    def reset(self, *, seed: int | Sequence[int] | None = None, options: dict | None = None):
        options = options or {}
        self._config = options.get("config")

        if seed is None:
            seeds: list[int | None] = [None] * self.num_envs
        elif isinstance(seed, int):
            seeds = [seed + i for i in range(self.num_envs)]
        else:
            if len(seed) != self.num_envs:
                raise ValueError(f"expected {self.num_envs} seeds, got {len(seed)}")
            seeds = list(seed)

        for i, s in enumerate(seeds):
            if s is not None or len(self._rngs) <= i:
                rng, _ = seeding.np_random(s)
                if len(self._rngs) <= i:
                    self._rngs.append(rng)
                else:
                    self._rngs[i] = rng

        request: dict[str, Any] = {
            "cmd": "reset_batch",
            "env_ids": self._env_ids,
            "seeds": [self._draw(i) for i in range(self.num_envs)],
        }
        if self._config is not None:
            request["config"] = self._config

        self._states = self.engine.states(request)
        self._autoreset[:] = False
        self._elapsed[:] = 0
        return self._observations(), self._infos()

    def step(self, actions):
        actions = np.asarray(actions).reshape(self.num_envs, 2)

        # Slots that ended last step are reset now rather than stepped.
        self._reset_slots([i for i in range(self.num_envs) if self._autoreset[i]])

        stepping = [i for i in range(self.num_envs) if not self._autoreset[i]]
        rewards = np.zeros(self.num_envs, dtype=np.float64)
        terminated = np.zeros(self.num_envs, dtype=bool)

        if stepping:
            previous = [self._states[i] for i in stepping]
            new = self.engine.states(
                {
                    "cmd": "step_batch",
                    "env_ids": [self._env_ids[i] for i in stepping],
                    "actions": [[int(actions[i][0]), int(actions[i][1])] for i in stepping],
                }
            )
            for i, before, after in zip(stepping, previous, new):
                self._states[i] = after
                rewards[i] = compute_reward(
                    before,
                    after,
                    scheme=self.reward_scheme,
                    kill_bonus=self.kill_bonus,
                    death_penalty=self.death_penalty,
                    step_cost=self.step_cost,
                )
                terminated[i] = bool(after["dead"])
                self._elapsed[i] += 1

        truncated = np.zeros(self.num_envs, dtype=bool)
        if self.max_episode_steps is not None:
            # Only a slot that is still alive can be truncated; a slot that died
            # on this very step is terminated, not truncated.
            truncated = (self._elapsed >= self.max_episode_steps) & ~terminated

        # A slot that just autoreset reports neither reward nor an end flag: its
        # step was spent being reborn.
        self._autoreset = terminated | truncated
        return self._observations(), rewards, terminated, truncated, self._infos()

    def close(self, **kwargs):
        engine = getattr(self, "engine", None)
        if engine is not None:
            engine.close()

    def __enter__(self) -> "AgarioVectorEnv":
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()

    def __del__(self):
        try:
            self.close()
        except Exception:
            pass

    # ── Helpers ──────────────────────────────────────────────────────────────

    def _reset_slots(self, idx: list[int]) -> None:
        """Give the listed slots fresh worlds, in one round trip."""
        if not idx:
            return
        request: dict[str, Any] = {
            "cmd": "reset_batch",
            "env_ids": [self._env_ids[i] for i in idx],
            "seeds": [self._draw(i) for i in idx],
        }
        if self._config is not None:
            request["config"] = self._config
        for i, state in zip(idx, self.engine.states(request)):
            self._states[i] = state
            self._elapsed[i] = 0

    def flush_autoreset(self) -> np.ndarray:
        """Perform any pending autoresets now and return fresh observations.

        This class follows Gymnasium's NEXT_STEP convention, where a slot that
        ended is reborn on the following step. Adapters for libraries that
        expect same-step autoreset — stable-baselines3 among them — call this
        immediately after a step to bring the two conventions into line.
        """
        idx = np.nonzero(self._autoreset)[0].tolist()
        if idx:
            self._reset_slots(idx)
            self._autoreset[:] = False
        return self._observations()

    def _draw(self, i: int) -> int:
        """The world seed for slot i, from that slot's own generator."""
        while len(self._rngs) <= i:
            rng, _ = seeding.np_random(None)
            self._rngs.append(rng)
        return int(self._rngs[i].integers(0, 2**63 - 1))

    def _observations(self) -> np.ndarray:
        meta = self.engine.meta
        return np.stack([_obs.encode(s, self.obs_mode, meta) for s in self._states])

    def _infos(self) -> dict[str, Any]:
        # Vector infos are dicts of arrays, not arrays of dicts.
        return {
            "mass": np.array([s["mass"] for s in self._states], dtype=np.float64),
            "cells": np.array([s["self_count"] for s in self._states], dtype=np.int64),
            "rank": np.array([s["rank"] for s in self._states], dtype=np.int64),
            "kills": np.array([s["kills"] for s in self._states], dtype=np.int64),
            "time": np.array([s["time"] for s in self._states], dtype=np.float64),
        }
