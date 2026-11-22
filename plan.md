# Kronos Fraud Shield: ML Fraud Detection for Kronos

A full machine learning project built around Kronos. Every transfer gets a fraud-risk score before it settles, the way a bank's fraud desk works. The ML side (simulated data, features, models, evaluation, explanations) can be reproduced end to end. The live app shows it working: hundreds of users send money at the same time, a few of them are fraudsters, and the frontend flags, holds and explains the suspicious transfers in real time.

> Status: **built** (all 10 phases). Not committed or pushed. Start with the [README](README.md), the [results](report/results.md) and the [final report](report/final_report.md).
>
> Where the build differs from this plan:
> - **Online features:** the risk service keeps sender history in memory with the same `FeatureState` used for training. It replays the last 30 days from Postgres (read-only) on startup instead of querying Postgres on every score, which keeps scoring at about 20 ms.
> - **Demo traffic:** the traffic generator in Go *executes* scenarios that the Python service *plans* (`POST /sim/plan`) from the training simulator. Bot transfers carry a simulated timestamp, so 36 hours of traffic play back in 60 seconds.
> - **Held transfers after a restart:** they are rebuilt from Postgres (`risk_scores` stores the wallet ids), so locked funds are never orphaned.
> - **WebSocket frames:** limited to 5 per second and capped in size. With 300 bots, the original 60 frames per second overwhelmed browsers.

---

## 1. Goals

| # | Goal | Proof it's done |
|---|------|-----------------|
| 1 | A simulator that produces Kronos-shaped transactions with known fraud patterns | `make data` writes `transactions.csv` the same way every time from a seed |
| 2 | Per-transaction features built only from earlier history (no leakage) | Leakage tests pass |
| 3 | Baseline + Isolation Forest + LOF + One-Class SVM, trained without labels | `make train` saves the model files and a comparison table |
| 4 | Scoring: precision, recall, F1, ROC-AUC and PR-AUC, broken down by fraud pattern | `make eval` writes the report and charts |
| 5 | Explanations a bank analyst can read ("amount 14x this user's usual", "new receiver") | Every score comes with its top reasons |
| 6 | A live scoring service, plus a Go check before settlement | Risky transfers get held and never settle on their own |
| 7 | A fraud-desk screen in the frontend, and a "launch attack" demo with hundreds of senders at once | Holds, alerts, scores and an analyst queue update live in the browser |
| 8 | A final report, notebooks and a demo script for submission | `report/` and the root README |

**Rules:** the physics and the settlement math stay exactly as they are. The ML part only decides whether a packet launches straight away or waits for review. If the scoring service is down, transfers still go through as today, and the miss is logged.

---

## 2. How a bank does it, and how Kronos copies it

Real banks score every payment as it happens. They act on fixed risk bands, put risky payments on hold, and send them to an analyst queue. The analyst approves or blocks each one, and those decisions become labels for retraining later. Kronos copies that flow step by step:

| Bank concept | Kronos version |
|---|---|
| Real-time transaction scoring | `POST /score` called from `TransferHandler` before `scheduler.AddPacket` |
| Risk bands: approve / step-up / hold / block | **Low** (<0.5): launch straight away · **Medium** (0.5–0.8): launch, marked "under watch" · **High** (≥0.8): **HELD**, funds stay locked, packet waits on the launch pad |
| Rules engine alongside the model | A few hard rules (velocity cap, maximum amount per hour, blocked accounts) next to the ML score; final score = max(rule, model) |
| Reason codes on every alert | Top 3 feature contributions, e.g. `AMOUNT_SPIKE`, `NEW_PAYEE`, `VELOCITY_10M`, `ODD_HOUR`, `ROUND_TRIP` |
| Analyst case queue | Fraud-desk page: held transfers sorted by score; Release / Block buttons |
| Release / block | Release: launch the packet. Block: refund the sender through the existing void path (the same one the black hole uses) |
| Account freeze | Optional: after N blocked transfers the sender is frozen, and further sends are refused |
| Analyst decisions as labels | Each release or block is saved in `fraud_reviews` and exported later as real labels for semi-supervised retraining (future work) |
| Model governance | Model version, threshold and feature list saved with every score (an audit trail) |

---

## 3. Architecture

