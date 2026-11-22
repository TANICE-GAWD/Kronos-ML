"""Statistical baseline: robust z-scores plus the hard rules."""
from __future__ import annotations

import numpy as np
import pandas as pd

from ..rules import rule_scores
from .detectors import Detector


class ZScoreBaseline(Detector):
    """Statistical baseline: largest robust z-score over amount, velocity and hour,
    lifted by the hard rules."""

    name = "zscore_baseline"
    COLS = ["amount_z", "amount_to_median", "tx_10m", "tx_1h", "hour_rarity"]

    def fit(self, X: pd.DataFrame) -> "ZScoreBaseline":
        Xs = self._sample(X)
        self.med = Xs[self.COLS].median()
        q = Xs[self.COLS].quantile([0.1, 0.9])
        self.scale = (q.loc[0.9] - q.loc[0.1]).replace(0, 1.0)
        self.pipe = "fitted"
        return self

    def raw(self, X: pd.DataFrame) -> np.ndarray:
        z = ((X[self.COLS] - self.med) / self.scale).clip(lower=0)
        rule, _ = rule_scores(X)
        return np.maximum(z.max(axis=1).to_numpy(), rule * 10.0)
