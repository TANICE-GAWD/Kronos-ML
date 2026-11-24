"""Past-only behavioural features.

`FeatureState` is a streaming feature store. For every transfer it first
computes features from what it has seen so far, and only then adds the transfer
to its history (`update`). Running the log through it in time order therefore
guarantees no feature looks at the transfer itself or anything after it.

The same class powers the live risk service (features/online.py), so offline
training and online scoring compute identical features.
"""
from __future__ import annotations

import math
import time
from collections import defaultdict, deque
from dataclasses import dataclass, field

import numpy as np
import pandas as pd

from ..config import load_config
from ..constants import STALL_R

MIN, HOUR, DAY = 60.0, 3600.0, 86400.0

FEATURES = [
    # amount
    "amount_log", "amount_z", "amount_to_median", "amount_to_max", "amount_to_balance",
    # velocity
    "tx_10m", "tx_1h", "tx_24h", "amount_1h_log", "secs_since_last_log",
    # counterparty
    "new_receiver", "distinct_receivers_1h", "receiver_age_days_log",
    "receiver_inflow_senders_1h", "receiver_inflow_amount_1h_log",
    # graph / cycle
    "round_trip_30m", "cycle_3hop_60m", "passthrough_ratio",
    # time
    "hour_sin", "hour_cos", "hour_rarity", "is_weekend",
    # account
    "sender_age_days_log", "sender_tx_count_log", "warmup",
    # physics
    "distance_log", "flight_time_s", "bh_risk",
]

# Human-readable groups, used by the notebooks and the report.
FEATURE_GROUPS = {
    "Amount": FEATURES[0:5],
    "Velocity": FEATURES[5:10],
    "Counterparty": FEATURES[10:15],
    "Graph / cycle": FEATURES[15:18],
    "Time": FEATURES[18:22],
    "Account": FEATURES[22:25],
    "Physics": FEATURES[25:28],
}


@dataclass
class _Sender:
    n: int = 0
    mean: float = 0.0      # running mean of log amount
    m2: float = 0.0        # running sum of squares (Welford)
    max_amount: float = 0.0
    last_t: float | None = None
    receivers: set = field(default_factory=set)
    recent: deque = field(default_factory=deque)       # (t, amount, receiver) within 24h
    hours: np.ndarray = field(default_factory=lambda: np.zeros(24))


