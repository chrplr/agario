# Copyright (c) 2026 Christophe Pallier
# SPDX-License-Identifier: Apache-2.0

"""Turning a wire state into an observation.

The tensor an agent sees is built here, in numpy, so that trying a different
encoding costs an edit rather than a rebuild of the Go binary. The server sends
geometry — positions relative to the agent, masses, and a threat/prey tag — and
never a tensor.
"""

from __future__ import annotations

from typing import Any

import numpy as np
from gymnasium import spaces

__all__ = ["OBS_MODES", "space_for", "encode", "RASTER"]

OBS_MODES = ("vector", "raster")

# Side of the raster grid, and its channel layout.
RASTER = 48
CH_FOOD, CH_SELF, CH_BIGGER, CH_SMALLER, CH_VIRUS = range(5)
N_CHANNELS = 5

# Masses are compressed with log1p before scaling: agario spans three orders of
# magnitude, and a linear scale spends almost all of its range on cells the
# agent will never meet.
_MASS_SCALE = 10.0

# Scalar features that precede the per-entity blocks in the vector encoding:
# mass, cell count, can_split, can_eject, four wall distances, rank, mass share.
N_SCALARS = 10


def _mass(m: float) -> float:
    return float(np.log1p(max(m, 0.0)) / _MASS_SCALE)


def space_for(mode: str, meta: dict[str, Any]) -> spaces.Space:
    """The observation space implied by a handshake."""
    if mode == "vector":
        n = (
            N_SCALARS
            + 4 * meta["max_blobs"]
            + 3 * meta["k_food"]
            + 4 * meta["k_cells"]
            + 3 * meta["k_virus"]
            + 3 * meta["k_ejecta"]
        )
        return spaces.Box(-1.0, 1.0, shape=(n,), dtype=np.float32)
    if mode == "raster":
        return spaces.Box(0.0, 1.0, shape=(N_CHANNELS, RASTER, RASTER), dtype=np.float32)
    raise ValueError(f"unknown obs_mode {mode!r}, expected one of {OBS_MODES}")


def encode(state: dict[str, Any], mode: str, meta: dict[str, Any]) -> np.ndarray:
    """Build the observation for one server state."""
    if mode == "vector":
        return _vector(state, meta)
    if mode == "raster":
        return _raster(state, meta)
    raise ValueError(f"unknown obs_mode {mode!r}, expected one of {OBS_MODES}")


def _vector(state: dict[str, Any], meta: dict[str, Any]) -> np.ndarray:
    """A flat egocentric feature vector, clipped to [-1, 1].

    Positions are divided by the view radius the server used, so the encoding is
    scale invariant: a small cell and a large one see their surroundings on the
    same numeric scale, which is what lets one policy handle both.
    """
    view = max(float(state["view_r"]), 1.0)
    parts: list[np.ndarray] = []

    n_owners = max(int(state.get("n_owners") or 1), 1)
    top = max(float(state.get("top_mass") or 0.0), 1.0)
    parts.append(
        np.array(
            [
                _mass(state["mass"]),
                state["self_count"] / max(meta["max_blobs"], 1),
                float(state["can_split"]),
                float(state["can_eject"]),
                state["wall_left"],
                state["wall_right"],
                state["wall_up"],
                state["wall_down"],
                # Rank as a fraction: 0 is leading, 1 is last.
                (state["rank"] - 1) / n_owners if state["rank"] else 1.0,
                # Share of the leader's mass, which is what "am I winning" means
                # in a game with no score.
                min(float(state["mass"]) / top, 1.0),
            ],
            dtype=np.float32,
        )
    )

    # No masking of the padding rows: the server pads with exact zeros and every
    # transform here maps 0 to 0, so the arithmetic is already correct on them.
    # Skipping the mask avoids five boolean-index allocations per observation,
    # which measured as most of the cost of this function.
    def rel(rows: list[list[float]], width: int) -> np.ndarray:
        a = np.asarray(rows, dtype=np.float32)
        a[:, :2] /= view
        a[:, 2] = np.log1p(a[:, 2]) / _MASS_SCALE
        return a.reshape(-1) if a.shape[1] == width else a[:, :width].reshape(-1)

    # Own cells carry a merge countdown rather than a relation, normalised by a
    # minute — long enough to cover the 30 s + mass cooldown.
    a = np.asarray(state["self"][: meta["max_blobs"]], dtype=np.float32)
    a[:, :2] /= view
    a[:, 2] = np.log1p(a[:, 2]) / _MASS_SCALE
    a[:, 3] = np.minimum(a[:, 3] / 60.0, 1.0)
    parts.append(a.reshape(-1))

    parts.append(rel(state["food"], 3))
    parts.append(rel(state["cells"], 4))
    parts.append(rel(state["viruses"], 3))
    parts.append(rel(state["ejecta"], 3))

    return np.clip(np.concatenate(parts), -1.0, 1.0).astype(np.float32)


def _raster(state: dict[str, Any], meta: dict[str, Any]) -> np.ndarray:
    """A multi-channel local view, centred on the agent.

    Entities are splatted as single cells rather than drawn to scale: the mass
    already occupies the value, and a disc rasteriser would cost far more per
    step than the information is worth at this resolution.
    """
    out = np.zeros((N_CHANNELS, RASTER, RASTER), dtype=np.float32)
    view = max(float(state["view_r"]), 1.0)
    half = RASTER / 2.0

    def plot(channel: int, dx: float, dy: float, value: float) -> None:
        # World coordinates run y-down, matching the renderer, so the raster
        # reads the same way round as the game does on screen.
        gx = int(half + dx / view * half)
        gy = int(half + dy / view * half)
        if 0 <= gx < RASTER and 0 <= gy < RASTER:
            out[channel, gy, gx] = max(out[channel, gy, gx], value)

    for row in state["food"]:
        if row[2] > 0:
            plot(CH_FOOD, row[0], row[1], 1.0)
    for row in state["ejecta"]:
        if row[2] > 0:
            plot(CH_FOOD, row[0], row[1], 1.0)
    for row in state["self"][: meta["max_blobs"]]:
        if row[2] > 0:
            plot(CH_SELF, row[0], row[1], min(_mass(row[2]), 1.0))
    for row in state["viruses"]:
        if row[2] > 0:
            plot(CH_VIRUS, row[0], row[1], 1.0)
    for row in state["cells"]:
        if row[2] <= 0:
            continue
        # The relation the server computed with the game's own eat rule, so the
        # agent never has to rediscover the 1.25 mass ratio from raw masses.
        channel = CH_BIGGER if row[3] < 0 else CH_SMALLER
        plot(channel, row[0], row[1], min(_mass(row[2]), 1.0))

    return out
