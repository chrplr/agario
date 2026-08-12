#!/usr/bin/env python3
# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Train a PPO agent and compare it against the baselines.

    pip install -e "python[rl]"
    python python/examples/train_ppo.py --steps 200000

The point of the final table is the comparison, not the absolute number: a
policy that does not beat the greedy heuristic has not learned anything worth
having, however good its reward curve looked.
"""

from __future__ import annotations

import argparse
import statistics

import gymnasium
import numpy as np

import agario_gym  # noqa: F401  (registers the ids)
from agario_gym import run_greedy, run_random


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", default="Agario-Small-v0")
    parser.add_argument("--steps", type=int, default=200_000, help="total timesteps")
    parser.add_argument("--envs", type=int, default=8, help="parallel worlds")
    parser.add_argument("--seed", type=int, default=0)
    parser.add_argument("--eval-episodes", type=int, default=5)
    parser.add_argument("--save", default="", help="path to write the trained model to")
    # The reward balance is the first thing to reach for when a run learns to
    # survive without growing: one death costs death_penalty, while tripling
    # mass over a whole episode earns only log(3) ~ 1.1. Leave the penalty at 5
    # and the agent will rationally hide in a corner.
    parser.add_argument("--death-penalty", type=float, default=1.0)
    parser.add_argument("--kill-bonus", type=float, default=1.0)
    args = parser.parse_args()

    try:
        from stable_baselines3 import PPO
        from stable_baselines3.common.vec_env import VecMonitor
    except ImportError:
        raise SystemExit('stable-baselines3 is missing: pip install -e "python[rl]"')

    from agario_gym.sb3 import make_sb3_vec_env

    # One server process holds all the worlds, so this is far cheaper than
    # SubprocVecEnv: no per-env Python interpreter, one round trip per step.
    # stable-baselines3 will not take a Gymnasium VectorEnv directly, hence the
    # adapter — see agario_gym/sb3.py for what it reconciles.
    spec = gymnasium.spec(args.env)
    envs = VecMonitor(
        make_sb3_vec_env(
            args.envs,
            max_episode_steps=spec.max_episode_steps,
            death_penalty=args.death_penalty,
            kill_bonus=args.kill_bonus,
            **spec.kwargs,
        )
    )
    envs.seed(args.seed)

    model = PPO(
        "MlpPolicy",
        envs,
        seed=args.seed,
        n_steps=256,
        batch_size=256,
        learning_rate=3e-4,
        gamma=0.995,  # episodes are long; growth pays off slowly
        # An MLP this small is faster on the CPU than on a GPU: the per-batch
        # transfer costs more than the matrix multiplies save.
        device="cpu",
        verbose=1,
    )
    model.learn(total_timesteps=args.steps)
    envs.close()

    if args.save:
        model.save(args.save)
        print(f"saved to {args.save}")

    print("\nEvaluating against the baselines\n")
    print(f"{'policy':<8}{'final mass':>12}{'best mass':>11}{'survived':>10}")
    print("-" * 41)

    for name in ("random", "greedy", "ppo"):
        finals, bests, times = [], [], []
        for i in range(args.eval_episodes):
            env = gymnasium.make(args.env)
            try:
                if name == "random":
                    r = run_random(env.unwrapped, seed=args.seed + i)
                elif name == "greedy":
                    r = run_greedy(env.unwrapped, seed=args.seed + i)
                else:
                    r = _run_policy(env, model, seed=args.seed + i)
            finally:
                env.close()
            finals.append(r["final_mass"])
            bests.append(r["best_mass"])
            times.append(r["time"])
        print(
            f"{name:<8}{statistics.mean(finals):>12.1f}"
            f"{statistics.mean(bests):>11.1f}{statistics.mean(times):>9.1f}s"
        )


def _run_policy(env, model, *, seed: int) -> dict:
    obs, info = env.reset(seed=seed)
    best = float(info["mass"])
    while True:
        action, _ = model.predict(obs, deterministic=True)
        obs, _reward, terminated, truncated, info = env.step(np.asarray(action))
        best = max(best, float(info["mass"]))
        if terminated or truncated:
            return {
                "final_mass": float(info["mass"]),
                "best_mass": best,
                "time": float(info["time"]),
            }


if __name__ == "__main__":
    main()
