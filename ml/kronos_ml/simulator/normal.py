"""Normal (legitimate) transfer behaviour."""
from __future__ import annotations

import numpy as np

from .users import DAY, Profiles, pick_users


def day_multipliers(t0: float, n_days: int) -> np.ndarray:
    """Busier at weekends and around paydays (1st and 15th of the month)."""
    import pandas as pd

    days = pd.to_datetime(t0 + np.arange(n_days) * DAY, unit="s")
    mult = np.ones(n_days)
    mult[days.dayofweek >= 5] *= 1.3
    mult[np.isin(days.day, [1, 2, 15, 16])] *= 1.5
    return mult


def normal_events(
    profiles: Profiles,
    rng: np.random.Generator,
    t0: float,
    t1: float,
    rate_scale: float = 1.0,
    new_contact_p: float = 0.12,
) -> dict:
    """Generate legitimate transfers for every created, normal user in [t0, t1)."""
    n_days = int(np.ceil((t1 - t0) / DAY))
    mult = day_multipliers(t0, n_days)
    users = np.where(profiles.role == "normal")[0]
    lam = profiles.rate_per_day[users, None] * mult[None, :] * rate_scale
    counts = rng.poisson(lam)
    # No activity before the account exists.
    day_start = t0 + np.arange(n_days) * DAY
    created = profiles.created_at[users]
    counts[created[:, None] > day_start[None, :] + DAY] = 0

    u_idx, d_idx = np.nonzero(counts)
    reps = counts[u_idx, d_idx]
    sender = np.repeat(users[u_idx], reps)
    day = np.repeat(d_idx, reps)
    n = len(sender)

    hour = rng.normal(profiles.active_center[sender], profiles.active_spread[sender])
    hour = np.clip(hour, 6.0, 23.5)
    t = t0 + day * DAY + hour * 3600.0 + rng.uniform(0, 60, n)
    keep = (t >= np.maximum(profiles.created_at[sender], t0)) & (t < t1)
    sender, t = sender[keep], t[keep]
    n = len(sender)

    amount = rng.lognormal(np.log(profiles.median_amount[sender]), profiles.amount_sigma[sender])
    amount = np.round(np.clip(amount, 0.5, None), 2)

    receiver = np.empty(n, dtype=int)
    normal_pool = users
    use_new = rng.random(n) < new_contact_p
    for i in range(n):
        s = sender[i]
        c = profiles.contacts[s]
        if len(c) and not use_new[i]:
            receiver[i] = c[rng.choice(len(c), p=profiles.contact_weights[s])]
        else:
            receiver[i] = pick_users(rng, normal_pool, profiles.home_planet, s, 1)[0]
    return {
        "t": t,
        "sender": sender,
        "receiver": receiver,
        "amount": amount,
        "is_fraud": np.zeros(n, dtype=int),
        "fraud_type": np.array(["none"] * n, dtype=object),
        "fraud_instance": np.full(n, -1),
    }
