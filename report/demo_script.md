# Live demo script (about 4 minutes)

## Before the demo

```bash
make up                 # postgres, backend, risk service, frontend (docker)
make seed-demo          # 300 bot accounts with a 30-day history
```

Or without Docker: run Postgres and apply the three schema files, then
`make serve`, then `cd backend && RISK_SERVICE_URL=http://localhost:8000 go run ./cmd/api`,
then `cd kronos && npm run dev`.

Open http://localhost:5173 and log in as **analyst / analyst**.

## 1. The problem (30 s)

*Solar system view.* "Every transfer in Kronos is a packet that flies to the
receiver's planet and settles automatically when it lands. Until now nothing
checked whether a transfer looked like fraud. A hijacked account could drain
itself as fast as packets fly."

## 2. Launch the attack (60 s)

In the left panel, pick **Mixed attack** and press **Start**.

* 500 transfers from about 280 bots over 60 seconds. A few of them are
  fraudsters: a micro-drain, two whale transfers, an off-hours spike, a
  laundering ring, a mule account and a brand-new account bursting.
* Point at the packet colours: **green** low risk, **amber** under watch,
  **red** high risk.
* High-risk packets never take off. They sit on a red **HELD** ring above
  the sender's planet, with their funds still locked.
* The **Live alerts** ticker scrolls like a bank fraud monitor. Each line
  shows the score, who paid whom, and reason codes (`VELOCITY_CAP`,
  `AMOUNT_SPIKE`, `MULE_INFLOW`…).
* The scorecard counts **caught / watched / missed / false alarms** live. The
  demo knows which transfers were fraud; the model never does.

## 3. The fraud desk (90 s)

Click **Fraud Desk**.

* KPI strip: transfers per second, scored, held, blocked, frozen accounts.
* **Review queue**, highest risk first. Press `j`/`k` to move, then `Enter` to
  open a case.
* **Case drawer**: why it was flagged (bars and plain-English reasons), the
  sender's last 20 transfers, and the money-flow graph. For a laundering case,
  the red arrows close the A→B→C→A loop.
* **Block** one micro-drain transfer: the sender is refunded through the same
  void path the black hole uses, and a burst of particles appears in the solar
  system. Block three from the same sender and the account is **frozen**.
* **Release** a false alarm: the packet launches and settles normally.
* Charts: score distribution with the 0.5 / 0.8 band lines, alerts per minute,
  recall by pattern.

## 4. The ML behind it (60 s)

Point at the **Active model** card on the right.

* Unsupervised: Isolation Forest + Local Outlier Factor, ranked and averaged.
  It was trained on 117k simulated transfers **without ever seeing a fraud
  label**.
* Test PR-AUC 0.82 against 0.47 for a z-score baseline. It holds about 3%
  of traffic and catches 84% of fraud.
* The hold threshold was chosen by business cost: missed fraud money versus
  analyst time.
* Then open `report/results.md`: model comparison, recall per pattern
  (layering is the hardest), robustness at 0.5% and 0.1% fraud.

## 5. Normal user view (optional, 20 s)

Log out and log in as any bot (`bot_042` / `kronos-bot-2026`). Send a very
large amount to a stranger: the modal says **"Held for security review"** and
lists the reasons, the way a banking app would.
