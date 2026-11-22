"""Map raw anomaly scores to a 0-1 risk score with bank-style bands.

Raw scores mean different things for different detectors. Using quantiles of
the validation slice, the chosen thresholds are pinned to fixed points so the
bands are identical whatever model is deployed:

    raw at the medium threshold -> 0.5   (Low below, Medium above)
    raw at the high threshold   -> 0.8   (High: hold for review)
"""
from __future__ import annotations

import numpy as np

LOW, MEDIUM, HIGH = "low", "medium", "high"
MEDIUM_AT, HIGH_AT = 0.5, 0.8


class Calibrator:
    def __init__(self, val_raw: np.ndarray, medium_raw: float, high_raw: float):
        q = np.quantile(val_raw, [0.0, 0.5, 0.999])
        lo, mid, top = float(q[0]), float(q[1]), float(q[2])
        hi_max = max(float(np.max(val_raw)), high_raw) + 1e-9
        xs = [lo, mid, medium_raw, high_raw, max(top, high_raw + 1e-9), hi_max + abs(hi_max) * 0.5 + 1e-6]
        ys = [0.0, 0.2, MEDIUM_AT, HIGH_AT, 0.97, 1.0]
        # Knots must increase strictly; nudge collisions.
        for i in range(1, len(xs)):
            if xs[i] <= xs[i - 1]:
                xs[i] = xs[i - 1] + 1e-9
        self.xs, self.ys = np.array(xs), np.array(ys)
        self.medium_raw, self.high_raw = medium_raw, high_raw

    def __call__(self, raw) -> np.ndarray:
        return np.interp(raw, self.xs, self.ys)


def band(score: float) -> str:
    if score >= HIGH_AT:
        return HIGH
    if score >= MEDIUM_AT:
        return MEDIUM
    return LOW
