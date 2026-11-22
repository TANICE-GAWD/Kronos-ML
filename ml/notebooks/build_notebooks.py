"""Build (and optionally execute) the five project notebooks.

    python notebooks/build_notebooks.py            # write .ipynb files
    python notebooks/build_notebooks.py --execute  # write and run them, saving outputs

The notebooks are generated from this file so their code stays in sync with the
kronos_ml package. Run from ml/ after `make train` and `make eval`.
"""
from __future__ import annotations

import argparse
from pathlib import Path

import nbformat as nbf

HERE = Path(__file__).resolve().parent

SETUP = """\
import sys, json, warnings
from pathlib import Path
sys.path.insert(0, str(Path.cwd().parent))
warnings.filterwarnings("ignore")
import numpy as np, pandas as pd, matplotlib.pyplot as plt, seaborn as sns
from kronos_ml.config import load_config, DATA_DIR, MODELS_DIR, REPORT_DIR
sns.set_theme(style="whitegrid", context="notebook")
pd.set_option("display.max_columns", 40); pd.set_option("display.width", 160)
cfg = load_config()"""


def md(text):
    return nbf.v4.new_markdown_cell(text.strip())


def code(text):
    return nbf.v4.new_code_cell(text.strip())


def nb_simulator():
    return [
        md("""
# 01 · Transaction simulator and exploratory analysis

Kronos has never needed fraud labels, so there is no real labelled dataset. This notebook
shows the **schema-faithful synthetic log** produced by `kronos_ml.simulator`:

* users with a home planet, activity level, contact circle and active hours,
* normal transfers (repeat contacts, log-normal amounts, daytime, weekend / payday peaks),
* packet physics from the real Go engine constants (distance, flight time, black-hole outcome),
* six injected abuse patterns, labelled **only for evaluation**.
"""),
        code(SETUP),
        code("""
tx = pd.read_csv(DATA_DIR / "transactions.csv", parse_dates=["created_at"])
users = pd.read_csv(DATA_DIR / "users.csv", parse_dates=["created_at"])
print(f"{len(tx):,} transfers, {len(users):,} users, {tx.created_at.min():%Y-%m-%d} → {tx.created_at.max():%Y-%m-%d}")
print(f"fraud rate {tx.is_fraud.mean():.2%}")
tx.head()
"""),
        md("Columns mirror the `transactions` table in `backend/internal/db/schema_enhanced.sql`; the extra physics fields come from a vectorised port of `physics.go`."),
        code("""
summary = tx.groupby("fraud_type").agg(rows=("tx_id", "size"), instances=("fraud_instance", "nunique"),
                                       median_amount=("amount", "median"), destroyed=("outcome", lambda s: (s == "destroyed").mean()))
summary["share"] = summary["rows"] / len(tx)
summary.sort_values("rows", ascending=False).round(3)
"""),
        code("""
fig, ax = plt.subplots(1, 2, figsize=(13, 4))
sns.histplot(data=tx[tx.is_fraud == 0], x="amount", log_scale=True, bins=60, ax=ax[0], color="#2a6fdb")
ax[0].set_title("Normal transfer amounts (log scale)")
sns.boxplot(data=tx, y="fraud_type", x="amount", log_scale=True, ax=ax[1], order=summary.index, fliersize=1)
ax[1].set_title("Amount by pattern")
plt.tight_layout()
"""),
        code("""
tx["hour"] = tx.created_at.dt.hour
fig, ax = plt.subplots(figsize=(10, 3.6))
for label, d in [("normal", tx[tx.is_fraud == 0]), ("odd_hour_spike", tx[tx.fraud_type == "odd_hour_spike"])]:
    d.hour.value_counts(normalize=True).sort_index().plot(ax=ax, marker="o", label=label)
ax.set(title="Hour of day (UTC): normal users sleep, compromised accounts do not", xlabel="hour", ylabel="share")
ax.legend();
"""),
        code("""
daily = tx[tx.is_fraud == 0].set_index("created_at").resample("D").size()
ax = daily.plot(figsize=(12, 3.5), title="Normal transfers per day (weekend and payday peaks)")
ax.set_ylabel("transfers");
"""),
        code("""
fig, ax = plt.subplots(1, 2, figsize=(12, 3.8))
users.home_planet.value_counts().plot.bar(ax=ax[0], title="Home planets", color="#3a9b5c")
tx.groupby("fraud_type")["outcome"].apply(lambda s: (s == "destroyed").mean()).plot.barh(ax=ax[1], color="#e08a1e",
    title="Share of packets swallowed by the black hole")
plt.tight_layout()
"""),
        md("### One example of each pattern"),
        code("""
cols = ["created_at", "sender_id", "receiver_id", "amount", "sender_balance", "fraud_type"]
for pattern in ["micro_drain", "whale", "odd_hour_spike", "layering", "mule_fan_in", "new_account_burst"]:
    inst = tx[tx.fraud_type == pattern].fraud_instance.iloc[0]
    ex = tx[tx.fraud_instance == inst][cols].copy()
    ex["sender_id"] = ex.sender_id.str[:8]; ex["receiver_id"] = ex.receiver_id.str[:8]
    print(f"\\n=== {pattern}: {len(ex)} transfers ===")
    print(ex.head(6).to_string(index=False))
"""),
        md("""
**Takeaways.** Normal behaviour is regular (contacts, amounts, hours). Each fraud pattern breaks
a *different* regularity: amount (whale), speed and fan-out (micro-drain), time of day (odd-hour),
account age (new-account burst, mule) and money flow structure (layering, mule). A single rule cannot
cover them all, which motivates multivariate anomaly detection.
"""),
    ]


