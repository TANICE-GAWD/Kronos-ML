# Machine Learning Fraud Shield: System Explanation & Results
*Prepared for Thapar Institute of Engineering and Technology Semester Project Evaluation*

---

## Executive Summary
**Kronos Fraud Shield** is a state-of-the-art unsupervised machine learning layer integrated directly into a peer-to-peer credit transfer system. In the cold-start environment of financial services where **labeled historical fraud data is completely unavailable**, we built a schema-faithful transaction simulator, engineered real-time past-only feature states, evaluated four unsupervised algorithms, and deployed a **Rank-Average Ensemble Model** that operates as an automated "fraud desk" inside the transaction settlement pipeline.

---

## 1. The Core Problem: Unsupervised Bootstrapping
When bootstrapping a fraud detection system for a new financial platform, we face a major challenge: **we have zero historical fraud labels**. Standard supervised learning models (like Random Forests or XGBoost) cannot be trained.

We resolved this by using **Unsupervised Anomaly Detection**:
* The model is trained **only on normal transaction behaviors** (fitting a boundary around what is legitimate).
* It flags transfers that deviate significantly from this normal boundary as anomalous.
* To evaluate our unsupervised detectors before going live, we built a schema-faithful simulator to generate realistic traffic and injected **six highly representative financial fraud patterns** purely as evaluation ground truth (never shown to the models during training).

---

## 2. Dataset Simulation & Faithful Physics
To ensure our machine learning models trained on data identical to the actual Go/Postgres production database, the simulator (`ml/kronos_ml/simulator`) matches Kronos exactly:
1. **Database Schema:** Uses identical columns (`sender_id`, `receiver_id`, `amount`, `currency`, `created_at`, `status`).
2. **Normal User Behavior (170k transfers over 90 days):**
   * **Activity Level:** Light, regular, and heavy users based on a Poisson process.
   * **Contact Preferences:** Zipf-like distribution (users pay their close friends most of the time, and pay strangers only 12% of the time).
   * **Paydays & Weekends:** Higher traffic on paydays (1st/15th of the month) and weekends.
3. **Astrophysical Flight Engine (Vectorized Physics):** 
   * A vectorized Python port of the Go orbital physics. It calculates the exact distance, flight duration, and black hole proximity for every transfer. This ensures that physical packet flight attributes can be leveraged by the model.

### Injected Fraud Patterns (Evaluation Labels Only)
To test model performance, we injected six classic financial attack vectors (accounting for ~2% of traffic):
* **Micro-draining (Account Takeover):** 15–60 tiny transfers to 5–20 strangers in minutes (tests high-frequency velocity).
* **Whale Transfer (Push-Payment Scam):** A single transfer of 10–50x the sender's median size to a stranger.
* **Odd-Hour Spike:** A daytime user sending multiple transfers during deep sleeping hours (e.g., 02:00–05:00).
* **Layering (Money Laundering):** circular hops of money (A → B → C → A) within minutes.
* **Mule Fan-In (Cashing Out):** 8–20 bot accounts sending money to a single new "mule" account.
* **New-Account Burst (Synthetic Identity):** A brand new account sending multiple large transfers within minutes.

---

## 3. Real-Time Feature Engineering & Zero-Leakage Policy
A common trap in transaction machine learning is **data leakage** (e.g., calculating features using future transactions, which would artificially inflate model scores in testing but fail in real life).

To prevent this, we implemented a strict **Zero-Leakage Policy** via `FeatureState` (`ml/kronos_ml/features/build.py`):
* The transaction log is streamed in strict time order.
* For each transfer, the model **first** computes features from the existing history and **only then** updates the history with the current transaction.
* **Bayesian Shrinkage (Prior Weight = 3):** To handle new users with no transaction history, we shrink their amount statistics toward a global population prior, stabilizing early-stage predictions.

### Engineered Feature Groups
* **Amount Features:** Log amount, transaction size vs. sender’s median, amount vs. wallet balance, and robust Z-score.
* **Velocity Features:** Number of transfers in the last 10 minutes, 1 hour, and 24 hours.
* **Counterparty/Mule Features:** Recipient account age, recipient's inflow velocity, and distinct payees in the last hour.
* **Graph/Cycle Features:** Circular round-trip loops (A → B → A) within 30 minutes, 3-hop cycles, and money pass-through ratios.
* **Astrophysical Features:** Packet distance, flight time, and black hole proximity risk (`bh_risk`).

---

## 4. The Unsupervised Machine Learning Models
We trained and evaluated four distinct anomaly detection algorithms on **Days 0–60** of the simulation:

1. **Z-Score Baseline (Statistical Baseline):** A robust statistical model that triggers alerts if a transaction's amount, velocity, or timing exceeds strict standard deviation thresholds.
2. **Isolation Forest (IF):** 
   * *How it works:* Builds 200 isolation trees. Anomalies are isolated much faster (nearer to the root of the trees) because they require fewer feature splits to separate.
   * *Strength:* Excellent at catching global outliers (extreme amounts, massive velocity spikes).
3. **Local Outlier Factor (LOF):**
   * *How it works:* Evaluates the local density of a data point's neighborhood compared to its $k$-nearest neighbors.
   * *Strength:* Outstanding at catching structural anomalies like new-account bursts and mule fan-ins.
4. **One-Class Support Vector Machine (OCSVM):**
   * *How it works:* Projects features into a high-dimensional kernel space and fits a boundary containing normal transactions, treating everything outside as an anomaly.
