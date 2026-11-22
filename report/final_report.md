# Kronos Fraud Shield
### Unsupervised fraud detection for a gamified fund-transfer platform

Machine Learning semester project · Keshav Harit (1024031163) · Prince Sharma (1024030961)
B.Tech Computer Engineering, Thapar Institute of Engineering and Technology, Patiala

---

## 1. Introduction

Kronos is a peer-to-peer credit transfer platform built by our team: a Go/Gin
backend, a React/Three.js frontend and PostgreSQL. Every transfer is a
"packet" that flies from the sender's home planet to the receiver's through a
simulated solar system with real orbital mechanics and a black hole that can
swallow packets. Funds are locked when a packet launches and settle the
instant it lands. Until this project there was no risk check anywhere in that
pipeline.

**Fraud Shield** adds that check the way a bank's fraud desk works. Every
transfer is scored before it launches. Low-risk transfers fly as before,
medium-risk ones fly under watch, and high-risk ones are **held** with their
funds still locked until an analyst releases or blocks them.

## 2. Problem statement

A compromised or abusive account can drain funds as fast as packets fly, and
Kronos has no labelled fraud history to learn from. The question:

> Can we detect abnormal, potentially fraudulent transfers on Kronos using
> only **unsupervised** learning, bootstrapped from the schema Kronos already
> produces, and use the result to stop risky transfers before they settle?

## 3. How a bank's fraud flow maps onto Kronos

| Bank | Kronos Fraud Shield |
|---|---|
| Real-time payment scoring | `POST /score` from `TransferService.Execute`, before `scheduler.AddPacket` |
| Risk bands (approve / monitor / hold) | low < 0.5 ≤ medium < 0.8 ≤ high (held) |
| Rules engine next to the model | velocity cap, 20x amount cap, balance drain to a new payee; final = max(rule, model) |
| Reason codes on alerts | top-3 codes such as `AMOUNT_SPIKE`, `VELOCITY_10M`, `MULE_INFLOW`, `ROUND_TRIP` |
| Analyst case queue | Fraud Desk: review queue, case drawer, Release / Block |
| Block and refund | the existing void path the black hole already uses |
| Account freeze | sender frozen after three blocked transfers |
| Decisions become labels | every decision is stored in `fraud_reviews` |
| Model governance | model version, score, band and reasons stored for every transfer (`risk_scores`) |

## 4. Dataset: why synthetic, and how it stays faithful

Kronos's production database has never needed fraud labels, so no labelled
dataset exists. Following standard practice for bootstrapping a fraud system,
we built a **schema-faithful simulator** (`ml/kronos_ml/simulator`) and
injected known fraud patterns purely as evaluation ground truth.

**Faithful to Kronos:**

* **Columns:** the columns of the `transactions` table (sender, receiver,
  amount, currency, origin/destination planet, created_at, status).
* **Planets and currencies:** the six home planets that have a seeded currency.
* **Starting money:** the 1,000-credit registration grant.
* **Escrow:** funds lock on send, settle on arrival, and are refunded when the
  black hole destroys the packet.
* **Physics:** a vectorised port of `physics.go`: the same orbits,
  `SpeedOfLight = 50`, event horizon `Pull_r = 40`, minimum dilation 0.3,
  black hole at (0, 0, 100). It gives every transfer a distance, flight time,
  closest approach to the black hole and outcome. About 20% of packets are
  swallowed.

**Normal behaviour:** 2,000 users over 90 days, about 170k transfers.

* **Activity:** each user is light (0.25 transfers/day), regular (1/day) or
  heavy (3/day).
* **Who they pay:** each user has 3–15 contacts with a Zipf-like preference.
  12% of payments go to someone new.
* **Amounts:** log-normal around the user's own median, roughly 30 credits.
* **Timing:** users are active by day, around their own centre hour. Traffic
  is busier on weekends and around the 1st and 15th (paydays).
* **Population:** 8% of users join during the window.
* **Planet mix:** contacts, new payees and fraud targets all use the same
  planet mix (35% same planet). Otherwise packet distance alone would give
  fraud away. We caught and removed this leak during development.

**Injected abuse** (about 2% of transfers, also run at 0.5% and 0.1%):