```
          React + Three.js (kronos/)
   ┌──────────────────────────────────────────┐
   │ Solar system  · held packets glow red    │
   │ Fraud Desk    · live alerts, queue, KPIs │
   │ Simulation    · "Launch 500 transfers"   │
   └───────▲───────────────────────┬──────────┘
           │ WebSocket (state +    │ REST
           │ risk + alerts)        │
   ┌───────┴───────────────────────▼──────────┐       ┌──────────────────────┐
   │ Go / Gin backend (backend/)              │ HTTP  │ Python risk service  │
   │ TransferHandler                          │──────▶│ (ml/service, FastAPI)│
   │  validate → lock → create tx             │◀──────│ features + model     │
   │  → SCORE → low/med: AddPacket            │ score │ + rules + reasons    │
   │           high: status=held, alert       │       └──────────┬───────────┘
   │ Review API: release / block              │                  │ reads sender
   │ Scheduler / physics: UNCHANGED           │                  │ history
   └───────────────┬──────────────────────────┘                  │
                   ▼                                             ▼
               PostgreSQL  (+ risk_scores, fraud_reviews, held status)
```

The ML code lives in `ml/` (Python). Go only adds a client, one hook, review endpoints, and extra fields in the WebSocket message.

---

## 4. Repo layout (new or changed)

```
plan.md                      ← this file
README.md                    ← updated: project overview, how to run the ML + demo
Makefile                     ← data / features / train / eval / serve / demo
docker-compose.yml           ← postgres + backend + risk-service + frontend
ml/
  requirements.txt           numpy, pandas, scikit-learn, matplotlib, seaborn, shap, fastapi, uvicorn, joblib, pytest
  config.yaml                seeds, fraud rate, thresholds, feature windows
  kronos_ml/
    constants.py             planets, currencies, SpeedOfLight, Pull_r, Time_dil (mirrors physics.go)
    physics.py               orbit position, travel distance/time, dilation (port of Go engine)
    simulator/
      users.py               user profiles (home planet, balance, activity level, contact circle, active hours)
      normal.py              normal behaviour generator
      patterns.py            4 fraud patterns + 2 bonus patterns
      generate.py            CLI → data/transactions.csv (+ users.csv)
    features/
      build.py               time-ordered, past-only feature builder
      online.py              same features for a single live tx (service uses this)
    models/
      baseline.py            z-score / rule baseline
      detectors.py           IF, LOF (novelty), OCSVM wrappers with common API
      train.py               time split, tuning, saves models/*.joblib + model_card.json
      ensemble.py            optional rank-average of best detectors
    eval/
      metrics.py             P/R/F1@k, ROC-AUC, PR-AUC, per-pattern recall, cost
      plots.py               ROC/PR curves, score histograms, confusion, SHAP
      report.py              writes report/results.md + figures
    explain.py               reason codes (SHAP for IF, per-feature z-contribution fallback)
    rules.py                 hard rules engine
  service/
    app.py                   FastAPI: /score, /score/batch, /health, /model
  notebooks/
    01_simulator_eda.ipynb
    02_feature_engineering.ipynb
    03_model_training.ipynb
    04_evaluation_and_errors.ipynb
    05_explainability.ipynb
  tests/
    test_no_leakage.py  test_simulator.py  test_features.py  test_service.py
  data/      (gitignored, generated)
  models/    (saved artifacts + model_card.json)
backend/
  internal/risk/client.go       HTTP client, 300ms timeout, fail-open
  internal/transport/handlers.go  hook before AddPacket; held branch
  internal/transport/review_handlers.go  GET /fraud/queue, POST /fraud/:txID/release|block, GET /fraud/stats
  internal/transport/sim_handlers.go     POST /sim/start, /sim/stop (demo traffic generator)
  internal/sim/                    Go traffic generator using bot users
  internal/db/migrations/003_risk.sql
  internal/models/packet/packet.go  + RiskScore, RiskBand, Reasons, Held fields
kronos/src/
  pages/FraudDeskPage.jsx
  components/fraud/ RiskBadge.jsx  AlertFeed.jsx  ReviewQueue.jsx  CaseDrawer.jsx
                    KpiStrip.jsx   SimControlPanel.jsx  ModelCard.jsx
  components/StarCreditManager.jsx   held/flagged packet visuals
  components/TransferModal.jsx       shows risk result after send
report/
  final_report.md (→ PDF)   figures/   demo_script.md
docs/integration.md
```

---

## 5. Phase 1: Transaction simulator (`ml/kronos_ml/simulator`)

**Matches the real schema** (`backend/internal/db/schema_enhanced.sql`): `id, sender_id, receiver_id, amount, currency_id, origin_planet, destination_planet, created_at, status`, plus physics fields `distance, travel_time_s, min_dilation, outcome (settled|destroyed)`, computed by `physics.py` from the real constants (orbit distances and speeds, `SpeedOfLight=50`, `ArrivalThreshold=2`, `Pull_r=40`, `Time_dil=0.3`).

