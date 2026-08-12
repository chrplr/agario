# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""The Gymnasium environment: conformance, spaces, rewards, termination."""

from __future__ import annotations

import warnings

import gymnasium
import numpy as np
import pytest
from gymnasium.utils.env_checker import check_env

from agario_gym import OBS_MODES, REWARD_SCHEMES, AgarioEnv, compute_reward

# Small worlds keep the suite quick; nothing here depends on the population.
SMALL = dict(n_food=60, n_bots=2, n_viruses=2)


@pytest.fixture
def env():
    e = AgarioEnv(**SMALL)
    yield e
    e.close()


@pytest.mark.parametrize("mode", OBS_MODES)
def test_passes_the_gymnasium_checker(mode):
    with AgarioEnv(obs_mode=mode, **SMALL) as e:
        with warnings.catch_warnings():
            warnings.simplefilter("error", UserWarning)
            check_env(e, skip_render_check=True)


@pytest.mark.parametrize("mode", OBS_MODES)
def test_observations_stay_inside_their_space(mode):
    with AgarioEnv(obs_mode=mode, **SMALL) as e:
        obs, _info = e.reset(seed=0)
        assert e.observation_space.contains(obs), "reset observation outside the space"
        for _ in range(60):
            obs, _r, terminated, _t, _info = e.step(e.action_space.sample())
            assert e.observation_space.contains(obs), "step observation outside the space"
            if terminated:
                break


def test_the_registered_ids_all_build():
    for env_id in ("Agario-v0", "Agario-Small-v0", "Agario-NoVirus-v0"):
        e = gymnasium.make(env_id, **SMALL)
        try:
            obs, info = e.reset(seed=0)
            assert e.observation_space.contains(obs)
        finally:
            e.close()


def test_the_ids_carry_a_step_budget():
    """agario never ends on its own, so a missing TimeLimit is a hang."""
    e = gymnasium.make("Agario-Small-v0", **SMALL)
    try:
        assert e.spec.max_episode_steps is not None
    finally:
        e.close()


def test_action_space_matches_the_handshake(env):
    meta = env.engine.meta
    assert list(env.action_space.nvec) == [meta["headings"], meta["triggers"]]


def test_headings_actually_steer(env):
    """Opposite headings must move the agent in opposite directions."""

    def drift(heading):
        env.reset(seed=7)
        before = (env._state["cx"], env._state["cy"])
        for _ in range(30):
            env.step(np.array([heading, 0]))
        after = (env._state["cx"], env._state["cy"])
        return after[0] - before[0], after[1] - before[1]

    east = drift(0)
    west = drift(8)
    assert east[0] > 50, f"heading 0 did not move east: {east}"
    assert west[0] < -50, f"heading 8 did not move west: {west}"


def test_the_trigger_mask_agrees_with_what_triggers_do(env):
    _obs, info = env.reset(seed=3)
    # A starting cell is under both thresholds.
    assert info["can_split"] is False
    assert info["can_eject"] is False
    assert list(info["action_mask"]) == [True, False, False]

    before = info["cells"]
    _obs, _r, _term, _trunc, info = env.step(np.array([0, 1]))
    assert info["cells"] == before, "split below the minimum changed the cell count"


def test_splitting_works_once_big_enough(env):
    """The whole path: grow by eating, then split, all through the protocol."""
    from agario_gym import greedy_action

    env.reset(seed=3)
    for _ in range(1500):
        if env._state["can_split"]:
            break
        env.step(greedy_action(env._state, 16))
    else:
        pytest.skip("the greedy policy never reached the split threshold")

    before = env._state["self_count"]
    _obs, _r, _term, _trunc, info = env.step(np.array([0, 1]))
    assert info["cells"] > before, "split did not divide the cell"


@pytest.mark.parametrize("scheme", REWARD_SCHEMES)
def test_every_reward_scheme_runs(scheme):
    with AgarioEnv(reward_scheme=scheme, **SMALL) as e:
        e.reset(seed=1)
        total = 0.0
        for _ in range(80):
            _obs, r, terminated, _t, _info = e.step(e.action_space.sample())
            assert np.isfinite(r), f"{scheme} produced a non-finite reward"
            total += r
            if terminated:
                break


def test_mass_delta_rewards_growth_and_punishes_death():
    before = {"mass": 100.0, "dead": False, "kills": 0}
    grew = {"mass": 200.0, "dead": False, "kills": 0}
    shrank = {"mass": 50.0, "dead": False, "kills": 0}
    died = {"mass": 0.0, "dead": True, "kills": 0}

    kw = dict(scheme="mass_delta", kill_bonus=1.0, death_penalty=5.0, step_cost=0.01)
    assert compute_reward(before, grew, **kw) > 0
    assert compute_reward(before, shrank, **kw) < 0
    assert compute_reward(before, died, **kw) == pytest.approx(-5.0)

    # Doubling is worth the same whatever the scale — the reason for the log.
    small = compute_reward({"mass": 20.0, "dead": False}, {"mass": 40.0, "dead": False, "kills": 0}, **kw)
    large = compute_reward({"mass": 2000.0, "dead": False}, {"mass": 4000.0, "dead": False, "kills": 0}, **kw)
    assert small == pytest.approx(large)


def test_kills_add_a_bonus():
    kw = dict(scheme="mass_delta", kill_bonus=2.0, death_penalty=5.0, step_cost=0.01)
    before = {"mass": 100.0, "dead": False}
    plain = compute_reward(before, {"mass": 110.0, "dead": False, "kills": 0}, **kw)
    killed = compute_reward(before, {"mass": 110.0, "dead": False, "kills": 1}, **kw)
    assert killed == pytest.approx(plain + 2.0)


def test_termination_only_on_death(env):
    _obs, _info = env.reset(seed=11)
    for _ in range(200):
        _obs, _r, terminated, truncated, info = env.step(env.action_space.sample())
        # The bare env never truncates; that is TimeLimit's job.
        assert truncated is False
        if terminated:
            assert info["cells"] == 0
            break


def test_step_before_reset_raises():
    with AgarioEnv(**SMALL) as e:
        with pytest.raises(RuntimeError):
            e.step(np.array([0, 0]))


def test_render_ansi_summarises():
    with AgarioEnv(render_mode="ansi", **SMALL) as e:
        e.reset(seed=0)
        text = e.render()
        assert "mass=" in text and "rank=" in text
