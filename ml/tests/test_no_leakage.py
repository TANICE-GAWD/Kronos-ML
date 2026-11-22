"""Features at time t must depend only on transfers strictly before t."""
import numpy as np
import pandas as pd

from kronos_ml.features.build import FEATURES, build_features


def test_future_rows_do_not_change_past_features(small_dataset):
    tx, users = small_dataset
    full = build_features(tx, users)
    cut = len(tx) // 2
    head = build_features(tx.iloc[:cut], users)
    pd.testing.assert_frame_equal(full.iloc[:cut], head, check_exact=False, atol=1e-9)


def test_shuffling_the_future_does_not_change_the_past(small_dataset):
    tx, users = small_dataset
    cut = len(tx) // 2
    future = tx.iloc[cut:].copy()
    rng = np.random.default_rng(0)
    # Scramble everything about the future except its timestamps.
    for col in ["sender_id", "receiver_id", "amount", "is_fraud", "fraud_type"]:
        future[col] = rng.permutation(future[col].to_numpy())
    scrambled = pd.concat([tx.iloc[:cut], future])
    a = build_features(tx, users).iloc[:cut]
    b = build_features(scrambled, users).iloc[:cut]
    pd.testing.assert_frame_equal(a, b, check_exact=False, atol=1e-9)


def test_labels_are_not_features():
    assert "is_fraud" not in FEATURES and "fraud_type" not in FEATURES


def test_first_transfer_has_no_history(small_dataset):
    tx, users = small_dataset
    X = build_features(tx.iloc[:1], users)
    row = X.iloc[0]
    assert row["tx_1h"] == 0 and row["tx_24h"] == 0
    assert row["new_receiver"] == 1 and row["warmup"] == 1