def nb_features():
    return [
        md("""
# 02 · Feature engineering without label leakage

`FeatureState` streams the log in time order. For each transfer it **first computes** features from
what it has already seen and **only then** adds the transfer to history. So every feature uses
strictly earlier data, and the live risk service runs the exact same code one transfer at a time.
"""),
        code(SETUP),
        code("""
from kronos_ml.features.build import FEATURES, FEATURE_GROUPS
df = pd.read_parquet(DATA_DIR / "features.parquet")
pd.DataFrame([(g, ", ".join(f)) for g, f in FEATURE_GROUPS.items()], columns=["group", "features"])
"""),
        code("""
df[FEATURES].describe().T[["mean", "std", "min", "50%", "max"]].round(3)
"""),
        md("### How each pattern looks in feature space (median, standardised against normal traffic)"),
        code("""
normal = df[df.is_fraud == 0][FEATURES]
z = (df.groupby("fraud_type")[FEATURES].median() - normal.median()) / normal.std().replace(0, 1)
plt.figure(figsize=(14, 4.2))
sns.heatmap(z.clip(-4, 4), cmap="RdBu_r", center=0, cbar_kws={"label": "std from normal"})
plt.title("Which features move for which pattern"); plt.xticks(rotation=60, ha="right");
"""),
        code("""
key = ["amount_to_median", "tx_10m", "new_receiver", "receiver_inflow_senders_1h", "hour_rarity", "sender_age_days_log"]
fig, axes = plt.subplots(2, 3, figsize=(14, 6.5))
for ax, f in zip(axes.flat, key):
    sns.boxplot(data=df.sample(40000, random_state=0) if len(df) > 40000 else df, x=f, y="fraud_type", ax=ax, fliersize=0.5)
    ax.set_ylabel("")
plt.tight_layout()
"""),
        md("### Proving there is no leakage"),
        code("""
from kronos_ml.features.build import build_features
tx = pd.read_csv(DATA_DIR / "transactions.csv", parse_dates=["created_at"]).iloc[:20000]
users = pd.read_csv(DATA_DIR / "users.csv", parse_dates=["created_at"])
full = build_features(tx, users)
cut = 10000
future = tx.iloc[cut:].copy()
rng = np.random.default_rng(0)
for c in ["sender_id", "receiver_id", "amount", "is_fraud"]:
    future[c] = rng.permutation(future[c].to_numpy())
scrambled = build_features(pd.concat([tx.iloc[:cut], future]), users)
diff = (full.iloc[:cut] - scrambled.iloc[:cut]).abs().to_numpy().max()
print(f"max change in the first {cut:,} rows after scrambling the future: {diff}")
assert diff == 0
"""),
        md("The same property is enforced by `ml/tests/test_no_leakage.py`, and `test_features.py` checks that one-at-a-time (online) features equal the batch features."),
        code("""
plt.figure(figsize=(11, 9))
sns.heatmap(normal.sample(20000, random_state=0).corr(), cmap="vlag", center=0)
plt.title("Feature correlation (normal traffic)");
"""),
    ]


