"""Online scoring: the same FeatureState as training, one transfer at a time.

`OnlineScorer` is what the risk service wraps. For each transfer it
  1. computes past-only features (identical code to the offline builder),
  2. runs the hard rules and the deployed detector,
  3. maps the raw score to 0-1 and a band (low / medium / high),
  4. explains the score with reason codes,
  5. only then adds the transfer to history.

It also owns the demo bots: `warmup` gives them a synthetic past so their
features are meaningful, and `plan` produces a scenario of normal and
fraudulent transfers for the Go traffic generator to execute.
"""
from __future__ import annotations

import threading
import time
from datetime import datetime, timezone

import joblib
import numpy as np
import pandas as pd

from ..config import MODELS_DIR, load_config
from ..models.calibration import band
from ..physics import simulate_flights
from ..rules import rule_score_one
from ..simulator.normal import normal_events
from ..simulator.patterns import PATTERNS, inject, instances_for_rate
from ..simulator.users import DAY, make_profiles
from .build import FEATURES, FeatureState

HOUR = 3600.0

SCENARIOS = {
    "normal_day": {"label": "Normal day", "hours": 24, "fraud_rate": 0.0, "patterns": {}},
    "account_takeover": {"label": "Account takeover", "hours": 3, "fraud_rate": None,
                         "patterns": {"micro_drain": 1, "whale": 1}},
    "laundering_ring": {"label": "Laundering ring", "hours": 3, "fraud_rate": None,
                        "patterns": {"layering": 3, "mule_fan_in": 1}},
    "mixed_attack": {"label": "Mixed attack", "hours": 12, "fraud_rate": None,
                     "patterns": {"micro_drain": 1, "whale": 2, "odd_hour_spike": 1, "layering": 1,
                                  "mule_fan_in": 1, "new_account_burst": 1}},
}


def to_unix(ts) -> float:
    if ts is None or ts == "":
        return time.time()
    if isinstance(ts, (int, float, np.floating)):
        return float(ts)
    # Naive timestamps (Postgres TIMESTAMP) are treated as UTC.
    return pd.Timestamp(ts).timestamp()


