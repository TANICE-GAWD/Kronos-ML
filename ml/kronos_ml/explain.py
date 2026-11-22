"""Reason codes an analyst can read.

Two explainers:
* ReasonExplainer: model-agnostic. For each feature it measures how far the
  value sits on the suspicious side of what is normal in the training data
  (robust z-score), and turns the biggest deviations into reason codes.
* shap_values(): SHAP TreeExplainer for the Isolation Forest (notebook 05).
"""
from __future__ import annotations

import math

import numpy as np
import pandas as pd

# feature -> (code, suspicious direction: +1 high, -1 low)
FEATURE_CODES = {
    "amount_z": ("AMOUNT_SPIKE", 1),
    "amount_to_median": ("AMOUNT_SPIKE", 1),
    "amount_log": ("LARGE_AMOUNT", 1),
    "amount_to_max": ("ABOVE_PREVIOUS_MAX", 1),
    "amount_to_balance": ("BALANCE_DRAIN", 1),
    "tx_10m": ("VELOCITY_10M", 1),
    "tx_1h": ("VELOCITY_1H", 1),
    "tx_24h": ("VELOCITY_1H", 1),
    "amount_1h_log": ("VELOCITY_1H", 1),
    "secs_since_last_log": ("RAPID_REPEAT", -1),
    "new_receiver": ("NEW_PAYEE", 1),
    "distinct_receivers_1h": ("FAN_OUT", 1),
    "receiver_age_days_log": ("NEW_RECEIVER_ACCOUNT", -1),
    "receiver_inflow_senders_1h": ("MULE_INFLOW", 1),
    "receiver_inflow_amount_1h_log": ("MULE_INFLOW", 1),
    "round_trip_30m": ("ROUND_TRIP", 1),
    "cycle_3hop_60m": ("ROUND_TRIP", 1),
    "passthrough_ratio": ("PASS_THROUGH", 1),
    "hour_rarity": ("ODD_HOUR", 1),
    "sender_age_days_log": ("NEW_ACCOUNT", -1),
    "sender_tx_count_log": ("NEW_ACCOUNT", -1),
    "warmup": ("NEW_ACCOUNT", 1),
    "bh_risk": ("BLACK_HOLE_PATH", 1),
    "distance_log": ("LONG_ROUTE", 1),
    "flight_time_s": ("LONG_ROUTE", 1),
}


def reason_text(code: str, f: dict) -> str:
    g = f.get
    if code == "AMOUNT_SPIKE":
        return f"amount is {math.exp(g('amount_to_median', 0)):.1f}x this sender's usual"
    if code == "LARGE_AMOUNT":
        return f"large amount ({math.expm1(g('amount_log', 0)):,.0f} credits)"
    if code == "ABOVE_PREVIOUS_MAX":
        return f"{math.exp(g('amount_to_max', 0)):.1f}x the largest transfer they ever sent"
    if code == "BALANCE_DRAIN":
        return f"uses {g('amount_to_balance', 0) * 100:.0f}% of the available balance"
    if code == "VELOCITY_10M":
        return f"{int(g('tx_10m', 0)) + 1} transfers in the last 10 minutes"
    if code == "VELOCITY_1H":
        return f"{int(g('tx_1h', 0)) + 1} transfers in the last hour"
    if code == "RAPID_REPEAT":
        return f"sent again {math.expm1(g('secs_since_last_log', 0)):.0f}s after the previous transfer"
    if code == "NEW_PAYEE":
        return "first payment to this receiver"
    if code == "FAN_OUT":
        return f"{int(g('distinct_receivers_1h', 0))} different receivers in the last hour"
    if code == "NEW_RECEIVER_ACCOUNT":
        return f"receiver account is {math.expm1(g('receiver_age_days_log', 0)):.1f} days old"
    if code == "MULE_INFLOW":
        return f"receiver collected money from {int(g('receiver_inflow_senders_1h', 0))} senders in the last hour"
    if code == "ROUND_TRIP":
        return "money is cycling back toward where it came from"
    if code == "PASS_THROUGH":
        return "forwards money received in the last 30 minutes"
    if code == "ODD_HOUR":
        h = (math.degrees(math.atan2(g("hour_sin", 0), g("hour_cos", 1))) % 360) / 15
        return f"unusual time of day for this sender ({int(h):02d}:00)"
    if code == "NEW_ACCOUNT":
        return f"sender account is {math.expm1(g('sender_age_days_log', 0)):.1f} days old with little history"
    if code == "BLACK_HOLE_PATH":
        return "route passes close to the black hole"
    if code == "LONG_ROUTE":
        return "unusually long route"
    if code == "VELOCITY_CAP":
        return "rule: 8+ transfers in 10 minutes"
    if code == "AMOUNT_CAP":
        return "rule: amount 20x+ the sender's usual"
    if code == "DRAIN_TO_NEW":
        return "rule: over 90% of balance to a first-time receiver"
    return code


class ReasonExplainer:
    def __init__(self, features: list[str]):
        self.features = [f for f in features if f in FEATURE_CODES]

    def fit(self, X: pd.DataFrame) -> "ReasonExplainer":
        q = X[self.features].quantile([0.5, 0.9, 0.1])
        self.med = q.loc[0.5]
        spread = (q.loc[0.9] - q.loc[0.1]).abs()
        # Binary / sparse features: fall back to the std so a 1 still counts.
        self.scale = spread.where(spread > 1e-9, X[self.features].std().replace(0, 1.0))
        return self

    def contributions(self, X: pd.DataFrame) -> pd.DataFrame:
        sign = np.array([FEATURE_CODES[f][1] for f in self.features])
        z = (X[self.features] - self.med) / self.scale * sign
        return z.clip(lower=0)

    def explain(self, feats: dict, rule_codes: list[str] | None = None, top: int = 3) -> list[dict]:
        c = self.contributions(pd.DataFrame([feats])).iloc[0]
        by_code: dict[str, float] = {}
        for f, v in c.items():
            code = FEATURE_CODES[f][0]
            by_code[code] = max(by_code.get(code, 0.0), float(v))
        ranked = sorted(((v, k) for k, v in by_code.items() if v > 0.5), reverse=True)[:top]
        reasons = [{"code": r, "text": reason_text(r, feats), "weight": 1.0} for r in (rule_codes or [])]
        total = sum(v for v, _ in ranked) or 1.0
        for v, k in ranked:
            if len(reasons) >= top:
                break
            if all(r["code"] != k for r in reasons):
                reasons.append({"code": k, "text": reason_text(k, feats), "weight": round(v / total, 3)})
        return reasons


def shap_values(iforest_detector, X: pd.DataFrame):
    """SHAP values for an IsolationForestDetector (positive = pushes toward anomalous)."""
    import shap

    scaler, forest = iforest_detector.pipe[0], iforest_detector.pipe[-1]
    Xs = scaler.transform(X[iforest_detector.features].to_numpy())
    explainer = shap.TreeExplainer(forest)
    # TreeExplainer explains the path-length output (higher = more normal), so flip it.
    return -explainer.shap_values(Xs), Xs