class FeatureState:
    def __init__(self, cfg: dict | None = None):
        f = (cfg or load_config())["features"]
        self.mu0 = f["amount_prior_log_mean"]
        self.sd0 = f["amount_prior_log_std"]
        self.k0 = f["prior_weight"]
        self.warmup_tx = f["warmup_tx"]
        self.senders: dict[str, _Sender] = defaultdict(_Sender)
        self.inflow: dict[str, deque] = defaultdict(deque)      # receiver -> (t, sender, amount) 1h
        self.pair_last: dict[tuple, float] = {}                  # (a, b) -> last time a paid b
        self.out_recent: dict[str, deque] = defaultdict(deque)   # sender -> (t, receiver) 1h
        self.created: dict[str, float] = {}
        self.max_t = float("-inf")  # latest transfer time seen

    # ------------------------------------------------------------------ helpers
    def register_user(self, user_id: str, created_at: float) -> None:
        self.created[user_id] = created_at

    def _shrunk_stats(self, s: _Sender) -> tuple[float, float]:
        """Mean/std of log amount, shrunk toward the population prior for new users."""
        n = s.n
        mean = (self.k0 * self.mu0 + n * s.mean) / (self.k0 + n)
        var_s = s.m2 / (n - 1) if n > 1 else self.sd0**2
        var = (self.k0 * self.sd0**2 + (n - 1 if n > 1 else 0) * var_s) / (
            self.k0 + (n - 1 if n > 1 else 0)
        )
        return mean, max(math.sqrt(var), 0.15)

    @staticmethod
    def _trim(dq: deque, t: float, window: float) -> None:
        while dq and dq[0][0] < t - window:
            dq.popleft()

    # ------------------------------------------------------------------ core
    def compute(self, tx: dict) -> dict:
        """Features for one transfer using only history seen so far. Does not mutate state.

        Every window also ignores history stamped after `t`. Offline data is
        sorted so this never triggers; live, demo bots run on a simulated clock
        while humans run on the real one.
        """
        sid, rid, t, amt = tx["sender_id"], tx["receiver_id"], float(tx["t"]), float(tx["amount"])
        s = self.senders.get(sid) or _Sender()
        mean, sd = self._shrunk_stats(s)
        log_amt = math.log1p(amt)
        median = math.exp(mean)

        recent = [r for r in s.recent if t - DAY <= r[0] <= t]
        tx_10m = sum(1 for r in recent if r[0] >= t - 10 * MIN)
        last_hour = [r for r in recent if r[0] >= t - HOUR]
        tx_1h = len(last_hour)
        amount_1h = sum(r[1] for r in last_hour)
        distinct_1h = len({r[2] for r in last_hour} | {rid})

        inflow = [r for r in self.inflow.get(rid, ()) if t - HOUR <= r[0] <= t]
        rx_senders = len({r[1] for r in inflow})
        rx_amount = sum(r[2] for r in inflow)
        # Money the sender itself received in the last 30 min (pass-through / layering).
        got = [r for r in self.inflow.get(sid, ()) if t - 30 * MIN <= r[0] <= t]
        got_amt = sum(r[2] for r in got)
        passthrough = min(amt / got_amt, 3.0) if got_amt > 0 else 0.0
        passthrough = passthrough if passthrough > 0.5 else 0.0

        back = self.pair_last.get((rid, sid))
        round_trip = 1.0 if back is not None and t - 30 * MIN <= back <= t else 0.0
        cycle = 0.0
        for (tt, x) in self.out_recent.get(rid, ()):
            if t - HOUR <= tt <= t and x != sid:
                lx = self.pair_last.get((x, sid))
                if lx is not None and tt <= lx <= t:
                    cycle = 1.0
                    break

        ts = pd.Timestamp(t, unit="s")
        hour = ts.hour + ts.minute / 60.0
        h = ts.hour
        hist = s.hours
        rarity = 1.0 - (hist[h] + hist[(h - 1) % 24] * 0.5 + hist[(h + 1) % 24] * 0.5 + 0.1) / (
            hist.sum() + 2.4
        ) * 12.0
        rarity = float(np.clip(rarity, 0.0, 1.0)) if s.n >= 3 else 0.5

        s_created = self.created.get(sid, t)
        r_created = self.created.get(rid, t)
        balance = float(tx.get("sender_balance") or 0.0)
        min_bh = float(tx.get("min_bh_dist", 1e3))

        return {
            "amount_log": log_amt,
            "amount_z": (math.log(max(amt, 1e-6)) - mean) / sd,
            "amount_to_median": math.log(max(amt, 1e-6) / median),
            "amount_to_max": math.log(max(amt, 1e-6) / max(s.max_amount, median)),
            "amount_to_balance": min(amt / balance, 1.0) if balance > 0 else 1.0,
            "tx_10m": float(tx_10m),
            "tx_1h": float(tx_1h),
            "tx_24h": float(len(recent)),
            "amount_1h_log": math.log1p(amount_1h),
            "secs_since_last_log": math.log1p(min(max(t - s.last_t, 0.0), 30 * DAY)) if s.last_t else math.log1p(30 * DAY),
            "new_receiver": 0.0 if rid in s.receivers else 1.0,
            "distinct_receivers_1h": float(distinct_1h),
            "receiver_age_days_log": math.log1p(max(t - r_created, 0.0) / DAY),
            "receiver_inflow_senders_1h": float(rx_senders),
            "receiver_inflow_amount_1h_log": math.log1p(rx_amount),
            "round_trip_30m": round_trip,
            "cycle_3hop_60m": cycle,
            "passthrough_ratio": passthrough,
            "hour_sin": math.sin(2 * math.pi * hour / 24),
            "hour_cos": math.cos(2 * math.pi * hour / 24),
            "hour_rarity": rarity,
            "is_weekend": 1.0 if ts.day_of_week >= 5 else 0.0,
            "sender_age_days_log": math.log1p(max(t - s_created, 0.0) / DAY),
            "sender_tx_count_log": math.log1p(s.n),
            "warmup": 1.0 if s.n < self.warmup_tx else 0.0,
            "distance_log": math.log1p(float(tx.get("distance", 0.0))),
            "flight_time_s": float(tx.get("travel_time_s", 0.0)),
            "bh_risk": float(np.clip((STALL_R + 40 - min_bh) / (STALL_R + 40), 0.0, 1.0)),
        }

    def update(self, tx: dict) -> None:
        """Add a transfer to history. Call only after `compute`."""
        sid, rid, t, amt = tx["sender_id"], tx["receiver_id"], float(tx["t"]), float(tx["amount"])
        s = self.senders[sid]
        x = math.log(max(amt, 1e-6))
        s.n += 1
        d = x - s.mean
        s.mean += d / s.n
        s.m2 += d * (x - s.mean)
        s.max_amount = max(s.max_amount, amt)
        s.last_t = t if s.last_t is None else max(s.last_t, t)
        self.max_t = max(self.max_t, t)
        s.receivers.add(rid)
        s.recent.append((t, amt, rid))
        self._trim(s.recent, t, DAY)
        s.hours[pd.Timestamp(t, unit="s").hour] += 1
        q = self.inflow[rid]
        q.append((t, sid, amt))
        self._trim(q, t, HOUR)
        self.pair_last[(sid, rid)] = t
        o = self.out_recent[sid]
        o.append((t, rid))
        self._trim(o, t, HOUR)

    def process(self, tx: dict) -> dict:
        feats = self.compute(tx)
        self.update(tx)
        return feats


