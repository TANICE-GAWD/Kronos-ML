"""Build the synthetic Kronos transaction log.

    python -m kronos_ml.simulator.generate            # uses ml/config.yaml
    python -m kronos_ml.simulator.generate --fraud-rate 0.005 --seed 7 --out data/rate_0005

Output columns follow the `transactions` table in
backend/internal/db/schema_enhanced.sql, plus packet physics fields and the
evaluation-only labels (is_fraud, fraud_type, fraud_instance).
"""
from __future__ import annotations

import argparse
import time
from pathlib import Path

import numpy as np
import pandas as pd

from ..config import DATA_DIR, load_config
from ..constants import PLANET_CURRENCY
from ..physics import simulate_flights
from .normal import normal_events
from .patterns import fresh_accounts_needed, inject, instances_for_rate
from .users import DAY, Profiles, make_profiles, new_uuid


def _concat(*parts: dict) -> dict:
    return {k: np.concatenate([p[k] for p in parts]) for k in parts[0]}


def apply_balances(ev: dict, p: Profiles, initial_balance: float, payday_credit: float, t0: float, t1: float, rng):
    """Walk transfers in time order like the Kronos escrow ledger.

    Funds leave the sender on send; they reach the receiver only if the packet
    settles (a black-hole loss refunds the sender). Transfers larger than the
    balance are scaled down (normal users) or capped (fraud) so no balance ever
    goes negative. Returns kept row mask, final amounts and sender balance before each row.
    """
    n_users = len(p)
    balance = np.full(n_users, initial_balance)
    paydays = []
    d = pd.Timestamp(t0, unit="s").normalize()
    while d.timestamp() < t1:
        if d.day in (1, 15):
            paydays.append(d.timestamp())
        d += pd.Timedelta(days=1)
    pi = 0
    # A capped layering hop shrinks the rest of its ring, so later hops still
    # forward (slightly less than) what they received.
    ring_scale: dict[int, float] = {}
    keep = np.ones(len(ev["t"]), dtype=bool)
    amounts = ev["amount"].copy()
    before = np.zeros(len(ev["t"]))
    for i in range(len(ev["t"])):
        t = ev["t"][i]
        while pi < len(paydays) and paydays[pi] <= t:
            created = p.created_at <= paydays[pi]
            balance[created] += payday_credit
            pi += 1
        s, r = ev["sender"][i], ev["receiver"][i]
        a = amounts[i]
        inst = int(ev["fraud_instance"][i])
        if ev["fraud_type"][i] == "layering" and inst in ring_scale:
            a = round(a * ring_scale[inst], 2)
        b = balance[s]
        if a > b:
            if ev["is_fraud"][i]:
                capped = round(b * 0.97, 2)
                if ev["fraud_type"][i] == "layering":
                    ring_scale[inst] = ring_scale.get(inst, 1.0) * capped / a
                a = capped
            else:
                a = round(b * rng.uniform(0.2, 0.7), 2)
            if a < 0.5:
                keep[i] = False
                continue
        amounts[i] = a
        before[i] = b
        balance[s] -= a
        if ev["outcome"][i] == "settled":
            balance[r] += a
    return keep, amounts, before


def build_dataset(
    seed: int | None = None,
    n_users: int | None = None,
    days: int | None = None,
    fraud_rate: float | None = None,
    start: str | None = None,
) -> tuple[pd.DataFrame, pd.DataFrame]:
    cfg = load_config()
    sim = cfg["simulation"]
    seed = cfg["seed"] if seed is None else seed
    n_users = n_users or sim["n_users"]
    days = days or sim["days"]
    fraud_rate = sim["fraud_rate"] if fraud_rate is None else fraud_rate
    t0 = pd.Timestamp(start or sim["start"]).timestamp()
    t1 = t0 + days * DAY
    rng = np.random.default_rng(seed)

    # First pass sizes the fresh-account pool, so build profiles with a generous pool.
    probe = make_profiles(np.random.default_rng(seed), n_users, t0, days)
    n_normal_est = int(probe.rate_per_day.sum() * days * 1.1)
    counts = instances_for_rate(n_normal_est, fraud_rate, sim["pattern_weights"])
    p = make_profiles(rng, n_users, t0, days, n_fresh=fresh_accounts_needed(counts))

    normal = normal_events(p, rng, t0, t1)
    counts = instances_for_rate(len(normal["t"]), fraud_rate, sim["pattern_weights"])
    fraud = inject(p, rng, t0 + 3 * DAY, t1, counts)
    ev = _concat(normal, fraud)
    order = np.lexsort((ev["sender"], ev["t"]))
    ev = {k: v[order] for k, v in ev.items()}

    flights = simulate_flights(p.home_planet[ev["sender"]], p.home_planet[ev["receiver"]], ev["t"])
    ev.update(flights)
    keep, amounts, before = apply_balances(
        ev, p, sim["initial_balance"], sim["payday_credit"], t0, t1, rng
    )
    ev["amount"] = amounts
    ev["sender_balance"] = before
    ev = {k: v[keep] for k, v in ev.items()}

    n = len(ev["t"])
    ids = [new_uuid(rng) for _ in range(n)]
    uid = np.array(p.user_id)
    tx = pd.DataFrame(
        {
            "tx_id": ids,
            "sender_id": uid[ev["sender"]],
            "receiver_id": uid[ev["receiver"]],
            "amount": np.round(ev["amount"], 2),
            "currency_id": [PLANET_CURRENCY[h] for h in p.home_planet[ev["sender"]]],
            "origin_planet": p.home_planet[ev["sender"]],
            "destination_planet": p.home_planet[ev["receiver"]],
            "created_at": pd.to_datetime(ev["t"], unit="s"),
            "status": np.where(ev["outcome"] == "settled", "settled", "failed"),
            "distance": ev["distance"].round(3),
            "travel_time_s": ev["travel_time_s"].round(3),
            "min_bh_dist": ev["min_bh_dist"].round(3),
            "min_dilation": ev["min_dilation"].round(4),
            "outcome": ev["outcome"],
            "sender_balance": ev["sender_balance"].round(2),
            "is_fraud": ev["is_fraud"],
            "fraud_type": ev["fraud_type"],
            "fraud_instance": ev["fraud_instance"],
        }
    )
    users = p.to_frame()
    users = users[users["created_at"].notna()].reset_index(drop=True)
    tx.attrs["counts"] = counts
    return tx, users


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--seed", type=int)
    ap.add_argument("--users", type=int)
    ap.add_argument("--days", type=int)
    ap.add_argument("--fraud-rate", type=float)
    ap.add_argument("--out", type=Path, default=DATA_DIR)
    a = ap.parse_args(argv)
    t = time.time()
    tx, users = build_dataset(a.seed, a.users, a.days, a.fraud_rate)
    a.out.mkdir(parents=True, exist_ok=True)
    tx.to_csv(a.out / "transactions.csv", index=False)
    users.to_csv(a.out / "users.csv", index=False)
    print(
        f"wrote {len(tx):,} transactions ({tx.is_fraud.mean():.2%} fraud) and {len(users):,} users "
        f"to {a.out} in {time.time() - t:.1f}s"
    )
    print(tx[tx.is_fraud == 1].fraud_type.value_counts().to_string())


if __name__ == "__main__":
    main()
