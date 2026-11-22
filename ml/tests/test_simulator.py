import numpy as np
import pandas as pd

from kronos_ml.constants import PLANET_CURRENCY
from kronos_ml.simulator.generate import build_dataset
from kronos_ml.simulator.patterns import PATTERNS


def test_schema_matches_kronos_transactions(small_dataset):
    tx, _ = small_dataset
    for col in ["tx_id", "sender_id", "receiver_id", "amount", "currency_id", "origin_planet",
                "destination_planet", "created_at", "status"]:
        assert col in tx.columns
    assert set(tx["status"]) <= {"settled", "failed"}
    assert set(tx["origin_planet"]) <= set(PLANET_CURRENCY)
    assert (tx["amount"] > 0).all()
    assert tx["tx_id"].is_unique


def test_no_self_transfers_and_time_sorted(small_dataset):
    tx, _ = small_dataset
    assert (tx["sender_id"] != tx["receiver_id"]).all()
    assert tx["created_at"].is_monotonic_increasing


def test_balances_never_negative(small_dataset):
    tx, _ = small_dataset
    # The simulator records the balance before each transfer; the transfer never exceeds it.
    assert (tx["sender_balance"] >= tx["amount"] - 1e-6).all()


def test_every_fraud_row_has_a_pattern(small_dataset):
    tx, _ = small_dataset
    fraud = tx[tx["is_fraud"] == 1]
    assert len(fraud) > 0
    assert fraud["fraud_type"].isin(PATTERNS).all()
    assert (tx.loc[tx["is_fraud"] == 0, "fraud_type"] == "none").all()


def test_fraud_rate_close_to_target():
    tx, _ = build_dataset(seed=3, n_users=300, days=40, fraud_rate=0.02)
    assert 0.01 < tx["is_fraud"].mean() < 0.04


def test_same_seed_same_data():
    a, _ = build_dataset(seed=5, n_users=80, days=10, fraud_rate=0.03)
    b, _ = build_dataset(seed=5, n_users=80, days=10, fraud_rate=0.03)
    pd.testing.assert_frame_equal(a, b)


def test_users_exist_before_they_transact(small_dataset):
    tx, users = small_dataset
    created = dict(zip(users["user_id"], pd.to_datetime(users["created_at"])))
    first_send = tx.groupby("sender_id")["created_at"].min()
    late = [u for u, t in first_send.items() if created[u] > t]
    assert not late


def test_layering_closes_a_cycle(small_dataset):
    tx, _ = small_dataset
    lay = tx[tx["fraud_type"] == "layering"]
    for _, g in lay.groupby("fraud_instance"):
        if len(g) < 3:
            continue
        assert g.iloc[0]["sender_id"] == g.iloc[-1]["receiver_id"]
        assert np.all(np.diff(g["amount"].to_numpy()) <= 0)
