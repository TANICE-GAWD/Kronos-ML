"""Loads ml/config.yaml once and exposes repo paths."""
from __future__ import annotations

from functools import lru_cache
from pathlib import Path

import yaml

ML_DIR = Path(__file__).resolve().parents[1]
REPO_DIR = ML_DIR.parent
DATA_DIR = ML_DIR / "data"
MODELS_DIR = ML_DIR / "models"
REPORT_DIR = REPO_DIR / "report"
FIGURES_DIR = REPORT_DIR / "figures"


@lru_cache(maxsize=1)
def load_config(path: str | None = None) -> dict:
    with open(path or ML_DIR / "config.yaml") as fh:
        return yaml.safe_load(fh)
