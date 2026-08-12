#!/usr/bin/env python3
# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Baselines: what a random policy and a simple heuristic each achieve.

Run this before training anything. The greedy row is the bar a learned policy
has to clear to be worth the electricity; the random row is the floor.

    python python/examples/random_agent.py --episodes 5
"""

from __future__ import annotations

import argparse
import statistics

import gymnasium

import agario_gym  # noqa: F401  (registers the ids)
from agario_gym import run_greedy, run_random


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", default="Agario-Small-v0", help="environment id")
    parser.add_argument("--episodes", type=int, default=5)
    parser.add_argument("--steps", type=int, default=900, help="max steps per episode")
    parser.add_argument("--seed", type=int, default=0)
    args = parser.parse_args()

    print(f"{args.env}, {args.episodes} episodes of up to {args.steps} steps\n")
    print(f"{'policy':<8}{'final mass':>12}{'best mass':>11}{'survived':>10}{'reward':>10}")
    print("-" * 51)

    results = {}
    for name, run in (("random", run_random), ("greedy", run_greedy)):
        finals, bests, times, rewards = [], [], [], []
        for i in range(args.episodes):
            env = gymnasium.make(args.env).unwrapped
            try:
                r = run(env, seed=args.seed + i, max_steps=args.steps)
            finally:
                env.close()
            finals.append(r["final_mass"])
            bests.append(r["best_mass"])
            times.append(r["time"])
            rewards.append(r["reward"])
        results[name] = statistics.mean(bests)
        print(
            f"{name:<8}{statistics.mean(finals):>12.1f}{statistics.mean(bests):>11.1f}"
            f"{statistics.mean(times):>9.1f}s{statistics.mean(rewards):>10.2f}"
        )

    print()
    if results["greedy"] > results["random"]:
        print(
            f"greedy reaches {results['greedy'] / max(results['random'], 1e-9):.1f}x "
            "the mass of random, as it should — the observation encoding and the "
            "action mapping are wired up correctly."
        )
    else:
        print(
            "greedy did NOT beat random, which means something is wrong: most "
            "likely the heading mapping or the relative coordinates in the state."
        )


if __name__ == "__main__":
    main()