def nb_models():
    return [
        md("""
# 03 · Unsupervised models

* **Split by time**: train = days 0-60, validation = 60-70, test = 70-90.
* **Training never sees `is_fraud`.** Labels on the validation slice only pick hyper-parameters
  and the alert threshold; the test slice is only used for the final numbers.
* Detectors: z-score baseline (+ hard rules), Isolation Forest, Local Outlier Factor (novelty mode),
  One-Class SVM (on a 15k subsample, it scales badly), and a rank-average ensemble of the two best.
* Everything runs on a CPU; `make train` takes about a minute.
"""),
        code(SETUP),
        code("""
from kronos_ml.models.train import load_features, split
parts = split(load_features(), cfg)
pd.DataFrame({k: {"rows": len(v), "fraud": int(v.is_fraud.sum()), "fraud_rate": round(v.is_fraud.mean(), 4),
                  "from": v.created_at.min().date(), "to": v.created_at.max().date()} for k, v in parts.items()}).T
"""),
        code("""
tuning = pd.read_csv(MODELS_DIR / "tuning.csv")
tuning.sort_values(["family", "val_pr_auc"], ascending=[True, False]).round(3)
"""),
        code("""
best = tuning.loc[tuning.groupby("family").val_pr_auc.idxmax()].set_index("family").val_pr_auc
card = json.loads((MODELS_DIR / "model_card.json").read_text())
best["ensemble"] = card["validation_pr_auc"]["ensemble"]
ax = best.sort_values().plot.barh(figsize=(8, 3.5), color="#2a6fdb", title="Best validation PR-AUC per model family")
ax.axvline(parts["val"].is_fraud.mean(), ls="--", color="grey", label="random"); ax.legend();
"""),
        code("""
print(json.dumps({k: card[k] for k in ["version", "model", "description", "thresholds", "labels_used_for_training"]}, indent=2))
"""),
        md("### Choosing the High (hold) threshold: business cost on validation"),
        code("""
grid = pd.read_csv(MODELS_DIR / "threshold_cost.csv")
display(grid.round(1))
ax = grid.plot(x="alert_rate", y=["missed_fraud_amount", "review_cost", "total_cost"], marker="o", figsize=(8, 4))
ax.set(xlabel="share of transfers held", ylabel="credits");
"""),
        md("""
Cost = fraud money that settles because we missed it + a fixed analyst cost per alert
(`risk.review_cost` in `config.yaml`). The minimum sets how much traffic a bank-style desk should review.
"""),
    ]


def nb_eval():
    return [
        md("""
# 04 · Evaluation and error analysis (test slice, days 70-90)

Generated figures live in `report/figures/`; this notebook recomputes the key ones and digs into errors.
"""),
        code(SETUP),
        code("""
from IPython.display import Image, Markdown
res = json.loads((REPORT_DIR / "results.json").read_text())
comp = pd.DataFrame(res["comparison"]).set_index("model")
comp[["roc_auc", "pr_auc", "precision@thr", "recall@thr", "f1@thr", "alert_rate@thr", "precision@top2%", "recall@top2%",
      "missed_fraud_amount", "total_cost"]].round(3)
"""),
        code('Image(REPORT_DIR / "figures" / "roc_pr.png")'),
        code("""
pd.DataFrame(res["pattern_recall"]).round(2)
"""),
        code('Image(REPORT_DIR / "figures" / "pattern_recall.png")'),
        code('Image(REPORT_DIR / "figures" / "confusion.png")'),
        md("### What does the deployed model miss, and what does it wrongly flag?"),
        code("""
import joblib
from kronos_ml.models.train import load_features, split
from kronos_ml.features.build import FEATURES
parts = split(load_features(), cfg)
test = parts["test"].copy()
bundle = joblib.load(MODELS_DIR / "deployed.joblib")
test["raw"] = bundle["detector"].raw(test)
test["score"] = bundle["calibrator"](test["raw"])
test["flag"] = test["score"] >= 0.8
missed = test[(test.is_fraud == 1) & ~test.flag]
false_alarms = test[(test.is_fraud == 0) & test.flag]
print(f"missed {len(missed)} of {int(test.is_fraud.sum())} fraud rows; {len(false_alarms)} false alarms out of {int((test.is_fraud == 0).sum()):,} normal rows")
missed.fraud_type.value_counts()
"""),
        code("""
show = ["fraud_type", "amount", "score", "amount_to_median", "tx_10m", "new_receiver", "cycle_3hop_60m", "passthrough_ratio", "hour_rarity"]
missed[show].groupby("fraud_type").median().round(2)
"""),
        md("""
**Why layering is hard.** Each hop of a laundering ring is one ordinary-looking transfer to a new contact.
Only the *first* hop is unusual in amount, and only the *closing* hop shows the 3-hop cycle. The middle
hops are only caught through `passthrough_ratio` (forwarding money just received). Graph features over the
whole transfer network would be the next step (see Future work).
"""),
        code("""
false_alarms[show[1:]].describe().T[["mean", "50%"]].round(2)
"""),
        md("False alarms are mostly genuine but unusual behaviour: a large first payment to a new contact, or a burst of activity. A bank's analyst queue exists exactly for these."),
        code('Image(REPORT_DIR / "figures" / "cost_curve.png")'),
        md("### Robustness: lower fraud rates and other seeds"),
        code("""
rob = pd.read_csv(REPORT_DIR / "robustness.csv")
rob.groupby(["fraud_rate", "model"]).pr_auc.agg(["mean", "std"]).unstack("model").round(3)
"""),
        code('Image(REPORT_DIR / "figures" / "robustness.png")'),
        md("""
PR-AUC falls as fraud gets rarer (the random baseline *is* the fraud rate), but the ranking of models
and the gap to the z-score baseline hold. ROC-AUC barely moves, which is why PR-AUC is the headline metric.
"""),
    ]


