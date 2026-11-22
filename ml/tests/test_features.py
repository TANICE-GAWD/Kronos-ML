import math

import pandas as pd

from kronos_ml.features.build import FEATURES, FeatureState, build_features, tx_records
from kronos_ml.physics import simulate_flights


def test_online_matches_offline(small_dataset):
    """The risk service feeds one transfer at a time; features must equal the batch builder."""
    tx, users = small_dataset
    offline = build_features(tx, users)
    state = FeatureState()
    created = pd.to_datetime(users["created_at"]).astype("int64").to_numpy() / 1e9
    for uid, c in zip(users["user_id"], created):
        state.register_user(uid, c)
    online = [state.process(r) for r in tx_records(tx)]
    pd.testing.assert_frame_equal(offline, pd.DataFrame(online, columns=FEATURES, index=tx.index))


def test_velocity_and_new_payee_features():
    s = FeatureState()
    base = {"sender_id": "a", "amount": 10.0, "sender_balance": 1000}
    for i in range(5):
        s.process({**base, "receiver_id": f"r{i}", "t": 1000.0 + i * 60})
    f = s.compute({**base, "receiver_id": "r0", "t": 1000.0 + 5 * 60})
    assert f["tx_10m"] == 5 and f["tx_1h"] == 5
    assert f["new_receiver"] == 0
    assert f["distinct_receivers_1h"] == 5
    g = s.compute({**base, "receiver_id": "zz", "t": 1000.0 + 5 * 60})
    assert g["new_receiver"] == 1


def test_amount_spike_is_large_for_whale():
    s = FeatureState()
    for i in range(20):
        s.process({"sender_id": "a", "receiver_id": "b", "amount": 30.0, "t": i * 3600.0, "sender_balance": 900})
    f = s.compute({"sender_id": "a", "receiver_id": "c", "amount": 900.0, "t": 21 * 3600.0, "sender_balance": 950})
    assert f["amount_to_median"] > math.log(20)
    assert f["amount_z"] > 3


def test_three_hop_cycle_detected():
    s = FeatureState()
    s.process({"sender_id": "A", "receiver_id": "B", "amount": 100, "t": 0})
    s.process({"sender_id": "B", "receiver_id": "C", "amount": 98, "t": 120})
    f = s.compute({"sender_id": "C", "receiver_id": "A", "amount": 96, "t": 240})
    assert f["cycle_3hop_60m"] == 1.0
    assert f["passthrough_ratio"] > 0.5


def test_physics_port_is_reasonable():
    r = simulate_flights(["earth"], ["mars"], [0.0])
    assert r["distance"][0] > 0
    assert r["outcome"][0] in ("settled", "destroyed")
    assert 0.3 <= r["min_dilation"][0] <= 1.0
