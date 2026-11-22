"""Figures for the notebooks and the final report."""
from __future__ import annotations

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
import seaborn as sns  # noqa: E402
from sklearn.metrics import ConfusionMatrixDisplay, precision_recall_curve, roc_curve  # noqa: E402

PALETTE = {
    "zscore_baseline": "#8c8c8c",
    "isolation_forest": "#2a6fdb",
    "lof": "#e08a1e",
    "ocsvm": "#3a9b5c",
    "ensemble": "#c2185b",
}
LABELS = {
    "zscore_baseline": "Z-score baseline",
    "isolation_forest": "Isolation Forest",
    "lof": "Local Outlier Factor",
    "ocsvm": "One-Class SVM",
    "ensemble": "Ensemble (rank avg)",
}

sns.set_theme(style="whitegrid", context="notebook")


def _save(fig, path):
    if path:
        fig.savefig(path, dpi=150, bbox_inches="tight")
    return fig


def roc_pr_curves(y, scores: dict, path=None):
    fig, ax = plt.subplots(1, 2, figsize=(12, 5))
    for name, s in scores.items():
        fpr, tpr, _ = roc_curve(y, s)
        p, r, _ = precision_recall_curve(y, s)
        ax[0].plot(fpr, tpr, label=LABELS.get(name, name), color=PALETTE.get(name))
        ax[1].plot(r, p, label=LABELS.get(name, name), color=PALETTE.get(name))
    ax[0].plot([0, 1], [0, 1], ls="--", color="#bbb")
    ax[0].set(title="ROC curve (test)", xlabel="False positive rate", ylabel="True positive rate")
    ax[1].axhline(np.mean(y), ls="--", color="#bbb", label="fraud rate")
    ax[1].set(title="Precision-recall curve (test)", xlabel="Recall", ylabel="Precision")
    ax[1].legend(loc="upper right", fontsize=9)
    fig.tight_layout()
    return _save(fig, path)


def score_histograms(y, scores: dict, thresholds: dict | None = None, path=None):
    n = len(scores)
    fig, axes = plt.subplots(1, n, figsize=(4 * n, 3.6))
    for ax, (name, s) in zip(np.atleast_1d(axes), scores.items()):
        r = pd.Series(s).rank(pct=True).to_numpy()
        ax.hist(r[y == 0], bins=50, alpha=0.6, density=True, label="normal", color="#9bb7d4")
        ax.hist(r[y == 1], bins=50, alpha=0.7, density=True, label="fraud", color="#d64545")
        ax.set(title=LABELS.get(name, name), xlabel="score percentile", yticks=[])
        if thresholds and name in thresholds:
            ax.axvline(thresholds[name], color="k", ls="--", lw=1)
    np.atleast_1d(axes)[0].legend()
    fig.tight_layout()
    return _save(fig, path)


def confusion(y, flagged, title="", path=None, ax=None):
    own = ax is None
    if own:
        fig, ax = plt.subplots(figsize=(4, 3.6))
    ConfusionMatrixDisplay.from_predictions(
        y, flagged.astype(int), display_labels=["normal", "fraud"], ax=ax, colorbar=False, cmap="Blues", values_format=","
    )
    ax.set_title(title)
    return _save(ax.figure, path) if own else ax.figure


def confusion_grid(y, flags: dict, path=None):
    fig, axes = plt.subplots(1, len(flags), figsize=(3.8 * len(flags), 3.6))
    for ax, (name, f) in zip(np.atleast_1d(axes), flags.items()):
        confusion(y, f, LABELS.get(name, name), ax=ax)
    fig.tight_layout()
    return _save(fig, path)


def pattern_heatmap(table: pd.DataFrame, title="Recall by fraud pattern", path=None):
    fig, ax = plt.subplots(figsize=(1.4 * table.shape[1] + 2, 0.6 * table.shape[0] + 1.5))
    t = table.rename(index=LABELS)
    sns.heatmap(t, annot=True, fmt=".2f", vmin=0, vmax=1, cmap="RdYlGn", cbar=False, ax=ax)
    ax.set(title=title, xlabel="", ylabel="")
    plt.setp(ax.get_xticklabels(), rotation=25, ha="right")
    fig.tight_layout()
    return _save(fig, path)


def cost_curve(grid: pd.DataFrame, chosen_rate: float, path=None):
    fig, ax = plt.subplots(figsize=(7, 4))
    ax.plot(grid["alert_rate"] * 100, grid["missed_fraud_amount"], marker="o", label="missed fraud (credits)")
    ax.plot(grid["alert_rate"] * 100, grid["review_cost"], marker="o", label="analyst review cost")
    ax.plot(grid["alert_rate"] * 100, grid["total_cost"], marker="o", lw=2.5, color="k", label="total cost")
    ax.axvline(chosen_rate * 100, ls="--", color="#c2185b", label=f"chosen: {chosen_rate:.1%} alerts")
    ax.set(xlabel="share of transfers held for review (%)", ylabel="credits", title="Choosing the High-risk threshold (validation)")
    ax.legend()
    fig.tight_layout()
    return _save(fig, path)


def feature_importance(imp: pd.Series, title="Feature importance", path=None, top=15):
    imp = imp.sort_values(ascending=True).tail(top)
    fig, ax = plt.subplots(figsize=(7, 0.35 * len(imp) + 1.2))
    ax.barh(imp.index, imp.values, color="#2a6fdb")
    ax.set(title=title, xlabel="mean |contribution|")
    fig.tight_layout()
    return _save(fig, path)


def robustness_plot(df: pd.DataFrame, path=None):
    fig, ax = plt.subplots(figsize=(8, 4.2))
    sns.pointplot(
        data=df, x="fraud_rate", y="pr_auc", hue="model", errorbar="sd", dodge=0.4,
        palette=PALETTE, ax=ax, order=sorted(df["fraud_rate"].unique(), reverse=True),
    )
    handles, labels = ax.get_legend_handles_labels()
    ax.legend(handles, [LABELS.get(lab, lab) for lab in labels], fontsize=9)
    ax.set(title="PR-AUC across fraud rates (mean ± sd over seeds)", xlabel="fraud rate", ylabel="PR-AUC (test)")
    fig.tight_layout()
    return _save(fig, path)