**Normal users** (~2,000 users, ~90 days, ~150k transactions):
- Each user gets a home planet, a starting balance (taken from `seed.go`), an activity level (light, regular or heavy), a contact circle of 3–15 people (with a preference for same-planet contacts), and personal active hours (mostly 8:00–22:00).
- Amounts: lognormal around the user's own median. Most transfers go to people they've paid before.
- Some seasonal variation (busier at weekends and on "payday" dates), so the models don't treat ordinary peaks as fraud.

**Fraud patterns** (main runs ~2% of transactions; also tested at 0.5% and 0.1%):

| Pattern | Simulation | Bank analogue |
|---|---|---|
| `micro_drain` | A taken-over account sends 15–60 small transfers to 5–20 new receivers within 10 minutes | Account takeover, card testing |
| `whale` | One transfer 10–50x the sender's median, to a new receiver, often a drain of most of the balance | Authorized push-payment scam |
| `odd_hour_spike` | A daytime-only user suddenly sends a burst between 02:00 and 05:00 | Stolen-credential use in another time zone |
| `layering` | A→B→C→(D)→A cycles within minutes, with only a small amount lost at each hop | Money laundering |
| `mule_fan_in` *(bonus)* | Many accounts pay into one new account, which then moves it all out | Mule account |
| `new_account_burst` *(bonus)* | A brand-new account sends a lot straight away | Synthetic identity |

Labels: `is_fraud` and `fraud_type` are used **only for evaluation**. Output goes to `ml/data/transactions.csv` and `users.csv`, and the same seed always gives the same data.

---

## 6. Phase 2: Feature engineering (`ml/kronos_ml/features`)

Transactions are processed in time order, and each sender's history is updated only after their transaction has been scored. So every feature only uses data from strictly before the transaction (no leakage).

| Group | Features |
|---|---|
| Amount | log amount; z-score against the sender's history; amount ÷ sender's median; amount ÷ sender's past maximum; amount ÷ current balance |
| Velocity | number of transfers in the last 10 min / 1 h / 24 h; total amount sent in the last 1 h; seconds since the sender's last transfer |
| Counterparty | new receiver yes/no; number of different receivers in the last 1 h; age of the receiver's account; how much money the receiver has taken in from different senders over the last 1 h (mule signal) |
| Graph / cycle | did the receiver pay the sender in the last 30 min (round trip); a 2–3 hop cycle check over a recent window |
| Time | hour of day as sin/cos; how unusual this hour is for the sender; weekend yes/no |
| Account | sender's account age; how many transfers the sender has ever made (warm-up flag) |
| Physics | distance between planets; expected travel time; risk that the path passes near the black hole |

- `online.py` computes the same features for one live transaction from Postgres history. A unit test checks that it matches the offline builder.
- Leakage test: change or shuffle all rows after time *t*, and the features at *t* must not change.

---

## 7. Phase 3: Models (`ml/kronos_ml/models`)

- **Split:** by time: train on days 1–60, validate on days 61–70, test on days 71–90. Training is unsupervised: the models never see `is_fraud`. Labels are used only to pick thresholds on the validation slice and to score the test slice.
- **Baseline:** the largest absolute z-score across amount, velocity and hour, plus the hard rules.
- **Detectors:** `IsolationForest`, `LocalOutlierFactor(novelty=True)`, and `OneClassSVM` (on a subsample, because it scales badly). All use the same `RobustScaler` pipeline.
- **Tuning:** small grids (contamination, n_estimators, n_neighbors, nu, gamma) scored by PR-AUC on the validation slice.
- **Optional ensemble:** average the rankings of the top 2 models.
- **Saved files:** `ml/models/<name>.joblib` and `model_card.json` (version, training window, feature list, thresholds, metrics).

---

## 8. Phase 4: Evaluation (`ml/kronos_ml/eval` + notebook 04)

- **Metrics:** ROC-AUC and PR-AUC (PR-AUC is the main one because fraud is rare); precision, recall and F1 at the chosen threshold; precision and recall at a fixed alert budget (the top 1% / 2% of transactions), which matches a bank's limited number of analysts.
- **Recall per pattern:** a table of which model catches which fraud type, and why the misses happen.
- **Business cost:** money lost to missed fraud versus analyst hours spent on false alarms, for each threshold. This is how the High-band threshold gets chosen.
- **Robustness:** rerun at 0.5% and 0.1% fraud rates and with 5 different seeds; report the mean and spread.
- **Charts:** ROC and PR curves, score histograms split into fraud and normal, confusion matrices, a recall-by-pattern heatmap, and which features matter most.
- **Explainability (notebook 05):** SHAP on Isolation Forest, plus worked examples of each fraud type with their reason codes.

