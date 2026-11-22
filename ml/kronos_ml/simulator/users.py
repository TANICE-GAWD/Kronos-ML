"""User profiles for the simulator.

A profile describes how a Kronos user normally behaves: where they live, how
often they send, how much, at what time of day, and to whom. The same profiles
drive the offline dataset and the live demo bots.
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field

import numpy as np

from ..constants import HOME_PLANET_WEIGHTS, PLANET_CURRENCY

DAY = 86400.0

# activity level -> (share of users, mean transfers per day)
ACTIVITY_LEVELS = {
    "light": (0.50, 0.25),
    "regular": (0.35, 1.0),
    "heavy": (0.15, 3.0),
}


@dataclass
class Profiles:
    user_id: list
    username: list
    home_planet: np.ndarray
    created_at: np.ndarray          # unix seconds; NaN = not created yet (fresh pool)
    activity: np.ndarray
    rate_per_day: np.ndarray
    median_amount: np.ndarray
    amount_sigma: np.ndarray
    active_center: np.ndarray        # hour of day
    active_spread: np.ndarray        # hours
    contacts: list                   # list[np.ndarray of user indices]
    contact_weights: list
    role: np.ndarray                 # "normal" | "fresh" (reserved for mule / new-account fraud)
    extra: dict = field(default_factory=dict)

    def __len__(self) -> int:
        return len(self.user_id)

    @property
    def currency(self) -> np.ndarray:
        return np.array([PLANET_CURRENCY[p] for p in self.home_planet])

    def to_frame(self):
        import pandas as pd

        return pd.DataFrame(
            {
                "user_id": self.user_id,
                "username": self.username,
                "home_planet": self.home_planet,
                "currency_id": self.currency,
                "created_at": pd.to_datetime(self.created_at, unit="s"),
                "activity": self.activity,
                "rate_per_day": self.rate_per_day,
                "median_amount": self.median_amount,
                "active_center": self.active_center,
                "role": self.role,
            }
        )


def new_uuid(rng: np.random.Generator) -> str:
    return str(uuid.UUID(bytes=rng.bytes(16), version=4))


SAME_PLANET_SHARE = 0.35


def pick_users(rng, pool: np.ndarray, home: np.ndarray, anchor: int, k: int) -> np.ndarray:
    """Pick k distinct users from `pool`, ~35% from the anchor's home planet.

    Contacts, new payees and fraud targets all use this, so the planet mix (and
    therefore packet distance) does not by itself give fraud away.
    """
    pool = pool[pool != anchor]
    if len(pool) == 0 or k <= 0:
        return np.array([], dtype=int)
    same = pool[home[pool] == home[anchor]]
    other = pool[home[pool] != home[anchor]]
    n_same = min(len(same), int(rng.binomial(k, SAME_PLANET_SHARE)))
    n_other = min(len(other), k - n_same)
    out = []
    if n_same:
        out.append(rng.choice(same, size=n_same, replace=False))
    if n_other:
        out.append(rng.choice(other, size=n_other, replace=False))
    return np.concatenate(out).astype(int) if out else np.array([], dtype=int)


def _pick_contacts(rng, n_users, home, idx, eligible):
    k = int(rng.integers(3, 16))
    chosen = pick_users(rng, eligible, home, idx, k)
    rng.shuffle(chosen)
    # Zipf-like preference: a few favourite contacts get most of the money.
    w = 1.0 / np.arange(1, len(chosen) + 1) ** 1.1
    return chosen, w / w.sum()


def make_profiles(
    rng: np.random.Generator,
    n_users: int,
    t_start: float,
    days: int,
    user_ids=None,
    usernames=None,
    home_planets=None,
    n_fresh: int = 0,
    late_join_share: float = 0.08,
) -> Profiles:
    """Build `n_users` normal profiles plus `n_fresh` reserved fresh accounts.

    Fresh accounts have no history and no created_at yet; fraud patterns
    (mule_fan_in, new_account_burst) claim them and set created_at.
    """
    total = n_users + n_fresh
    planets = np.array(list(HOME_PLANET_WEIGHTS))
    pw = np.array(list(HOME_PLANET_WEIGHTS.values()))
    home = (
        np.array([str(p).lower() for p in home_planets])
        if home_planets is not None
        else rng.choice(planets, size=total, p=pw / pw.sum())
    )
    levels = list(ACTIVITY_LEVELS)
    shares = np.array([ACTIVITY_LEVELS[l][0] for l in levels])
    activity = rng.choice(levels, size=total, p=shares)
    rate = np.array([ACTIVITY_LEVELS[a][1] for a in activity]) * rng.lognormal(0, 0.25, total)
    median = rng.lognormal(np.log(30.0), 0.6, total)
    sigma = rng.uniform(0.35, 0.6, total)
    center = np.clip(rng.normal(14.0, 2.5, total), 9.0, 19.0)
    spread = rng.uniform(2.0, 3.5, total)

    created = np.full(total, np.nan)
    # Most users existed long before the window; a few join during it.
    late = rng.random(n_users) < late_join_share
    created[:n_users] = np.where(
        late,
        t_start + rng.uniform(0, max(days - 10, 1), n_users) * DAY,
        t_start - rng.uniform(30, 400, n_users) * DAY,
    )
    role = np.array(["normal"] * n_users + ["fresh"] * n_fresh)

    eligible = np.arange(n_users)
    contacts, weights = [], []
    for i in range(total):
        if i < n_users:
            c, w = _pick_contacts(rng, total, home, i, eligible)
        else:
            c, w = np.array([], dtype=int), np.array([])
        contacts.append(c)
        weights.append(w)

    ids = list(user_ids) if user_ids is not None else [new_uuid(rng) for _ in range(total)]
    names = (
        list(usernames)
        if usernames is not None
        else [f"user_{i:04d}" if i < n_users else f"acct_{i:04d}" for i in range(total)]
    )
    return Profiles(
        user_id=ids,
        username=names,
        home_planet=home,
        created_at=created,
        activity=activity,
        rate_per_day=rate,
        median_amount=median,
        amount_sigma=sigma,
        active_center=center,
        active_spread=spread,
        contacts=contacts,
        contact_weights=weights,
        role=role,
    )