| Pattern | Simulation | Bank analogue |
|---|---|---|
| Micro-draining | 15–60 small transfers to 5–20 strangers within 10 minutes | account takeover, card testing |
| Whale transfer | one transfer of 10–50x the sender's median to a stranger | authorised push-payment scam |
| Odd-hour spike | a daytime user sends 3–8 transfers at 02:00–05:00 | stolen credentials |
| Layering | A→B→C(→D)→A within minutes, losing 1–3% per hop | money laundering |
| Mule fan-in | 8–20 accounts pay a new account, which cashes out | money mule |
| New-account burst | a minutes-old account sends 5–15 large transfers | synthetic identity |

The labels `is_fraud` and `fraud_type` are never used for training.

## 5. Feature engineering without leakage

`FeatureState` (`ml/kronos_ml/features/build.py`) streams the log in time
order. For each transfer it **first** computes features from history and
**only then** adds the transfer to history. So every feature uses strictly
earlier data. The live risk service runs the same class one transfer at a
time, so training and serving features are identical.

| Group | Features |
|---|---|
| Amount | log amount, shrunk z-score vs the sender's history, ÷ median, ÷ past max, ÷ balance |
| Velocity | transfers in 10 min / 1 h / 24 h, amount in 1 h, time since last |
| Counterparty | new payee, distinct payees in 1 h, receiver account age, receiver inflow senders and amount in 1 h (mule) |
| Graph / cycle | 30-min round trip, 3-hop cycle in 60 min, pass-through ratio (forwarding money just received) |
| Time | hour sin/cos, hour rarity for this sender, weekend |
| Account | sender account age, transfer count, warm-up flag |
| Physics | distance, flight time, black-hole proximity |

New users have little history, so their amount statistics are shrunk toward a
population prior (Bayesian shrinkage with prior weight 3).

**Leakage tests** (`ml/tests/test_no_leakage.py`):

* Truncating the log leaves earlier features unchanged.
* Scrambling every future sender, receiver, amount and label leaves earlier
  features unchanged.
* `test_online_matches_offline` checks that one-at-a-time features equal the
  batch features.

## 6. Models

* **Split by time:** train on days 0–60 (117k transfers), validate on 60–70,
  test on 70–90.
* **No labels in training:** models are fit on the training slice without
  labels. Validation labels choose hyper-parameters (by PR-AUC) and the hold
  threshold. The test slice is used only for the final numbers.
* **Hardware:** everything runs on a CPU. `make train` takes about a minute.

| Model | Notes |
|---|---|
| Z-score baseline | largest robust z over amount, velocity and hour, lifted by the hard rules |
| Isolation Forest | 200 trees; grid over `max_samples` and `max_features` |
| Local Outlier Factor | novelty mode, trained on 25k rows; grid over `n_neighbors` (20 / 100 / 250) |
| One-Class SVM | RBF kernel, trained on 15k rows; grid over `nu` and `gamma` |
| Ensemble | average of the empirical-CDF ranks of the two best learned detectors |

All models share a `RobustScaler(10–90%)` step.

**Calibration and bands.** Raw anomaly scores are mapped to 0–1 with fixed
knots, so the bands mean the same thing whatever model is deployed. The
top-8% raw score maps to 0.5 (medium) and the cost-optimal hold threshold
maps to 0.8 (high).

**Choosing the hold threshold.** For alert rates from 0.5% to 5% of traffic
we compute, on validation:

    cost = fraud money that settles undetected + 15 credits × number of alerts

We take the minimum. This is how a bank sizes its review queue.

## 7. Results

Test slice: days 70–90, 37,628 transfers, 2.01% fraud. Full tables and every
figure are in [`results.md`](results.md); notebooks 04 and 05 reproduce them.

### 7.1 Model comparison

Each model uses its own cost-optimal hold threshold, chosen on validation.

| Model | ROC-AUC | **PR-AUC** | Precision @hold | Recall @hold | F1 @hold | Held | Recall @top-2% |
|---|---|---|---|---|---|---|---|
| Z-score baseline + rules | 0.921 | 0.468 | 0.259 | 0.674 | 0.374 | 5.2% | 0.467 |
| Isolation Forest | 0.982 | 0.750 | 0.485 | 0.809 | 0.606 | 3.4% | 0.674 |
| Local Outlier Factor | 0.974 | 0.741 | **0.696** | 0.698 | **0.697** | 2.0% | 0.697 |
| One-Class SVM | 0.974 | 0.705 | 0.302 | 0.835 | 0.444 | 5.6% | 0.638 |
| **Ensemble (IF + LOF)** | **0.986** | **0.822** | 0.531 | **0.844** | 0.652 | 3.2% | **0.759** |