5. **Rank-Average Ensemble Model (Deployed):**
   * *How it works:* Takes the empirical Cumulative Distribution Function (eCDF) ranks of both **Isolation Forest** and **Local Outlier Factor** and averages them.
   * *Why it is the best:* It blends the global outlier isolation of IF with the local density clustering of LOF, compensating for each other's weaknesses.

---

## 5. Optimal Business Cost & Calibration
Instead of arbitrarily choosing a threshold, banks determine their review thresholds by **minimizing business cost**. We defined our cost function on the validation slice:

$$\text{Total Cost} = \text{Fraud Money Missed} + (15\text{ Credits} \times \text{Number of Analyst Alerts})$$

This cost curve (`report/figures/cost_curve.png`) showed that the cost-optimal point lies at the **top 3.0% of traffic**. 
* Transactions scoring **below 0.5** are approved (**Low Risk**).
* Transactions scoring **between 0.5 and 0.8** fly under watch (**Medium Risk**).
* Transactions scoring **0.8 or higher** are held in escrow (**High Risk**), prompting a manual analyst review on the **Fraud Desk**.

---

## 6. Performance Results & Key Findings
Evaluated on a completely unseen test slice (**Days 70–90**, 37,628 transfers, 2.01% fraud rate):

### 6.1 Model Performance Comparison
* **Precision/Recall True North:** In fraud detection, ROC-AUC is misleadingly high (0.92–0.98) due to severe class imbalance. We focus on **PR-AUC** (Precision-Recall Area Under Curve).

| Model | ROC-AUC | PR-AUC | Precision @Hold | Recall @Hold | F1 @Hold | Total Business Cost |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Z-Score Baseline** | 0.921 | 0.468 | 25.9% | 67.4% | 0.374 | 61,646 Credits |
| **Isolation Forest** | 0.982 | 0.750 | 48.5% | 80.9% | 0.606 | 37,120 Credits |
| **Local Outlier Factor** | 0.974 | 0.741 | **69.6%** | 69.8% | **0.697** | 36,824 Credits |
| **One-Class SVM** | 0.974 | 0.705 | 30.2% | 83.5% | 0.444 | 44,037 Credits |
| **Ensemble (Rank Avg)** | **0.986** | **0.822** | 53.1% | **0.844** | 0.652 | **31,009 Credits** (Lowest) |

* **The Ensemble Model wins** with a massive PR-AUC of **0.822**, catching **84.4% of all fraud** while maintaining a high precision of **53.1%** (meaning more than half of the held alerts are actual fraud!).
* Compared to the baseline, the Ensemble **halves the financial loss** of missed fraud.

### 6.2 Recall by Fraud Pattern (At cost-optimal threshold)
* **Micro-drain & New-account burst:** **98% to 100% caught** because the velocity spikes and account age are extremely pronounced.
* **Whale & Odd-hour spike:** **90% caught** because transaction size and odd timing represent severe global anomalies.
* **Mule fan-in:** **93% caught** due to our recipient inflow velocity features.
* **Layering (Money Laundering):** **58% caught**. Laundering is the hardest pattern to catch because each individual transfer looks completely ordinary on its own. However, our graph and circular loop feature detectors still push most of the missed hops into the **Medium Risk (Watch)** band (median score 0.64).

### 6.3 Explainability
* **Model-Agnostic Reason Codes:** When an analyst inspects a case, the system provides up to three plain-language reason codes (e.g. `VELOCITY_10M`, `MULE_INFLOW`, `ROUND_TRIP`). These are computed dynamically using localized robust Z-scores and hard rules.
* **Global SHAP Feature Importance:** SHAP values on the Isolation Forest show that `new_receiver`, `hour_rarity`, `amount_to_balance`, and `receiver_inflow_amount_1h` are the strongest drivers of the anomaly scores.

---

## 7. Our Performance Engineering & Optimizations
In this run, we made two critical engineering contributions to the system, resolving significant memory and performance bottlenecks:

### 7.1 Resolving the "Distributed Deadlock" in Reset
* **The Problem:** We implemented an automated clean-up of past simulation runs whenever a new simulation is started. Initially, our Python `/sim/reset` route called `warm_from_db` to connect to Postgres. This caused a distributed deadlock: the Go backend held table locks while deleting old transactions, which blocked the Python service's database query, while Go was blocked waiting for Python's HTTP reset response.
* **The Fix:** We stripped the database query out of Python's `reset` method. Since starting a new simulation automatically triggers a fresh 30-day warmup anyway, we re-engineered the Python reset to perform a **purely in-memory reset** in **under 1 millisecond**, completely bypassing Postgres and resolving the deadlock!

### 7.2 $100\times$ Speedup in Warmup (The Pandas Bottleneck)
* **The Problem:** The 30-day warmup for 100 bots (simulating thousands of transactions) was timing out and throwing a `context deadline exceeded` error.
* **The Culprit:** In `build.py`, the code was calling `pd.Timestamp(t, unit="s")` inside the loops of both `compute` and `update` functions. Creating thousands of heavy Pandas objects added severe overhead (40+ microseconds per iteration).
* **The Fix:** We replaced the Pandas `Timestamp` logic with standard-library `time.gmtime(t)` (written in pure C, with zero memory overhead). This optimized the loops, bringing the 30-day bots warmup time **from over 60 seconds down to under 1.5 seconds!**

### 7.3 Visual Lag Reduction
* **The Problem:** Pushing 100 transactions in 60 seconds forced the Three.js 3D orbits canvas to render high-frequency WebSockets frames, causing browser lag and crashes.
* **The Fix:** We modified the default duration of the 100-transfer simulation **from 60 seconds to 120 seconds**, cutting the rendering frequency in half and delivering a completely smooth, lag-free visual demo.
