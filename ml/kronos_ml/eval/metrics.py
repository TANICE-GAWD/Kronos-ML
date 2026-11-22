"""Evaluation metrics for fraud scoring."""
from __future__ import annotations

import numpy as np
import pandas as pd
from sklearn.metrics import average_precision_score, f1_score, precision_score, recall_score, roc_auc_score


def at_threshold(y, score, thr) -> dict:
    pred = (score >= thr).astype(int)
    return {
        "precision": precision_score(y, pred, zero_division=0),
        "recall": recall_score(y, pred, zero_division=0),
        "f1": f1_score(y, pred, zero_division=0),
        "alert_rate": pred.mean(),
    }


def at_budget(y, score, rate) -> dict:
    """Precision/recall when analysts can only review the top `rate` share of transfers."""
    thr = np.quantile(score, 1 - rate)
    r = at_threshold(y, score, thr)
    return {"precision": r["precision"], "recall": r["recall"]}


def ranking(y, score) -> dict:
    return {"roc_auc": roc_auc_score(y, score), "pr_auc": average_precision_score(y, score)}


def per_pattern_recall(fraud_type: pd.Series, score, thr) -> pd.Series:
    flagged = pd.Series(score >= thr, index=fraud_type.index)
    ft = fraud_type[fraud_type != "none"]
    return flagged[ft.index].groupby(ft).mean()


def business_cost(y, score, amount, thr, review_cost: float) -> dict:
    """Missed fraud money + analyst time spent on every alert."""
    flagged = score >= thr
    missed = float(amount[(y == 1) & ~flagged].sum())
    caught = float(amount[(y == 1) & flagged].sum())
    reviews = int(flagged.sum())
    return {
        "missed_fraud_amount": missed,
        "caught_fraud_amount": caught,
        "alerts": reviews,
        "review_cost": reviews * review_cost,
        "total_cost": missed + reviews * review_cost,
    }


def full_report(y, score, thr, fraud_type, amount, review_cost) -> dict:
    out = ranking(y, score)
    out.update({f"{k}@thr": v for k, v in at_threshold(y, score, thr).items()})
    for rate in (0.01, 0.02):
        out.update({f"{k}@top{int(rate * 100)}%": v for k, v in at_budget(y, score, rate).items()})
    out.update(business_cost(y, score, amount, thr, review_cost))
    return out
