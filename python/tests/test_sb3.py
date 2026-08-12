# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""The stable-baselines3 adapter.

Skipped when stable-baselines3 is absent — it is an optional dependency.
"""

from __future__ import annotations

import numpy as np
import pytest

sb3 = pytest.importorskip("stable_baselines3")

from agario_gym.sb3 import make_sb3_vec_env  # noqa: E402

SMALL = dict(n_food=60, n_bots=2, n_viruses=2)
N = 3


@pytest.fixture
def envs():
    e = make_sb3_vec_env(N, **SMALL)
    yield e
    e.close()


def test_it_satisfies_the_sb3_vecenv_api(envs):
    from stable_baselines3.common.vec_env import VecEnv

    assert isinstance(envs, VecEnv)
    assert envs.num_envs == N
    obs = envs.reset()
    assert obs.shape == (N,) + envs.observation_space.shape


def test_step_returns_the_old_gym_four_tuple(envs):
    envs.reset()
    actions = np.zeros((N, 2), dtype=np.int64)
    obs, rewards, dones, infos = envs.step(actions)
    assert obs.shape == (N,) + envs.observation_space.shape
    assert rewards.shape == (N,)
    assert dones.shape == (N,)
    assert len(infos) == N
    assert all(isinstance(i, dict) for i in infos)


def test_autoreset_is_same_step_with_the_terminal_observation_in_info():
    """SB3 expects the reset observation immediately and the final one in info.

    This env resets on the *following* step, so the adapter has to force the
    pending reset. Getting it wrong silently trains on a stale observation.
    """
    envs = make_sb3_vec_env(N, max_episode_steps=10, **SMALL)
    try:
        envs.reset()
        actions = np.zeros((N, 2), dtype=np.int64)
        for _ in range(10):
            obs, _rewards, dones, infos = envs.step(actions)
            if dones.any():
                break
        assert dones.any(), "nothing ended within the budget"

        for i in np.nonzero(dones)[0]:
            assert "terminal_observation" in infos[i], "the final observation was dropped"
            # Truncation must be flagged, or SB3 stops bootstrapping the value
            # function past a time limit and learns that the clock is death.
            assert infos[i].get("TimeLimit.truncated") is True
            # The returned observation is the fresh one, not the terminal one.
            assert not np.array_equal(obs[i], infos[i]["terminal_observation"])
    finally:
        envs.close()


def test_a_short_ppo_run_completes():
    """Not a quality check — just that the whole stack runs end to end."""
    from stable_baselines3 import PPO

    envs = make_sb3_vec_env(2, max_episode_steps=50, **SMALL)
    try:
        model = PPO("MlpPolicy", envs, n_steps=32, batch_size=32, seed=0, verbose=0)
        model.learn(total_timesteps=128)
        obs = envs.reset()
        action, _ = model.predict(obs, deterministic=True)
        assert action.shape == (2, 2)
    finally:
        envs.close()