**Expected results, to check against the real numbers:** Isolation Forest is the best overall. LOF is strong on micro-drain bursts. Whale and odd-hour fraud are easy to catch. Layering is the hardest, and the cycle features are what help with it.

---

## 9. Phase 5: Risk scoring service (`ml/service/app.py`)

- `POST /score` takes `{tx_id, sender_id, receiver_id, amount, currency, origin_planet, destination_planet, timestamp}` and returns `{score 0–1, band, reasons:[{code, text, weight}], model_version, latency_ms}`.
- `POST /score/batch` handles the simulation burst. `GET /health` and `GET /model` return the model card.
- It reads sender history read-only from Postgres (fast lookups, indexed on `sender_id, created_at`), and keeps a small in-memory cache for velocity counters.
- Raw scores are mapped to 0–1 using percentiles on the validation slice, so the bands mean the same thing whichever model is used.
- Target: under 50 ms per call (p95).

---

## 10. Phase 6: Go integration (`backend/`)

1. **Migration `003_risk.sql`:**
   - `transactions.status` also allows `held` and `blocked`.
   - New table `risk_scores(tx_id, score, band, reasons jsonb, model_version, created_at)`.
   - New table `fraud_reviews(tx_id, reviewer_id, decision, note, created_at)`.
   - Optional `users.is_frozen`.
