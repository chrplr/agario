# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Using the batched environment with stable-baselines3.

stable-baselines3 predates Gymnasium's vector API and carries its own
``VecEnv``, which it will not accept a :class:`gymnasium.vector.VectorEnv` in
place of. This adapter bridges the two so a training run keeps the one-process
batching instead of falling back to N separate environments.

There are two differences to reconcile:

* **Autoreset timing.** Gymnasium's NEXT_STEP convention revives a finished slot
  on the *following* step; stable-baselines3 expects the reset observation
  straight away, with the final one filed under ``terminal_observation``.
* **Ending an episode.** Gymnasium reports ``terminated`` and ``truncated``
  separately; stable-baselines3 wants one ``done`` plus a
  ``TimeLimit.truncated`` flag, which is what tells it to bootstrap the value
  function past a time limit rather than treat it as a real ending.

Import is deferred: stable-baselines3 is an optional dependency, so this module
is not imported by the package's ``__init__``.

    pip install -e "python[rl]"

    from agario_gym.sb3 import make_sb3_vec_env
    envs = make_sb3_vec_env(16, n_bots=4, n_food=200, max_episode_steps=1500)
    PPO("MlpPolicy", envs).learn(200_000)
"""

from __future__ import annotations

from typing import Any, Sequence

import numpy as np

from .vector_env import AgarioVectorEnv

__all__ = ["make_sb3_vec_env"]


def make_sb3_vec_env(num_envs: int = 8, **kwargs: Any):
    """Build a batched agario environment that stable-baselines3 accepts.

    Keyword arguments are passed to :class:`AgarioVectorEnv`, so ``n_bots``,
    ``n_food``, ``obs_mode``, ``reward_scheme`` and ``max_episode_steps`` all
    work as they do there.
    """
    from stable_baselines3.common.vec_env import VecEnv

    venv = AgarioVectorEnv(num_envs, **kwargs)

    class _Adapter(VecEnv):
        def __init__(self):
            super().__init__(
                num_envs=venv.num_envs,
                observation_space=venv.single_observation_space,
                action_space=venv.single_action_space,
            )
            self.venv = venv
            self._actions: np.ndarray | None = None
            self._seed: int | None = None
            # SB3 reads this off the env; the batched env draws nothing.
            self.render_mode = None

        # ── the parts that do the work ───────────────────────────────────────

        def reset(self) -> np.ndarray:
            obs, _info = self.venv.reset(seed=self._seed)
            self._seed = None  # seed once, as SB3 expects
            return obs

        def step_async(self, actions: np.ndarray) -> None:
            self._actions = actions

        def step_wait(self):
            obs, rewards, terminated, truncated, _info = self.venv.step(self._actions)
            dones = np.logical_or(terminated, truncated)
            infos: list[dict[str, Any]] = [{} for _ in range(self.num_envs)]

            if dones.any():
                # SB3 wants the *reset* observation here and the final one in
                # info, so the pending next-step autoresets are forced now.
                final = obs
                obs = self.venv.flush_autoreset()
                for i in np.nonzero(dones)[0]:
                    infos[i]["terminal_observation"] = final[i]
                    if truncated[i]:
                        # Without this SB3 treats a time limit as a real
                        # ending and stops bootstrapping the value there.
                        infos[i]["TimeLimit.truncated"] = True

            return obs, rewards, dones, infos

        def close(self) -> None:
            self.venv.close()

        def seed(self, seed: int | None = None) -> Sequence[int | None]:
            self._seed = seed
            return [seed] * self.num_envs

        # ── the parts SB3 requires but this env has no use for ───────────────

        def get_attr(self, attr_name: str, indices=None) -> list[Any]:
            return [getattr(self.venv, attr_name, None)] * self._n(indices)

        def set_attr(self, attr_name: str, value: Any, indices=None) -> None:
            setattr(self.venv, attr_name, value)

        def env_method(self, method_name: str, *args, indices=None, **kwargs) -> list[Any]:
            method = getattr(self.venv, method_name)
            return [method(*args, **kwargs)] * self._n(indices)

        def env_is_wrapped(self, wrapper_class, indices=None) -> list[bool]:
            return [False] * self._n(indices)

        def get_images(self):
            return [None] * self.num_envs

        def _n(self, indices) -> int:
            if indices is None:
                return self.num_envs
            if isinstance(indices, int):
                return 1
            return len(list(indices))

    return _Adapter()
