"""Train, tune and package the fraud detectors.

    python -m kronos_ml.models.train

Time split (from config.yaml): train = days [0, 60), validation = [60, 70),
test = [70, 90). Models are fit on the training slice WITHOUT labels. Labels on
the validation slice are used only to pick hyper-parameters and the alert
thresholds; the test slice is touched only by evaluation.

Outputs in ml/models/:
    <family>.joblib       best detector of each family
    deployed.joblib       bundle the risk service loads
    model_card.json       version, window, features, thresholds, metrics
    tuning.csv            every hyper-parameter setting tried
    scores.parquet        raw scores of every model on val + test (for eval)
"""
from __future__ import annotations

import json
import time
from datetime import datetime, timezone

import joblib
import numpy as np
import pandas as pd
from sklearn.metrics import average_precision_score

from ..config import DATA_DIR, MODELS_DIR, load_config
from ..eval.metrics import business_cost, full_report
from ..explain import ReasonExplainer
from ..features.build import FEATURES, build_features, load_dataset
from .calibration import Calibrator
from .detectors import FAMILIES
from .ensemble import RankEnsemble


def load_features(data_dir=None) -> pd.DataFrame:
    d = data_dir or DATA_DIR
    path = d / "features.parquet"
    if path.exists():
        return pd.read_parquet(path)
    tx, users = load_dataset(d)
    return pd.concat([tx, build_features(tx, users)], axis=1)


def split(df: pd.DataFrame, cfg: dict) -> dict[str, pd.DataFrame]:
    start = pd.Timestamp(cfg["simulation"]["start"])
    day = (pd.to_datetime(df["created_at"]) - start).dt.total_seconds() / 86400
    s = cfg["split"]
    return {
        "train": df[day < s["train_end_day"]],
        "val": df[(day >= s["train_end_day"]) & (day < s["val_end_day"])],
        "test": df[day >= s["val_end_day"]],
    }


def max_rows_for(family: str, cfg: dict) -> int:
    m = cfg["models"]
    return {"lof": m["lof_train_rows"], "ocsvm": m["ocsvm_train_rows"]}.get(family, m["max_train_rows"])


def tune(parts: dict, cfg: dict, log=print):
    rows, best = [], {}
    yv = parts["val"]["is_fraud"].to_numpy()
    for family, (cls, grid) in FAMILIES.items():
        for params in grid:
            t = time.time()
            det = cls(FEATURES, max_rows=max_rows_for(family, cfg), seed=cfg["seed"], **params).fit(parts["train"])
            ap = average_precision_score(yv, det.raw(parts["val"]))
            rows.append({"family": family, "params": json.dumps(params), "val_pr_auc": ap, "fit_s": time.time() - t})
            log(f"  {family:17s} {json.dumps(params):60s} val PR-AUC={ap:.3f} ({time.time() - t:.1f}s)")
            if family not in best or ap > best[family][1]:
                best[family] = (det, ap)
    return best, pd.DataFrame(rows)


def choose_thresholds(det, val: pd.DataFrame, cfg: dict) -> tuple[float, float, pd.DataFrame]:
    r = cfg["risk"]
    raw = det.raw(val)
    y = val["is_fraud"].to_numpy()
    amount = val["amount"].to_numpy()
    grid = []
    for rate in r["high_rate_grid"]:
        thr = float(np.quantile(raw, 1 - rate))
        c = business_cost(y, raw, amount, thr, r["review_cost"])
        grid.append({"alert_rate": rate, "threshold": thr, **c})
    grid = pd.DataFrame(grid)
    high = float(grid.loc[grid["total_cost"].idxmin(), "threshold"])
    medium = float(np.quantile(raw, 1 - r["medium_alert_rate"]))
    medium = min(medium, high - 1e-6)
    return medium, high, grid


