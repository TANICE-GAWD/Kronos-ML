"""Anomaly detectors with one shared interface.

Every detector exposes fit(X) and raw(X), where a higher raw score means more
anomalous. Labels are never passed in: training is fully unsupervised.
"""
from __future__ import annotations

import numpy as np
import pandas as pd
from sklearn.ensemble import IsolationForest
from sklearn.neighbors import LocalOutlierFactor
from sklearn.pipeline import make_pipeline
from sklearn.preprocessing import RobustScaler
from sklearn.svm import OneClassSVM



class Detector:
    name = "detector"

    def __init__(self, features: list[str], max_rows: int | None = None, seed: int = 0, **params):
        self.features = features
        self.max_rows = max_rows
        self.seed = seed
        self.params = params
        self.pipe = None

    def _sample(self, X: pd.DataFrame) -> pd.DataFrame:
        if self.max_rows and len(X) > self.max_rows:
            return X.sample(self.max_rows, random_state=self.seed)
        return X

    def _estimator(self):
        raise NotImplementedError

    def fit(self, X: pd.DataFrame) -> "Detector":
        self.pipe = make_pipeline(RobustScaler(quantile_range=(10, 90)), self._estimator())
        self.pipe.fit(self._sample(X[self.features]).to_numpy())
        return self

    def raw(self, X: pd.DataFrame) -> np.ndarray:
        return -self.pipe.score_samples(X[self.features].to_numpy())

    def describe(self) -> str:
        return f"{self.name}({', '.join(f'{k}={v}' for k, v in self.params.items())})"


class IsolationForestDetector(Detector):
    name = "isolation_forest"

    def _estimator(self):
        p = {"n_estimators": 200, "max_samples": 512, "max_features": 1.0} | self.params
        return IsolationForest(random_state=self.seed, n_jobs=-1, **p)


class LOFDetector(Detector):
    name = "lof"

    def _estimator(self):
        p = {"n_neighbors": 35} | self.params
        return LocalOutlierFactor(novelty=True, n_jobs=-1, **p)


class OCSVMDetector(Detector):
    name = "ocsvm"

    def _estimator(self):
        p = {"nu": 0.03, "gamma": "scale", "kernel": "rbf"} | self.params
        return OneClassSVM(cache_size=1000, **p)


from .baseline import ZScoreBaseline  # noqa: E402

FAMILIES = {
    "zscore_baseline": (ZScoreBaseline, [{}]),
    "isolation_forest": (
        IsolationForestDetector,
        [
            {"n_estimators": n, "max_samples": m, "max_features": f}
            for n in (200,)
            for m in (256, 1024, 2048)
            for f in (1.0, 0.6)
        ],
    ),
    "lof": (LOFDetector, [{"n_neighbors": k} for k in (20, 100, 250)]),
    "ocsvm": (OCSVMDetector, [{"nu": nu, "gamma": g} for nu in (0.01, 0.05, 0.1) for g in ("scale", 0.01)]),
}


