# Fraud Shield integration with the Kronos backend

How the ML risk score sits in front of settlement without changing the physics
engine or the settlement code.

## Request path

```
POST /api/transfer
  └─ TransferService.Execute                        backend/internal/transport/transfer_service.go
       1. validate sender / receiver / planets / wallets   (unchanged rules)
       2. LockFundsInWallet  → funds move to escrow         (unchanged)
       3. CreateTransaction  → status = pending             (unchanged)
       4. risk.Client.Score  → POST {RISK_SERVICE_URL}/score   (new, 300 ms timeout)
       5a. band low / medium → Scheduler.AddPacket(p)       (unchanged launch; packet carries risk fields)
       5b. band high         → status = held, Desk.Record(case), HTTP 202 {status:"held", risk:{…}}
```

The scheduler, the physics (`engine/physics.go`) and the settle / void SQL
(`repository/transaction_repository.go`) are untouched. A held packet simply
never reaches the scheduler until an analyst releases it.

### Fail-open

If the risk service is slow (over `RISK_TIMEOUT`, default 300 ms), down or
returns an error, the transfer is recorded as `unscored`, launched as before,
and logged as `risk_unavailable`. This keeps Kronos available when the ML is
not. A real bank would more likely **fail closed above an amount threshold**;
that is a one-line change in `TransferService.score`.

## The score

`ml/service/app.py` (FastAPI) wraps `kronos_ml.features.online.OnlineScorer`:

1. Past-only features from an in-memory `FeatureState`, the same class used
   to build the training data, so offline and online features are identical
   (checked by `ml/tests/test_features.py::test_online_matches_offline`).
2. Hard rules (`kronos_ml/rules.py`): velocity cap, 20x amount cap, draining
   to a new payee.
3. The deployed detector (currently the Isolation Forest + LOF rank ensemble).
4. Calibration to 0–1 with fixed band edges: **low < 0.5 ≤ medium < 0.8 ≤ high**.
   The raw score at the cost-optimal hold threshold is pinned to 0.8.
5. Final score = `max(rule_score, model_score)`, plus up to three reason codes.
6. Only then is the transfer added to history.

On startup the service replays the last 30 days of `transactions` from
Postgres (read-only) so a restart does not reset everyone's history.

Response:

```json
{"tx_id": "…", "score": 0.93, "band": "high", "model_score": 0.93, "rule_score": 0.0,
 "reasons": [{"code": "AMOUNT_SPIKE", "text": "amount is 23.4x this sender's usual", "weight": 0.61}],
 "model_version": "ensemble-20261122…", "latency_ms": 18.2}
```

## Database (`backend/internal/db/migrations/003_risk.sql`)

Applied automatically and idempotently at backend start.

| Change | Purpose |
|---|---|
| `transactions.status` also allows `held`, `blocked` | the hold and block states |
| `risk_scores(tx_id, score, band, reasons, model_version, is_simulated, …wallet ids, truth)` | audit trail of every score; lets held cases be rebuilt after a restart |
| `fraud_reviews(tx_id, reviewer_id, decision, note, created_at)` | analyst decisions, the future source of real labels |
| `users.is_frozen` | sender frozen after 3 blocked transfers |
| index `transactions(sender_id, created_at)` | history lookups |

## Review API (analysts only: `ADMIN_USERNAMES`, default `analyst`)

| Endpoint | Effect |
|---|---|
| `GET /api/fraud/queue` | held cases (score desc) + latest alerts |
| `GET /api/fraud/stats` | desk counters, score histogram, alerts per minute |
| `GET /api/fraud/case/:txID` | case + sender's last 20 transfers + 24 h money-flow graph |
| `POST /api/fraud/:txID/release` | status → pending, packet launched from the planet's current position |
| `POST /api/fraud/:txID/block` | refund through the existing `VoidTransaction` path, status → blocked |
| `GET /api/fraud/model` | model card proxied from the risk service |
| `POST /api/sim/seed`, `/sim/start`, `/sim/stop`, `GET /sim/status` | live demo traffic |

Every decision is written to `fraud_reviews`. The desk takes a case
exclusively (`Desk.Take`), so two analysts cannot release and block the same
transfer.

## WebSocket

`StateUpdate` gains `held`, `alerts`, `riskStats` and `sim`, and each packet
carries `risk_score`, `risk_band` and `reasons`. Because the scheduler only
emits frames while packets fly, `cmd/api/main.go` adds a 700 ms heartbeat
when the sky is empty so held cases and the scorecard still update. The
transaction list in each frame is capped to the newest 300 rows.

## Demo traffic generator

`backend/internal/sim` creates bot accounts (`bot_001…`), tops their wallets
up to 5,000, asks the risk service to give them a 30-day synthetic history
(`POST /warmup`) and to plan a scenario (`POST /sim/plan`). It then replays the
plan in real time through **the same `TransferService.Execute`** as real users.
Each plan item carries a simulated timestamp, so a 36-hour scenario can be
compressed into a 60-second demo without making everyone look fast. The
simulator's ground-truth label is kept on the case for the scorecard and is
never part of `ScoreRequest`.

## Configuration

| Variable | Default | Where |
|---|---|---|
| `RISK_SERVICE_URL` | unset → scoring disabled | backend |
| `RISK_TIMEOUT` | `300ms` | backend |
| `ADMIN_USERNAMES` | `analyst` | backend |
| `ANALYST_PASSWORD` | `analyst` | backend (demo account) |
| `DATABASE_URL` | required | backend, risk service (read-only) |
| `MODEL_PATH` | `ml/models/deployed.joblib` | risk service |

## Retraining

`make data train eval` rebuilds everything from `ml/config.yaml`. To swap the
live model, restart the risk service; the model version is stored on every
score, so old and new decisions stay traceable.
