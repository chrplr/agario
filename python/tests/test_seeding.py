# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Reproducibility across the language boundary.

This is the cross-language guarantee, and it needs its own tests because agario
differs from a puzzle game here: the world is *generated* randomly, and that
generation happens in Go. Python seeds itself, draws a concrete world seed and
sends it across. If that seed ever stopped being threaded through, every test in
the suite would still pass while every experiment became unreproducible.
"""

from __future__ import annotations

import numpy as np
import pytest

from agario_gym import AgarioEnv

SMALL = dict(n_food=60, n_bots=3, n_viruses=2)


def trajectory(seed, *, steps=120, env_seed=None):
    """Play a fixed action sequence and return everything observable."""
    with AgarioEnv(**SMALL) as e:
        obs, info = e.reset(seed=seed) if env_seed is None else e.reset(
            seed=seed, options={"seed": env_seed}
        )
        rng = np.random.default_rng(0)  # the actions are fixed, not seeded by `seed`
        out = [obs.copy()]
        rewards = []
        for _ in range(steps):
            action = np.array([rng.integers(16), 0])
            obs, r, terminated, _t, info = e.step(action)
            out.append(obs.copy())
            rewards.append(r)
            if terminated:
                break
        return np.concatenate(out), np.array(rewards), info["mass"]


def test_the_same_seed_gives_the_same_episode():
    a_obs, a_rew, a_mass = trajectory(1234)
    b_obs, b_rew, b_mass = trajectory(1234)

    assert a_obs.shape == b_obs.shape
    np.testing.assert_array_equal(a_obs, b_obs)
    np.testing.assert_array_equal(a_rew, b_rew)
    assert a_mass == b_mass


def test_different_seeds_diverge():
    """Guards against the seed being dropped somewhere between here and Go."""
    a_obs, _a, _am = trajectory(1234)
    b_obs, _b, _bm = trajectory(4321)
    assert not np.array_equal(a_obs, b_obs), "two seeds produced identical episodes"


def test_an_explicit_world_seed_pins_the_world():
    a_obs, _a, _am = trajectory(1, env_seed=99)
    b_obs, _b, _bm = trajectory(2, env_seed=99)
    # Different Gymnasium seeds, same explicit world seed: the world must match.
    np.testing.assert_array_equal(a_obs, b_obs)


def test_reset_without_a_seed_still_varies():
    """Two unseeded environments must not silently share a world."""
    with AgarioEnv(**SMALL) as a, AgarioEnv(**SMALL) as b:
        obs_a, _ = a.reset()
        obs_b, _ = b.reset()
        for _ in range(20):
            obs_a, *_ = a.step(np.array([1, 0]))
            obs_b, *_ = b.step(np.array([1, 0]))
        assert not np.array_equal(obs_a, obs_b), "unseeded resets produced the same world"


def test_reseeding_the_same_env_reproduces():
    """The guarantee has to hold within one process too, not just across them."""
    with AgarioEnv(**SMALL) as e:
        def run():
            obs, _info = e.reset(seed=7)
            frames = [obs.copy()]
            for i in range(40):
                obs, *_ = e.step(np.array([i % 16, 0]))
                frames.append(obs.copy())
            return np.concatenate(frames)

        np.testing.assert_array_equal(run(), run())


def test_the_seed_reaches_the_server_not_just_the_action_sampler():
    """A world seed alone must change the world even with identical actions.

    If Python only seeded its own action sampler and the server ignored the
    seed, this would pass by accident whenever the actions matched — so the
    actions are held fixed and only the world seed varies.
    """
    with AgarioEnv(**SMALL) as e:
        def first_obs(world_seed):
            obs, _info = e.reset(seed=0, options={"seed": world_seed})
            return obs.copy()

        assert not np.array_equal(first_obs(1), first_obs(2))
        np.testing.assert_array_equal(first_obs(5), first_obs(5))