2. **`internal/risk/client.go`:** calls `/score` with a 300 ms timeout. On error it treats the transfer as low risk and logs `risk_unavailable` (fail-open, so the ML can't break transfers).
3. **`TransferHandler`:** after the funds are locked and the transaction is created (around lines 141 and 162), and before `scheduler.AddPacket` (around line 194):
   - Low or medium: launch the packet as today; the risk score is attached to it.
   - High: set the status to `held`, keep the funds locked, save the packet in a `held` map (not the physics loop), send a `fraud_alert` event, and return `202 {status:"held", reasons}`.
4. **Review API** (admin role):
   - `GET /api/fraud/queue`, `GET /api/fraud/stats`.
   - `POST /api/fraud/:txID/release` hands the packet to `scheduler.AddPacket`.
   - `POST /api/fraud/:txID/block` refunds the sender through the existing `VoidTransaction` path and sets the status to `blocked`.
   - Every decision is written to `fraud_reviews`.
5. **WebSocket:** `StateUpdate` gains `held`, `alerts` (the latest N) and `riskStats` (scored, held and blocked counts, plus the score distribution). Each packet carries `risk_score`, `risk_band` and `reasons`.
6. **The scheduler and physics stay unchanged.**

---

## 11. Phase 7: Live mass-transaction simulation (demo)

The goal is to see hundreds of users sending money at the same moment, with a few fraudsters among them, and watch them get caught.

- **Bot users:** `make seed-demo` creates around 300 bot accounts spread across all 8 planets, with wallets, contact circles and some prior history, so the features have something to work with.
- **Traffic generator:** `backend/internal/sim`, started with `POST /api/sim/start {tps, duration_s, fraud_rate, patterns[]}`.
  - It runs Go goroutines that send real transfers through the same `TransferHandler` path, so the demo uses the same scoring as real transfers.
  - It reuses the normal and fraud behaviour from the Python simulator, ported to Go or driven by a script that `ml/simulator` writes out.
  - Each bot transfer is tagged with its ground truth, used only for the demo scorecard and never sent to the scorer.
- **Scenario presets:**
  - "Normal day": 50 transfers per second, no fraud.
  - "Account takeover": one bot starts draining.
  - "Laundering ring": layering cycles.
  - "Mixed attack": 500 transfers with about 3% fraud across every pattern.
- **Live scorecard:** caught, missed and false alarms update as the run goes, so the evaluation metrics are visible live during the demo.

---

## 12. Phase 8: Frontend (`kronos/src`), the bank fraud-desk view

**In the 3D scene:**
- Packet color follows the risk band: green (low), amber (medium, faint pulse), red (high).
- Held packets sit on the launch pad above the sender's planet with a red ring and a "HELD" label, and never take off.
- Blocked packets break up into particles, styled like the black-hole effect.
- Clicking any packet opens a case drawer with its score and reasons.

**Fraud Desk page** (`/fraud`, a new tab next to the solar system; split view on wide screens):
- **KPI strip:** transfers per second, scored, held, blocked, released, average score, caught/missed during a simulation, and model version.
- **Live alert feed:** a bank-style ticker, e.g. `HIGH 0.93 · @bot_142 → @bot_77 · 4,800 MARS · AMOUNT_SPIKE, NEW_PAYEE`.
- **Review queue:** a table sorted by score, with Release and Block buttons, filters by band and pattern, and keyboard shortcuts.
- **Case drawer:**
  - the transaction details;
  - a bar chart of the reasons;
  - the sender's last 20 transfers as a sparkline;
  - the sender's contact graph, with the round-trip cycle highlighted for layering;
  - Release or Block, with a note.
- **Charts:** a live histogram of scores split by band, alerts per minute, and recall by pattern during a simulation.
- **Simulation control panel:** scenario preset, number of transfers, fraud %, and Start/Stop, with a progress bar.
- **Model card panel:** active model, training date, PR-AUC/ROC-AUC, thresholds, and the comparison table from the evaluation.

**Normal users:**
- After sending, `TransferModal` shows either "Sent" or "Held for security review" with a plain-language reason, the way a bank app would.
- `TransactionHistory` gets a status chip (held, blocked or released).

Admin access: one new `role` on users (or an admin username list in the config). Demo login: `analyst / analyst`.

---

## 13. Testing

- **Python** (`pytest`): simulator invariants (balances never go negative; every fraud row has a pattern); no leakage; online and offline features match; the service contract; and a check that a model loaded from its saved file gives the same scores.
- **Go:**
  - Unit tests for the risk client, including the fail-open path.
  - A `TransferHandler` test with the risk service faked as high, low and down.
  - Release and block move funds correctly: after a block the sender's locked funds go back to their available balance.
- **Frontend:** a Vitest test that `WebSocketManager` handles the new fields, and a component test for `ReviewQueue`.
- **End to end:** run `make demo`, start the "Mixed attack" scenario, and check that held > 0, blocked transfers are refunded, and no blocked transfer ever settles.

---

## 14. Running it

```
make setup        # python venv + go mod + npm install
make data         # simulate → ml/data/
make train        # train all models → ml/models/
make eval         # metrics + figures → report/
make serve        # risk service on :8000
docker compose up # postgres + backend + risk service + frontend
make seed-demo    # bot users for live simulation
# open frontend → Fraud Desk → "Mixed attack" → Start
```

---

## 15. Deliverables for submission

1. **Code:** the `ml/` package, the Go integration, and the frontend fraud desk.
2. **Notebooks 01–05**, run in order and with their outputs saved.
3. **`report/final_report.md` → PDF:** introduction; problem; how a bank's fraud flow maps to Kronos; dataset note (synthetic data and why); simulator design; features; models; results with per-pattern errors; business cost; explainability; system design; limitations; future work.
4. **`report/demo_script.md`:** a 3–5 minute walkthrough of the live demo, plus a recorded GIF/video.
5. **`docs/integration.md`** and an updated README, including the architecture diagram.

---

## 16. Limitations to state in the report

- The data is synthetic, so the results show the method works but say nothing about real fraud rates. The report should explain how to move to real labels once the review queue produces them.
- Unsupervised detectors miss fraud that looks like normal behaviour, such as a slow layering ring.
- Fraudsters change tactics over time, so the model needs retraining. Plan: retrain every week on a rolling window, keep an eye on the score distribution, and track release/block rates.
- Fail-open was chosen so the demo stays available. A real bank would probably fail closed for high amounts.

## 17. Future work

- Semi-supervised retraining from analyst decisions, e.g. Isolation Forest combined with a gradient-boosted model trained on the labelled cases.
- Graph ML on the transfer network (GNN or community detection) for laundering rings.
- An extra check before a risky transfer goes through (a confirmation step, like a bank asking for an OTP), instead of a hard hold.
- Run the model as ONNX inside Go so there's no extra network call.

---

## 18. Milestones

| Week | Milestone |
|---|---|
| 1 | Repo scaffold, constants and physics port, simulator (normal users) |
| 2 | Fraud patterns, dataset, notebook 01 (EDA) |
| 3 | Features, leakage tests, baseline, notebook 02 |
| 4 | IF / LOF / OCSVM training and tuning, notebook 03 |
| 5 | Evaluation, per-pattern errors, cost analysis, explainability (notebooks 04–05) |
| 6 | Risk service, Go migration, client, hook, review API |
| 7 | Bot seeding and traffic generator |
| 8 | Frontend: risk visuals in the 3D scene, Fraud Desk, simulation panel |
| 9 | End-to-end tests, docker compose, polish |
| 10 | Final report, demo recording, submission |
