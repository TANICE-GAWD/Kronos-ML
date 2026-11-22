"""Rank-average ensemble of the best detectors."""
from __future__ import annotations

import numpy as np
import pandas as pd

from .detectors import Detector


class RankEnsemble:
    """Average of the empirical-CDF ranks of several detectors."""

    name = "ensemble"

    def __init__(self, members: list[Detector], reference: pd.DataFrame):
        self.members = members
        self.features = members[0].features
        self.refs = [np.sort(m.raw(reference)) for m in members]

    def raw(self, X: pd.DataFrame) -> np.ndarray:
        ranks = [np.searchsorted(ref, m.raw(X), side="right") / len(ref) for m, ref in zip(self.members, self.refs)]
        return np.mean(ranks, axis=0)

    def describe(self) -> str:
        return "ensemble(" + " + ".join(m.name for m in self.members) + ")"