def train(data_dir=None, out_dir=None, log=print) -> dict:
    cfg = load_config()
    out = out_dir or MODELS_DIR
    out.mkdir(parents=True, exist_ok=True)
    df = load_features(data_dir)
    parts = split(df, cfg)
    log(f"train={len(parts['train']):,} val={len(parts['val']):,} test={len(parts['test']):,} rows")

    best, tuning = tune(parts, cfg, log)
    tuning.to_csv(out / "tuning.csv", index=False)
    for family, (det, _) in best.items():
        joblib.dump(det, out / f"{family}.joblib", compress=3)

    # Ensemble of the two strongest learned detectors.
    learned = sorted(((ap, f) for f, (d, ap) in best.items() if f != "zscore_baseline"), reverse=True)
    members = [best[f][0] for _, f in learned[:2]]
    ens = RankEnsemble(members, parts["train"].sample(min(20000, len(parts["train"])), random_state=0))
    ens_ap = average_precision_score(parts["val"]["is_fraud"], ens.raw(parts["val"]))
    best["ensemble"] = (ens, ens_ap)
    joblib.dump(ens, out / "ensemble.joblib", compress=3)
    log(f"  ensemble          {ens.describe():60s} val PR-AUC={ens_ap:.3f}")

    deployed_name = max(best, key=lambda k: best[k][1])
    det = best[deployed_name][0]
    medium_raw, high_raw, cost_grid = choose_thresholds(det, parts["val"], cfg)
    cost_grid.to_csv(out / "threshold_cost.csv", index=False)
    calib = Calibrator(det.raw(parts["val"]), medium_raw, high_raw)
    explainer = ReasonExplainer(FEATURES).fit(parts["train"])

    # Scores of every model on val + test, for evaluation and the notebooks.
    scores = []
    for name, (d, _) in best.items():
        for sname in ("val", "test"):
            p = parts[sname]
            scores.append(pd.DataFrame({"tx_id": p["tx_id"].to_numpy(), "split": sname, "model": name, "raw": d.raw(p)}))
    scores = pd.concat(scores, ignore_index=True)
    scores.to_parquet(out / "scores.parquet")

    test = parts["test"]
    test_raw = det.raw(test)
    test_metrics = full_report(
        test["is_fraud"].to_numpy(), test_raw, high_raw, test["fraud_type"], test["amount"].to_numpy(),
        cfg["risk"]["review_cost"],
    )
    version = f"{deployed_name}-{datetime.now(timezone.utc).strftime('%Y%m%d%H%M%S')}"
    start = pd.Timestamp(cfg["simulation"]["start"])
    card = {
        "version": version,
        "model": deployed_name,
        "description": det.describe(),
        "trained_at": datetime.now(timezone.utc).isoformat(),
        "training_window": {
            "start": str(start.date()),
            "end": str((start + pd.Timedelta(days=cfg["split"]["train_end_day"])).date()),
            "rows": len(parts["train"]),
        },
        "features": FEATURES,
        "thresholds": {
            "medium_score": 0.5, "high_score": 0.8,
            "medium_raw": medium_raw, "high_raw": high_raw,
            "medium_alert_rate": cfg["risk"]["medium_alert_rate"],
            "high_alert_rate": float((det.raw(parts["val"]) >= high_raw).mean()),
        },
        "validation_pr_auc": {k: round(float(v[1]), 4) for k, v in best.items()},
        "test_metrics": {k: (round(float(v), 4) if isinstance(v, (float, np.floating)) else v) for k, v in test_metrics.items()},
        "labels_used_for_training": False,
    }
    bundle = {
        "version": version,
        "name": deployed_name,
        "features": FEATURES,
        "detector": det,
        "calibrator": calib,
        "explainer": explainer,
        "card": card,
    }
    joblib.dump(bundle, out / "deployed.joblib", compress=3)
    (out / "model_card.json").write_text(json.dumps(card, indent=2))
    log(f"deployed {deployed_name}: test PR-AUC={test_metrics['pr_auc']:.3f} ROC-AUC={test_metrics['roc_auc']:.3f} "
        f"recall@thr={test_metrics['recall@thr']:.3f} precision@thr={test_metrics['precision@thr']:.3f}")
    return card


def main():
    t = time.time()
    train()
    print(f"done in {time.time() - t:.0f}s")


if __name__ == "__main__":
    main()
