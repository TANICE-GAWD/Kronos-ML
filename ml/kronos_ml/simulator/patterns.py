"""Fraud and abuse patterns injected on top of normal traffic.

Each pattern returns rows (t, sender, receiver, amount). Labels are attached by
`inject`; they are used for evaluation only and never reach the models.

| pattern            | bank analogue                         |
|--------------------|---------------------------------------|
| micro_drain        | account takeover / card testing       |
| whale              | authorised push-payment scam          |
| odd_hour_spike     | stolen credentials used off-hours     |
| layering           | money laundering round-trips          |
| mule_fan_in        | money-mule collection account         |
| new_account_burst  | synthetic identity bust-out           |
"""
from __future__ import annotations

import numpy as np

from .users import DAY, Profiles, pick_users

MIN = 60.0
HOUR = 3600.0

# Average rows produced per instance, used to hit a target fraud rate.
AVG_ROWS = {
    "micro_drain": 37.0,
    "whale": 1.0,
    "odd_hour_spike": 5.5,
    "layering": 3.5,
    "mule_fan_in": 16.0,
    "new_account_burst": 10.0,
}
PATTERNS = list(AVG_ROWS)


def _sized(rng, lo: int, hi: int, size: float) -> int:
    """Random count in [lo, hi), shrunk by `size` (live demos use compact attacks)."""
    return max(3, int(round(int(rng.integers(lo, hi)) * size)))


def _established(p: Profiles, t: float) -> np.ndarray:
    """Normal users that existed at least 5 days before t."""
    return np.where((p.role == "normal") & (p.created_at < t - 5 * DAY))[0]


def _strangers(p: Profiles, rng, user: int, k: int, t: float) -> np.ndarray:
    pool = np.setdiff1d(_established(p, t), np.append(p.contacts[user], user))
    return pick_users(rng, pool, p.home_planet, user, k)


def _claim_fresh(p: Profiles, rng, created_at: float):
    fresh = np.where((p.role == "fresh") & np.isnan(p.created_at))[0]
    if len(fresh) == 0:
        return None
    u = int(fresh[0])
    p.created_at[u] = created_at
    p.role[u] = "fraud_account"
    return u


def micro_drain(p, rng, t0, t1, size=1.0):
    victims = _established(p, t0)
    v = int(rng.choice(victims))
    start = rng.uniform(t0, t1 - HOUR)
    n = _sized(rng, 15, 61, size)
    receivers = _strangers(p, rng, v, _sized(rng, 5, 21, size), start)
    times = np.sort(start + rng.uniform(0, 10 * MIN, n))
    amounts = np.round(p.median_amount[v] * rng.uniform(0.15, 0.6, n), 2)
    return [(t, v, int(rng.choice(receivers)), a) for t, a in zip(times, amounts)]


def whale(p, rng, t0, t1, size=1.0):
    v = int(rng.choice(_established(p, t0)))
    t = rng.uniform(t0, t1)
    r = int(_strangers(p, rng, v, 1, t)[0])
    amount = round(p.median_amount[v] * rng.uniform(10, 50), 2)
    return [(t, v, r, amount)]


def odd_hour_spike(p, rng, t0, t1, size=1.0):
    daytime = _established(p, t0)
    daytime = daytime[(p.active_center[daytime] >= 11) & (p.active_center[daytime] <= 17)]
    v = int(rng.choice(daytime))
    day = int(rng.integers(0, max(int((t1 - t0) / DAY), 1)))
    start = t0 + day * DAY + rng.uniform(2.0, 4.4) * HOUR
    n = int(rng.integers(3, 9))
    times = np.sort(start + rng.uniform(0, 40 * MIN, n))
    strangers = _strangers(p, rng, v, n, start)
    rows = []
    for t in times:
        if rng.random() < 0.7 or not len(p.contacts[v]):
            r = int(rng.choice(strangers))
        else:
            r = int(rng.choice(p.contacts[v]))
        rows.append((t, v, r, round(p.median_amount[v] * rng.uniform(1, 3), 2)))
    return rows


