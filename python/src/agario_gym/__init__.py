# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""agario as a Gymnasium environment.

    import gymnasium, agario_gym
    env = gymnasium.make("Agario-Small-v0")
    obs, info = env.reset(seed=0)
    obs, reward, terminated, truncated, info = env.step(env.action_space.sample())

The rules live in Go, in the same package the playable game uses, and are served
by the ``agario-env`` binary over a JSON-lines protocol. This package is the
client: it owns the observation encoding, the reward and the episode budget.
"""

from __future__ import annotations

import gymnasium

from .baselines import greedy_action, run_greedy, run_random
from .binary import BinaryNotFound, find_binary
from .engine import (
    CommandFailed,
    Engine,
    EngineDied,
    EngineError,
    ProtocolError,
    server_args,
)
from .env import REWARD_SCHEMES, AgarioEnv, compute_reward
from .obs import OBS_MODES
from .vector_env import AgarioVectorEnv

__all__ = [
    "AgarioEnv",
    "AgarioVectorEnv",
    "BinaryNotFound",
    "CommandFailed",
    "Engine",
    "EngineDied",
    "EngineError",
    "OBS_MODES",
    "ProtocolError",
    "REWARD_SCHEMES",
    "compute_reward",
    "find_binary",
    "greedy_action",
    "register",
    "run_greedy",
    "run_random",
    "server_args",
]

# agario has no natural end — an agent that survives keeps playing — so every id
# carries a step budget. At the default frame skip a step is 1/30 s of game
# time, so 3000 steps is about a hundred seconds of play.
_SPECS = (
    dict(id="Agario-v0", max_episode_steps=3000, kwargs={}),
    dict(
        id="Agario-Small-v0",
        max_episode_steps=1500,
        kwargs={"n_food": 200, "n_bots": 4, "n_viruses": 4},
    ),
    dict(
        id="Agario-NoVirus-v0",
        max_episode_steps=3000,
        kwargs={"n_viruses": 0},
    ),
)


def register() -> None:
    """Register the environment ids. Called on import; safe to call again."""
    for spec in _SPECS:
        if spec["id"] in gymnasium.registry:
            continue
        gymnasium.register(
            entry_point="agario_gym.env:AgarioEnv",
            vector_entry_point="agario_gym.vector_env:AgarioVectorEnv",
            **spec,
        )


register()
