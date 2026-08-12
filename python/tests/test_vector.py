# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""The batched environment, checked against stepping one world at a time."""

from __future__ import annotations

import gymnasium
import numpy as np
import pytest

from agario_gym import AgarioEnv, AgarioVectorEnv

SMALL = dict(n_food=60, n_bots=2, n_viruses=2)
N = 3


@pytest.fixture
def envs():
    e = AgarioVectorEnv(N, **SMALL)
    yield e
    e.close()


def test_spaces_are_batched(envs):
    assert envs.num_envs == N
    assert envs.action_space.shape == (N, 2)
    assert envs.observation_space.shape[0] == N
    assert envs.single_observation_space.shape == envs.observation_space.shape[1:]


def test_reset_and_step_shapes(envs):
    obs, info = envs.reset(seed=0)
    assert obs.shape == envs.observation_space.shape
    assert envs.observation_space.contains(obs)

    actions = np.zeros((N, 2), dtype=np.int64)
    obs, rewards, terminated, truncated, info = envs.step(actions)
    assert obs.shape == envs.observation_space.shape
    assert rewards.shape == (N,)
    assert terminated.shape == (N,)
    assert truncated.shape == (N,)
    assert info["mass"].shape == (N,)


def test_it_matches_stepping_one_world_at_a_time():
    """The whole point of the batch commands: identical results, fewer round trips.

    The seeds are set explicitly on both sides rather than relying on the two
    implementations drawing the same numbers, which is a property of the client
    and not of the protocol under test here.
    """
    seeds = [11, 22, 33]
    actions = [[(i * 3 + j) % 16, 0] for i, j in zip(range(60), range(60))]

    batched = AgarioVectorEnv(N, **SMALL)
    try:
        batched.reset(seed=0)
        # Pin each world explicitly by resetting through the engine.
        batched._states = batched.engine.states(
            {"cmd": "reset_batch", "env_ids": list(range(N)), "seeds": seeds}
        )
        batched._autoreset[:] = False
        rows = []
        for a in actions:
            obs, rewards, terminated, _t, _info = batched.step(np.array([a] * N))
            rows.append((obs.copy(), rewards.copy()))
    finally:
        batched.close()

    singles = []
    for i, seed in enumerate(seeds):
        e = AgarioEnv(**SMALL)
        try:
            e.reset(seed=0, options={"seed": seed})
            frames = []
            for a in actions:
                obs, r, _term, _t, _info = e.step(np.array(a))
                frames.append((obs.copy(), r))
            singles.append(frames)
        finally:
            e.close()

    for t, (obs, rewards) in enumerate(rows):
        for i in range(N):
            np.testing.assert_allclose(
                obs[i], singles[i][t][0], err_msg=f"observation differs at step {t}, env {i}"
            )
            assert rewards[i] == pytest.approx(
                singles[i][t][1]
            ), f"reward differs at step {t}, env {i}"


def test_worlds_do_not_interfere(envs):
    """Distinct seeds must give distinct worlds inside one process."""
    obs, _info = envs.reset(seed=1234)
    for i in range(N):
        for j in range(i + 1, N):
            assert not np.array_equal(obs[i], obs[j]), f"worlds {i} and {j} are identical"


def test_the_same_seed_reproduces_the_batch():
    def run():
        e = AgarioVectorEnv(N, **SMALL)
        try:
            obs, _info = e.reset(seed=5)
            frames = [obs.copy()]
            for i in range(40):
                obs, *_ = e.step(np.array([[i % 16, 0]] * N))
                frames.append(obs.copy())
            return np.concatenate(frames)
        finally:
            e.close()

    np.testing.assert_array_equal(run(), run())


def test_autoreset_revives_a_dead_world(envs):
    """A terminated slot is reset on the following step, NEXT_STEP style."""
    envs.reset(seed=2)

    # Kill world 0 outright through the engine, the way being eaten would.
    for _ in range(4000):
        obs, rewards, terminated, _t, _info = envs.step(
            np.array([[7, 0]] * N)
        )
        if terminated.any():
            break
    else:
        pytest.skip("no world died within the step budget")

    which = int(np.argmax(terminated))
    # The step after termination resets that slot and reports nothing for it.
    obs, rewards, terminated2, _t, info = envs.step(np.array([[7, 0]] * N))
    assert not terminated2[which], "a slot stayed terminated instead of autoresetting"
    assert rewards[which] == 0.0, "an autoreset slot was paid a reward"
    assert info["mass"][which] > 0, "the revived world has no agent"


def test_truncation_ends_an_episode_that_never_dies():
    """There is no TimeLimit wrapper for vector envs, so the budget lives in the
    class. Without it a surviving agent would run forever."""
    budget = 12
    envs = AgarioVectorEnv(N, max_episode_steps=budget, **SMALL)
    try:
        envs.reset(seed=4)
        for step in range(1, budget + 1):
            _obs, _r, terminated, truncated, _info = envs.step(np.array([[3, 0]] * N))
            if terminated.any():
                pytest.skip("a world died before the budget ran out")
        assert truncated.all(), f"nothing truncated at the budget of {budget} steps"
        assert not terminated.any(), "truncation was reported as termination"

        # And the next step revives them, exactly as termination does.
        _obs, _r, terminated, truncated, info = envs.step(np.array([[3, 0]] * N))
        assert not truncated.any() and not terminated.any()
        assert (info["mass"] > 0).all()
    finally:
        envs.close()


def test_make_vec_builds_this_class():
    envs = gymnasium.make_vec("Agario-Small-v0", num_envs=2)
    try:
        assert isinstance(envs.unwrapped, AgarioVectorEnv)
        obs, _info = envs.reset(seed=0)
        assert obs.shape[0] == 2
    finally:
        envs.close()