* **PR-AUC is the headline metric.** With 2% fraud a random ranker scores
  0.02, and ROC-AUC looks excellent for every model (0.92–0.99), hiding real
  differences.
* **Every unsupervised detector beats the statistical baseline clearly.**
  PR-AUC rises from 0.47 to 0.71–0.82, and the money lost to missed fraud
  roughly halves.
* **The two strongest detectors are complementary.** Isolation Forest
  isolates globally unusual points (big amounts, new accounts). LOF finds
  points in sparse neighbourhoods (bursts) and is the most precise. Averaging
  their ranks gives the best PR-AUC and the lowest total cost, so the ensemble
  is the deployed model.
* **The cost-optimal desk holds about 3% of traffic.** It catches 84% of fraud
  rows, and 53% of the held transfers are fraud.

### 7.2 Which fraud each model catches

Recall per pattern at each model's hold threshold:

| | Layering | Micro-drain | Mule fan-in | New-account burst | Odd-hour spike | Whale |
|---|---|---|---|---|---|---|
| Z-score baseline | 0.52 | 0.95 | 0.33 | 0.80 | 0.82 | 0.82 |
| Isolation Forest | 0.56 | 0.95 | 0.91 | 1.00 | 0.77 | 0.80 |
| LOF | 0.38 | 0.90 | 0.85 | 0.99 | 0.50 | 0.75 |
| One-Class SVM | 0.56 | 0.93 | 0.94 | 1.00 | 0.76 | 0.92 |
| **Ensemble** | **0.58** | **0.98** | **0.93** | **1.00** | **0.79** | **0.90** |

* **Easy:** new-account bursts and micro-draining (velocity and account age
  are extreme), and mule fan-in. The baseline misses mule fan-in (0.33)
  because it has no counterparty features.
* **Hardest: layering.** Each hop is one ordinary-looking transfer. Missed
  layering hops have a high pass-through ratio (0.97) but no closed cycle yet,
  so they land in the medium band (median score 0.64) rather than the hold
  band.
* **Error analysis (notebook 04)** of the 118 missed fraud rows:
  * *Micro-drain and odd-hour misses* are mostly the **first** transfer of a
    burst, before any velocity has built up.
  * *Whale misses* had been cut down by the sender's real balance (median
    0.9x the sender's usual), so they were no longer whales.
  * *False alarms (565 of 36,870 normal transfers, 1.5%)* are genuine but
    unusual behaviour: a burst of payments, or a first payment to a new
    contact. This is what the analyst queue is for.

### 7.3 Threshold and business cost

Holding the top 3% minimises missed fraud money plus review cost on
validation (`figures/cost_curve.png`). Holding less lets more fraud settle;
holding more spends analyst time on false alarms faster than it saves money.

### 7.4 Robustness: rarer fraud and other seeds

PR-AUC, mean ± std over 5 seeds (800-user datasets, tuned settings):

| Fraud rate | Baseline | IF | LOF | OCSVM | Ensemble |
|---|---|---|---|---|---|
| 2.0% | 0.51 ± 0.08 | 0.74 ± 0.06 | 0.75 ± 0.07 | 0.73 ± 0.05 | **0.81 ± 0.04** |
| 0.5% | 0.32 ± 0.10 | 0.56 ± 0.10 | 0.70 ± 0.04 | 0.64 ± 0.06 | **0.70 ± 0.05** |
| 0.1% | 0.27 ± 0.32 | 0.34 ± 0.39 | **0.51 ± 0.29** | 0.42 ± 0.34 | 0.39 ± 0.39 |

* **At 2% and 0.5% fraud the ranking holds,** and the gap to the baseline
  stays large.
* **At 0.1% the test slices contain only a handful of attack instances,** so
  the spread is huge and differences are not significant. LOF degrades most
  gracefully. Real fraud rates are closer to this regime, which is why the
  report treats these numbers as a proof of method, not a forecast.

### 7.5 Live demo results

Running the **Mixed attack** scenario (500 transfers from about 280 bots in
60 seconds, 31–45 of them fraudulent) through the real Go transfer path on a
fresh database held about **70–85% of the fraudulent transfers**. Most of the
rest were flagged medium. False alarms ranged from **1 to 45** across runs
(0.2–10% of normal traffic): bots have only a 30-day synthetic history, and
each run draws a new scenario. Every number is visible on the live scorecard.


