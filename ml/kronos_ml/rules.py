"""Hard rules that run next to the model, like a bank's rules engine.

Rules are deliberately few and conservative. The final live score is
max(rule_score, model_score).
"""
from __future__ import annotations

import math

import numpy as np
import pandas as pd

RULES = [
    # code, description, score
    ("VELOCITY_CAP", "8 or more transfers in 10 minutes", 1.0),
    ("AMOUNT_CAP", "amount at least 20x the sender's usual", 0.95),
    ("DRAIN_TO_NEW", "over 90% of the balance to a first-time receiver", 0.9),
]
_LN20 = math.log(20.0)


def rule_scores(X: pd.DataFrame) -> tuple[np.ndarray, list[list[str]]]:
    hits = np.zeros((len(X), len(RULES)), dtype=bool)
    hits[:, 0] = X["tx_10m"].to_numpy() >= 7          # this transfer is the 8th
    hits[:, 1] = X["amount_to_median"].to_numpy() >= _LN20
    hits[:, 2] = (
        (X["amount_to_balance"].to_numpy() >= 0.9)
        & (X["new_receiver"].to_numpy() == 1)
        & (X["amount_log"].to_numpy() >= math.log1p(100))
    )
    weights = np.array([r[2] for r in RULES])
    score = (hits * weights).max(axis=1)
    codes = [[RULES[j][0] for j in np.nonzero(row)[0]] for row in hits]
    return score, codes


def rule_score_one(feats: dict) -> tuple[float, list[str]]:
    s, c = rule_scores(pd.DataFrame([feats]))
    return float(s[0]), c[0]
