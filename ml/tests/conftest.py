import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


@pytest.fixture(scope="session")
def small_dataset():
    from kronos_ml.simulator.generate import build_dataset

    tx, users = build_dataset(seed=11, n_users=200, days=30, fraud_rate=0.05)
    return tx, users