def layering(p, rng, t0, t1, size=1.0):
    ring_size = int(rng.choice([3, 4]))
    ring = rng.choice(_established(p, t0), size=ring_size, replace=False)
    t = rng.uniform(t0, t1 - HOUR)
    amount = p.median_amount[ring[0]] * rng.uniform(3, 8)
    rows = []
    for i in range(ring_size):
        a, b = int(ring[i]), int(ring[(i + 1) % ring_size])
        rows.append((t, a, b, round(amount, 2)))
        t += rng.uniform(1, 10) * MIN
        amount *= 1 - rng.uniform(0.01, 0.03)
    return rows


def mule_fan_in(p, rng, t0, t1, size=1.0):
    t = rng.uniform(t0, t1 - 3 * HOUR)
    mule = _claim_fresh(p, rng, t - rng.uniform(0.5, 36) * HOUR)
    if mule is None:
        return []
    k = _sized(rng, 8, 21, size)
    senders = rng.choice(_established(p, t), size=k, replace=False)
    times = np.sort(t + rng.uniform(0, HOUR, k))
    rows = [
        (tt, int(s), mule, round(p.median_amount[s] * rng.uniform(1, 2.5), 2))
        for tt, s in zip(times, senders)
    ]
    total = sum(r[3] for r in rows)
    outs = int(rng.integers(1, 4))
    cash_out = _strangers(p, rng, mule, outs, t)
    t_out = times[-1] + rng.uniform(5, 30) * MIN
    for i, r in enumerate(cash_out):
        rows.append((t_out + i * rng.uniform(1, 5) * MIN, mule, int(r), round(total * 0.95 / outs, 2)))
    return rows


def new_account_burst(p, rng, t0, t1, size=1.0):
    t = rng.uniform(t0, t1 - HOUR)
    u = _claim_fresh(p, rng, t - rng.uniform(1, 10) * MIN)
    if u is None:
        return []
    n = _sized(rng, 5, 16, size)
    times = np.sort(t + rng.uniform(0, HOUR, n))
    receivers = _strangers(p, rng, u, n, t)
    base = np.median(p.median_amount[p.role == "normal"])
    return [(tt, u, int(rng.choice(receivers)), round(base * rng.uniform(2, 5), 2)) for tt in times]


GENERATORS = {
    "micro_drain": micro_drain,
    "whale": whale,
    "odd_hour_spike": odd_hour_spike,
    "layering": layering,
    "mule_fan_in": mule_fan_in,
    "new_account_burst": new_account_burst,
}


def instances_for_rate(n_normal: int, fraud_rate: float, weights: dict) -> dict:
    """How many instances of each pattern produce roughly `fraud_rate` fraud rows."""
    if fraud_rate <= 0:
        return {k: 0 for k in weights}
    target = fraud_rate / (1 - fraud_rate) * n_normal
    total_w = sum(weights.values())
    return {k: max(1, int(round(target * w / total_w / AVG_ROWS[k]))) for k, w in weights.items()}


def inject(p: Profiles, rng, t0: float, t1: float, counts: dict, first_instance: int = 0, size: float = 1.0) -> dict:
    rows, kinds, inst = [], [], []
    k = first_instance
    for name, n in counts.items():
        for _ in range(n):
            out = GENERATORS[name](p, rng, t0, t1, size=size)
            rows += out
            kinds += [name] * len(out)
            inst += [k] * len(out)
            k += 1
    if not rows:
        empty = np.array([])
        return {
            "t": empty, "sender": empty.astype(int), "receiver": empty.astype(int),
            "amount": empty, "is_fraud": empty.astype(int),
            "fraud_type": np.array([], dtype=object), "fraud_instance": empty.astype(int),
        }
    arr = np.array(rows, dtype=float)
    return {
        "t": arr[:, 0],
        "sender": arr[:, 1].astype(int),
        "receiver": arr[:, 2].astype(int),
        "amount": arr[:, 3],
        "is_fraud": np.ones(len(rows), dtype=int),
        "fraud_type": np.array(kinds, dtype=object),
        "fraud_instance": np.array(inst),
    }


def fresh_accounts_needed(counts: dict) -> int:
    return counts.get("mule_fan_in", 0) + counts.get("new_account_burst", 0)
