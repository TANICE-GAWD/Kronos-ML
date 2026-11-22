"""Vectorised port of the Kronos physics engine (backend/internal/engine/physics.go).

The Go engine integrates every packet at 60 FPS. Doing that for ~150k simulated
transfers in Python would be too slow, so this module uses the same orbit
equations and constants but solves each flight in closed form:

* planet position at time t: distance * (cos(t*speed + angle), 0, sin(t*speed + angle))
* the packet aims at where the destination will be (Go caps the lead at 10 s)
* the flight is a straight segment; its closest approach to the black hole
  decides the outcome: inside PULL_R it is destroyed, inside STALL_R it is
  slowed (dilated) and takes longer.
"""
from __future__ import annotations

import numpy as np

from .constants import BLACK_HOLE, PLANETS, PULL_R, SPEED_OF_LIGHT, STALL_R, TIME_DIL


def planet_position(names, t_seconds):
    """Positions (N, 3) of planets `names` at unix times `t_seconds`."""
    names = np.atleast_1d(np.asarray(names))
    t = np.atleast_1d(np.asarray(t_seconds, dtype=float))
    dist = np.array([PLANETS[str(n).lower()][0] for n in names])
    speed = np.array([PLANETS[str(n).lower()][1] for n in names])
    angle0 = np.array([PLANETS[str(n).lower()][2] for n in names])
    theta = np.mod(t * speed + angle0, 2 * np.pi)
    return np.stack([dist * np.cos(theta), np.zeros_like(theta), dist * np.sin(theta)], axis=1)


def _segment_min_distance(a, b, p):
    ab = b - a
    denom = np.maximum((ab * ab).sum(axis=1), 1e-9)
    u = np.clip(((p - a) * ab).sum(axis=1) / denom, 0.0, 1.0)
    closest = a + ab * u[:, None]
    return np.linalg.norm(closest - p, axis=1)


def simulate_flights(origins, destinations, t_seconds):
    """Return a dict of arrays: distance, travel_time_s, min_bh_dist, min_dilation, outcome.

    outcome is "settled" or "destroyed" (black hole), matching Packet.Status in Go.
    """
    origins = np.atleast_1d(np.asarray(origins))
    destinations = np.atleast_1d(np.asarray(destinations))
    t = np.atleast_1d(np.asarray(t_seconds, dtype=float))
    start = planet_position(origins, t)
    target_now = planet_position(destinations, t)
    # Iterative intercept, same idea as interceptTarget() in Go (lead capped at 10 s).
    lead = np.minimum(np.linalg.norm(target_now - start, axis=1) / SPEED_OF_LIGHT, 10.0)
    for _ in range(4):
        target = planet_position(destinations, t + lead)
        lead = np.minimum(np.linalg.norm(target - start, axis=1) / SPEED_OF_LIGHT, 10.0)
    target = planet_position(destinations, t + lead)
    distance = np.linalg.norm(target - start, axis=1)

    bh = np.broadcast_to(np.array(BLACK_HOLE), start.shape)
    min_bh = _segment_min_distance(start, target, bh)
    destroyed = min_bh <= PULL_R
    # In the stall zone the dilation factor decays toward TIME_DIL; approximate the
    # depth of the dip by how far into the zone the path goes.
    depth = np.clip((STALL_R - min_bh) / (STALL_R - PULL_R), 0.0, 1.0)
    min_dilation = np.where(min_bh < STALL_R, 1.0 - depth * (1.0 - TIME_DIL), 1.0)
    # Time spent in the zone is slower; average speed drops with the dip.
    avg_speed = SPEED_OF_LIGHT * (1.0 - 0.35 * (1.0 - min_dilation))
    travel_time = distance / avg_speed
    outcome = np.where(destroyed, "destroyed", "settled")
    return {
        "distance": distance,
        "travel_time_s": travel_time,
        "min_bh_dist": min_bh,
        "min_dilation": min_dilation,
        "outcome": outcome,
    }