class OnlineScorer:
    def __init__(self, bundle_path=None):
        self.cfg = load_config()
        self.bundle = joblib.load(bundle_path or MODELS_DIR / "deployed.joblib")
        self._single_thread(self.bundle["detector"])
        # One row at a time: BLAS/OpenMP thread pools only add overhead (60ms -> 20ms).
        from threadpoolctl import threadpool_limits

        threadpool_limits(1)
        self.state = FeatureState(self.cfg)
        self.lock = threading.Lock()
        self.profiles = None
        self.sim_clock: float | None = None
        self.scored = 0

    def reset(self):
        with self.lock:
            self.state = FeatureState(self.cfg)
            self.profiles = None
            self.sim_clock = None
            self.scored = 0

    @staticmethod
    def _single_thread(det):
        """Per-call scoring of one row is faster without joblib thread pools."""
        for d in getattr(det, "members", [det]):
            est = d.pipe[-1] if hasattr(d.pipe, "__getitem__") and not isinstance(d.pipe, str) else None
            if est is not None and hasattr(est, "n_jobs"):
                est.n_jobs = 1

    # ------------------------------------------------------------- scoring
    @property
    def version(self) -> str:
        return self.bundle["version"]

    def _features(self, tx: dict) -> dict:
        f = simulate_flights([tx["origin_planet"].lower()], [tx["destination_planet"].lower()], [tx["t"]])
        tx = {**tx, **{k: float(v[0]) for k, v in f.items() if k != "outcome"}}
        return tx, self.state.compute(tx)

    def score(self, req: dict) -> dict:
        start = time.perf_counter()
        tx = {
            "sender_id": str(req["sender_id"]),
            "receiver_id": str(req["receiver_id"]),
            "amount": float(req["amount"]),
            "t": to_unix(req.get("timestamp")),
            "sender_balance": float(req.get("sender_balance") or 0.0),
            "origin_planet": req.get("origin_planet") or "earth",
            "destination_planet": req.get("destination_planet") or "earth",
        }
        with self.lock:
            for key, uid in (("sender_created_at", tx["sender_id"]), ("receiver_created_at", tx["receiver_id"])):
                if uid not in self.state.created and req.get(key):
                    self.state.register_user(uid, to_unix(req[key]))
            tx, feats = self._features(tx)
            X = pd.DataFrame([feats], columns=FEATURES)
            raw = float(self.bundle["detector"].raw(X)[0])
            model_score = float(self.bundle["calibrator"](raw))
            rule, rule_codes = rule_score_one(feats)
            score = max(model_score, rule)
            reasons = self.bundle["explainer"].explain(feats, rule_codes)
            self.state.update(tx)
            self.scored += 1
        return {
            "tx_id": req.get("tx_id"),
            "score": round(score, 4),
            "model_score": round(model_score, 4),
            "rule_score": rule,
            "band": band(score),
            "reasons": reasons,
            "features": {k: round(float(v), 4) for k, v in feats.items()},
            "model_version": self.version,
            "latency_ms": round((time.perf_counter() - start) * 1000, 2),
        }

    # ------------------------------------------------------------- history
    def warm_from_rows(self, users: list[dict], txs: list[dict]) -> int:
        """Replay real history (e.g. from Postgres) so features see the past."""
        with self.lock:
            for u in users:
                self.state.register_user(str(u["id"]), to_unix(u["created_at"]))
            n = 0
            for r in sorted(txs, key=lambda r: to_unix(r["created_at"])):
                self.state.update({
                    "sender_id": str(r["sender_id"]), "receiver_id": str(r["receiver_id"]),
                    "amount": float(r["amount"]), "t": to_unix(r["created_at"]),
                })
                n += 1
        return n

    def warm_from_db(self, dsn: str) -> int:
        import psycopg

        with psycopg.connect(dsn) as conn, conn.cursor() as cur:
            cur.execute("SET TRANSACTION READ ONLY")
            cur.execute("SELECT id, created_at FROM users")
            users = [{"id": r[0], "created_at": r[1]} for r in cur.fetchall()]
            cur.execute(
                "SELECT sender_id, receiver_id, amount, created_at FROM transactions "
                "WHERE created_at > now() - interval '30 days' ORDER BY created_at"
            )
            txs = [{"sender_id": r[0], "receiver_id": r[1], "amount": r[2], "created_at": r[3]} for r in cur.fetchall()]
        return self.warm_from_rows(users, txs)

    # ------------------------------------------------------------- demo bots
    def warmup_bots(self, users: list[dict], days: int = 30, fresh_share: float = 0.06, seed: int = 7,
                    sim_start=None) -> dict:
        """Give demo bots a synthetic past of normal behaviour.

        A few bots are kept dormant ("fresh") with no history; mule and
        new-account fraud claim them, exactly like the offline simulator.
        """
        rng = np.random.default_rng(seed)
        n = len(users)
        # Simulated time starts after everything already in history (real transfers
        # replayed from Postgres, earlier demo runs), so bot history never runs backwards.
        floor = max(time.time(), self.state.max_t if np.isfinite(self.state.max_t) else 0.0)
        start = to_unix(sim_start) if sim_start else floor + HOUR
        p = make_profiles(
            rng, n, start, days,
            user_ids=[str(u["id"]) for u in users],
            usernames=[u["username"] for u in users],
            home_planets=[u["home_planet"] for u in users],
            late_join_share=0.0,
        )
        n_fresh = max(2, int(round(n * fresh_share)))
        fresh = rng.choice(n, size=n_fresh, replace=False)
        p.role[fresh] = "fresh"
        p.created_at[fresh] = np.nan
        for i in fresh:
            p.contacts[i] = np.array([], dtype=int)
            p.contact_weights[i] = np.array([])
        for i in range(n):  # nobody keeps a dormant account as a contact
            keep = ~np.isin(p.contacts[i], fresh)
            if len(p.contacts[i]):
                w = p.contact_weights[i][keep]
                p.contacts[i] = p.contacts[i][keep]
                p.contact_weights[i] = w / w.sum() if w.sum() else w
        ev = normal_events(p, rng, start - days * DAY, start)
        with self.lock:
            for i, uid in enumerate(p.user_id):
                if not np.isnan(p.created_at[i]):
                    self.state.register_user(uid, p.created_at[i])
            for k in np.argsort(ev["t"]):
                self.state.update({
                    "sender_id": p.user_id[ev["sender"][k]], "receiver_id": p.user_id[ev["receiver"][k]],
                    "amount": float(ev["amount"][k]), "t": float(ev["t"][k]),
                })
        self.profiles = p
        self.sim_clock = start
        return {"bots": n, "dormant": int(n_fresh), "history_transfers": int(len(ev["t"])),
                "sim_start": datetime.fromtimestamp(start, timezone.utc).isoformat()}

    def plan(self, scenario: str = "mixed_attack", n: int = 100, fraud_rate: float | None = None,
             seed: int | None = None) -> dict:
        if self.profiles is None:
            raise RuntimeError("bots are not warmed up; call /warmup first")
        sc = SCENARIOS[scenario]
        rng = np.random.default_rng(seed if seed is not None else int(time.time()))
        p = self.profiles
        # Stretch the simulated window so bots send at their natural pace; squeezing
        # hundreds of transfers into a few hours would make everyone look "fast".
        per_hour = p.rate_per_day[p.role == "normal"].sum() / 24
        hours = min(max(sc["hours"], n / max(per_hour, 1e-9)), 7 * 24)
        t0 = max(self.sim_clock, self.state.max_t) + HOUR
        t1 = t0 + hours * HOUR
        fr = sc["fraud_rate"] if fraud_rate is None else fraud_rate
        if sc["patterns"] is not None and fraud_rate is None:
            counts = dict(sc["patterns"])
        else:
            counts = instances_for_rate(n, fr or 0.0, self.cfg["simulation"]["pattern_weights"]) if fr else {}
        available_fresh = int(((p.role == "fresh") & np.isnan(p.created_at)).sum())
        for k in ("mule_fan_in", "new_account_burst"):
            if counts.get(k, 0) > available_fresh:
                counts[k] = available_fresh
            available_fresh -= counts.get(k, 0)
        # Live demos use compact attacks (size=0.4) so a few hundred transfers
        # contain "a few" frauds rather than one 60-transfer drain.
        fraud = inject(p, rng, t0, t1, {k: v for k, v in counts.items() if v > 0}, size=0.4)
        n_normal = max(n - len(fraud["t"]), 0)
        expected = p.rate_per_day[p.role == "normal"].sum() * hours / 24
        scale = max(n_normal / max(expected, 1e-9), 1e-6) * 1.3
        normal = normal_events(p, rng, t0, t1, rate_scale=scale)
        while len(normal["t"]) < n_normal and scale < 1e4:
            scale *= 1.6
            normal = normal_events(p, rng, t0, t1, rate_scale=scale)
        if len(normal["t"]) > n_normal:
            keep = np.sort(rng.choice(len(normal["t"]), size=n_normal, replace=False))
            normal = {k: v[keep] for k, v in normal.items()}
        ev = {k: np.concatenate([normal[k], fraud[k]]) for k in normal}
        order = np.argsort(ev["t"])
        items = []
        for seq, k in enumerate(order):
            s, r = int(ev["sender"][k]), int(ev["receiver"][k])
            items.append({
                "seq": seq,
                "sim_time": datetime.fromtimestamp(float(ev["t"][k]), timezone.utc).isoformat(),
                "sender_id": p.user_id[s], "sender_username": p.username[s],
                "receiver_id": p.user_id[r], "receiver_username": p.username[r],
                "amount": round(float(ev["amount"][k]), 2),
                "is_fraud": int(ev["is_fraud"][k]),
                "fraud_type": str(ev["fraud_type"][k]),
            })
        # Dormant accounts claimed by fraud now exist; tell the feature store.
        with self.lock:
            for i, uid in enumerate(p.user_id):
                if not np.isnan(p.created_at[i]) and uid not in self.state.created:
                    self.state.register_user(uid, p.created_at[i])
        self.sim_clock = t1
        return {
            "scenario": scenario, "label": sc["label"],
            "sim_start": datetime.fromtimestamp(t0, timezone.utc).isoformat(),
            "sim_end": datetime.fromtimestamp(t1, timezone.utc).isoformat(),
            "counts": {k: int(v) for k, v in counts.items() if v},
            "n_fraud": int(sum(i["is_fraud"] for i in items)),
            "items": items,
        }


__all__ = ["OnlineScorer", "SCENARIOS", "PATTERNS"]
