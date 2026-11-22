"""Risk service contract, and the saved model scores the same after a reload."""
import uuid

import joblib
import numpy as np
import pandas as pd
import pytest

from kronos_ml.config import MODELS_DIR

pytestmark = pytest.mark.skipif(not (MODELS_DIR / "deployed.joblib").exists(), reason="run make train first")


@pytest.fixture(scope="module")
def client():
    from fastapi.testclient import TestClient

    from service.app import app

    with TestClient(app) as c:
        yield c


def test_health_and_model(client):
    h = client.get("/health").json()
    assert h["status"] == "ok" and h["model_version"]
    card = client.get("/model").json()
    assert card["labels_used_for_training"] is False
    assert len(card["features"]) >= 20


def test_score_contract(client):
    body = {"sender_id": str(uuid.uuid4()), "receiver_id": str(uuid.uuid4()), "amount": 42.0,
            "origin_planet": "Earth", "destination_planet": "Mars", "timestamp": "2026-04-01T10:00:00Z",
            "sender_balance": 1000}
    r = client.post("/score", json=body).json()
    assert 0.0 <= r["score"] <= 1.0
    assert r["band"] in ("low", "medium", "high")
    assert isinstance(r["reasons"], list)
    for reason in r["reasons"]:
        assert {"code", "text", "weight"} <= reason.keys()


def test_rule_forces_high_on_rapid_fire(client):
    sender = str(uuid.uuid4())
    last = None
    for i in range(10):
        last = client.post("/score", json={
            "sender_id": sender, "receiver_id": str(uuid.uuid4()), "amount": 5.0,
            "origin_planet": "earth", "destination_planet": "venus",
            "timestamp": f"2026-04-02T03:00:{i * 5:02d}Z", "sender_balance": 1000}).json()
    assert last["band"] == "high"
    assert any(r["code"] == "VELOCITY_CAP" for r in last["reasons"])


def test_bad_request_rejected(client):
    assert client.post("/score", json={"sender_id": "a"}).status_code == 422


def test_warmup_and_plan(client):
    planets = ["earth", "mars", "venus", "mercury", "jupiter", "saturn"]
    users = [{"id": str(uuid.uuid4()), "username": f"t_{i}", "home_planet": planets[i % 6]} for i in range(60)]
    w = client.post("/warmup", json={"users": users}).json()
    assert w["bots"] == 60 and w["history_transfers"] > 0
    p = client.post("/sim/plan", json={"scenario": "mixed_attack", "n": 200, "seed": 1}).json()
    assert len(p["items"]) == 200
    assert 0 < p["n_fraud"] < 200
    times = [i["sim_time"] for i in p["items"]]
    assert times == sorted(times)


def test_reloaded_model_gives_identical_scores(tmp_path):
    bundle = joblib.load(MODELS_DIR / "deployed.joblib")
    X = pd.DataFrame(np.random.default_rng(0).normal(size=(50, len(bundle["features"]))), columns=bundle["features"])
    a = bundle["detector"].raw(X)
    joblib.dump(bundle, tmp_path / "copy.joblib")
    b = joblib.load(tmp_path / "copy.joblib")["detector"].raw(X)
    np.testing.assert_allclose(a, b)