def nb_explain():
    return [
        md("""
# 05 · Explainability

Two views:

1. **SHAP** on the Isolation Forest: which features push a transfer toward "anomalous".
2. **Reason codes** shown to analysts in the Fraud Desk: model-agnostic, computed from how far each
   feature sits on the suspicious side of normal (robust z-scores), plus any hard rule that fired.
"""),
        code(SETUP),
        code("""
import joblib, shap
from kronos_ml.models.train import load_features, split
from kronos_ml.features.build import FEATURES
from kronos_ml.explain import shap_values
parts = split(load_features(), cfg)
test = parts["test"]
iforest = joblib.load(MODELS_DIR / "isolation_forest.joblib")
sample = pd.concat([test[test.is_fraud == 1].sample(400, random_state=0), test[test.is_fraud == 0].sample(1600, random_state=0)])
sv, Xs = shap_values(iforest, sample)
shap.summary_plot(sv, pd.DataFrame(Xs, columns=FEATURES), show=False, max_display=15)
plt.title("Isolation Forest: SHAP (positive = more anomalous)"); plt.tight_layout()
"""),
        code("""
imp = pd.DataFrame(np.abs(sv), columns=FEATURES).groupby(sample.fraud_type.to_numpy()).mean()
plt.figure(figsize=(14, 4))
sns.heatmap(imp.div(imp.sum(axis=1), axis=0), cmap="mako_r")
plt.title("Share of SHAP attribution per feature, by pattern"); plt.xticks(rotation=60, ha="right");
"""),
        md("### Reason codes for one example of each pattern (what the analyst sees)"),
        code("""
from kronos_ml.rules import rule_score_one
bundle = joblib.load(MODELS_DIR / "deployed.joblib")
rows = []
for pattern in ["micro_drain", "whale", "odd_hour_spike", "layering", "mule_fan_in", "new_account_burst", "none"]:
    cand = test[test.fraud_type == pattern]
    r = cand.iloc[int(bundle["detector"].raw(cand).argmax())] if pattern != "none" else cand.iloc[0]
    feats = r[FEATURES].to_dict()
    score = max(float(bundle["calibrator"](bundle["detector"].raw(pd.DataFrame([feats]))[0])), rule_score_one(feats)[0])
    reasons = bundle["explainer"].explain(feats, rule_score_one(feats)[1])
    rows.append({"pattern": pattern, "score": round(score, 2), "reasons": " | ".join(x["text"] for x in reasons)})
pd.DataFrame(rows)
"""),
        md("""
The reason codes agree with SHAP on the big drivers (amount, velocity, new payee, inflow to the receiver,
account age) while staying cheap enough to compute on every live transfer (about a millisecond).
"""),
    ]


NOTEBOOKS = {
    "01_simulator_eda.ipynb": nb_simulator,
    "02_feature_engineering.ipynb": nb_features,
    "03_model_training.ipynb": nb_models,
    "04_evaluation_and_errors.ipynb": nb_eval,
    "05_explainability.ipynb": nb_explain,
}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--execute", action="store_true")
    a = ap.parse_args()
    for name, build in NOTEBOOKS.items():
        nb = nbf.v4.new_notebook()
        nb.cells = build()
        nb.metadata["kernelspec"] = {"name": "python3", "display_name": "Python 3", "language": "python"}
        path = HERE / name
        if a.execute:
            from nbconvert.preprocessors import ExecutePreprocessor

            ExecutePreprocessor(timeout=900, kernel_name="python3").preprocess(nb, {"metadata": {"path": str(HERE)}})
        nbf.write(nb, path)
        print(f"wrote {path.name}{' (executed)' if a.execute else ''}")


if __name__ == "__main__":
    main()