## 8. Explainability

* **For analysts:** each score comes with up to three reason codes in plain
  language, such as "amount is 23.4x this sender's usual", "receiver collected
  money from 9 senders in the last hour" or "rule: 8+ transfers in 10
  minutes". They are computed model-agnostically from robust z-scores against
  normal traffic, plus any rule that fired, and cost about a millisecond.
* **For the report:** SHAP on the Isolation Forest (notebook 05) shows
  `new_receiver`, `hour_rarity`, `amount_to_balance`, sender history size and
  receiver inflow among the top drivers. It also spends attribution on weakly
  informative features (`bh_risk`, `is_weekend`, `flight_time_s`): random
  splits treat every dimension equally. That is one reason LOF complements it,
  and feature selection is listed as future work.

## 9. System design

```
React + Three.js ── REST ──▶ Go / Gin TransferService ── POST /score ──▶ Python FastAPI risk service
       ▲                         │  lock → pending → score                 (FeatureState + model + rules)
       └──── WebSocket ◀─────────┤  low/medium: AddPacket (physics unchanged)
         held, alerts,           │  high: status=held, funds locked
         riskStats, sim          ▼
                           PostgreSQL (+ risk_scores, fraud_reviews, held/blocked, is_frozen)
```

* **Fail-open:** if the risk service is slow (over 300 ms) or down, the
  transfer launches unscored and is logged.
* **Held transfers survive restarts:** they are rebuilt from Postgres.
* **Full design:** `docs/integration.md`.

**Live demo.**

* **Bots:** about 300 bot accounts get a 30-day synthetic history inside the
  risk service.
* **Scenarios:** the service plans one of four scenarios (normal day, account
  takeover, laundering ring, mixed attack) with the same simulator used for
  training. The Go traffic generator replays it in real time through the same
  transfer path as real users.
* **Compressed time:** each transfer carries a simulated timestamp, so 36
  hours of traffic fit in a 60-second demo without everyone looking "fast".
* **Labels stay hidden:** the simulator's labels are shown only on the demo
  scorecard. They are never sent to the scorer.

**Frontend.**

* **Solar system:** packets are coloured by risk band. Held packets wait on a
  red ring above the sender's planet, and blocked ones break apart.
* **Live overlay:** a bank-style alert ticker and simulation launcher.
* **Fraud Desk page:** KPIs, the review queue (keyboard driven), a case drawer
  with reasons, the sender's history sparkline and a money-flow graph that
  highlights laundering loops, score and alert charts, and the model card.

## 10. Limitations

* **Synthetic data.** The results show the method works on realistic
  Kronos-shaped traffic, not real fraud rates. Real fraud is rarer and more
  adaptive. The robustness study shows PR-AUC falling as fraud gets rarer.
* **Layering is the hardest pattern.** Each hop looks ordinary on its own.
  Slow rings spread over hours would escape the 30/60-minute windows.
* **Unsupervised detectors flag "unusual", not "fraudulent".** Genuine but
  rare behaviour (a first large payment to a new friend) becomes a false
  alarm. The analyst queue exists for exactly this.
* **Drift.** Fraudsters adapt. The model should be retrained on a rolling
  window, and the score distribution and release/block rates monitored.
* **Fail-open** favours availability. A real bank would likely fail closed
  above an amount threshold.
* **Live variance.** In live demo runs the share of normal transfers held
  varies between runs. Bots have only 30 days of synthetic history and
  scenarios differ.

## 11. Future work

* **Semi-supervised retraining:** use analyst decisions from `fraud_reviews`
  as labels, for example Isolation Forest plus a gradient-boosted model on
  the reviewed cases.
* **Graph ML** on the transfer network (community detection, GNNs) for
  laundering rings.
* **Step-up verification** (an OTP-style confirmation) for medium risk
  instead of a hard hold.
* **Score inside Go** (for example an ONNX export of the forest) to remove the
  network hop.

## 12. Reproducing

```bash
make setup && make all   # data → features → train → eval + robustness → notebooks
make test                # pytest (23) + go test + vitest
make up && make seed-demo
```

Artefacts:

* `ml/models/model_card.json`
* `report/results.md`
* `report/figures/`
* `ml/notebooks/01–05`