def tx_records(tx: pd.DataFrame):
    """Yield dicts in the shape FeatureState expects, in time order."""
    t = tx["created_at"].astype("int64").to_numpy() / 1e9 if np.issubdtype(
        tx["created_at"].dtype, np.datetime64
    ) else pd.to_datetime(tx["created_at"]).astype("int64").to_numpy() / 1e9
    cols = {c: tx[c].to_numpy() for c in ["sender_id", "receiver_id", "amount", "sender_balance",
                                         "distance", "travel_time_s", "min_bh_dist"] if c in tx}
    for i in range(len(tx)):
        rec = {k: v[i] for k, v in cols.items()}
        rec["t"] = t[i]
        yield rec


def build_features(tx: pd.DataFrame, users: pd.DataFrame | None = None, cfg: dict | None = None) -> pd.DataFrame:
    """Feature matrix aligned with `tx` (which must be sorted by created_at)."""
    tx = tx.copy()
    tx["created_at"] = pd.to_datetime(tx["created_at"])
    state = FeatureState(cfg)
    if users is not None:
        created = pd.to_datetime(users["created_at"]).astype("int64").to_numpy() / 1e9
        for uid, c in zip(users["user_id"], created):
            state.register_user(uid, c)
    rows = [state.process(r) for r in tx_records(tx)]
    return pd.DataFrame(rows, columns=FEATURES, index=tx.index)


def load_dataset(data_dir=None) -> tuple[pd.DataFrame, pd.DataFrame]:
    from ..config import DATA_DIR

    d = data_dir or DATA_DIR
    tx = pd.read_csv(f"{d}/transactions.csv", parse_dates=["created_at"])
    users = pd.read_csv(f"{d}/users.csv", parse_dates=["created_at"])
    return tx, users


def main():
    import time

    from ..config import DATA_DIR

    t = time.time()
    tx, users = load_dataset()
    X = build_features(tx, users)
    out = pd.concat([tx, X], axis=1)
    out.to_parquet(DATA_DIR / "features.parquet") if _has_parquet() else out.to_csv(
        DATA_DIR / "features.csv", index=False
    )
    print(f"built {X.shape[1]} features for {len(X):,} transfers in {time.time() - t:.1f}s")


def _has_parquet() -> bool:
    try:
        import pyarrow  # noqa: F401

        return True
    except ImportError:
        return False


if __name__ == "__main__":
    main()
