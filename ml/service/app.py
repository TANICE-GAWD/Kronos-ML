"""Kronos risk-scoring service.

    uvicorn service.app:app --port 8000          (from ml/)

Endpoints
    POST /score         score one transfer before settlement
    POST /score/batch   score many (simulation bursts)
    GET  /health        liveness + model version
    GET  /model         model card (metrics, thresholds, features)
    POST /warmup        give demo bots a synthetic history
    POST /sim/plan      plan a demo scenario for the Go traffic generator
    GET  /scenarios     available scenario presets

On startup the service replays recent transactions from Postgres (read-only)
when DATABASE_URL is set, so live features see real history.
"""
from __future__ import annotations

import logging
import os
from typing import Optional

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from kronos_ml.features.online import SCENARIOS, OnlineScorer

log = logging.getLogger("risk")
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

app = FastAPI(title="Kronos Fraud Shield", version="1.0")
scorer: OnlineScorer | None = None


class ScoreRequest(BaseModel):
    tx_id: Optional[str] = None
    sender_id: str
    receiver_id: str
    amount: float = Field(gt=0)
    currency: Optional[str] = None
    origin_planet: str
    destination_planet: str
    timestamp: Optional[str] = None
    sender_balance: Optional[float] = None
    sender_created_at: Optional[str] = None
    receiver_created_at: Optional[str] = None


class BotUser(BaseModel):
    id: str
    username: str
    home_planet: str


class WarmupRequest(BaseModel):
    users: list[BotUser]
    days: int = 30
    seed: int = 7


class PlanRequest(BaseModel):
    scenario: str = "mixed_attack"
    n: int = Field(100, ge=1, le=5000)
    fraud_rate: Optional[float] = Field(None, ge=0, le=0.5)
    seed: Optional[int] = None


def get_scorer() -> OnlineScorer:
    global scorer
    if scorer is None:
        scorer = OnlineScorer(os.getenv("MODEL_PATH") or None)
        dsn = os.getenv("DATABASE_URL")
        if dsn:
            try:
                n = scorer.warm_from_db(dsn)
                log.info("replayed %d recent transactions from Postgres", n)
            except Exception as e:  # the service must still start without the DB
                log.warning("could not warm from Postgres: %s", e)
        log.info("loaded model %s", scorer.version)
    return scorer


@app.on_event("startup")
def _startup():
    get_scorer()


@app.get("/health")
def health():
    s = get_scorer()
    return {"status": "ok", "model_version": s.version, "scored": s.scored, "bots_ready": s.profiles is not None}


@app.get("/model")
def model():
    return get_scorer().bundle["card"]


@app.post("/score")
def score(req: ScoreRequest):
    return get_scorer().score(req.model_dump())


@app.post("/score/batch")
def score_batch(reqs: list[ScoreRequest]):
    s = get_scorer()
    return [s.score(r.model_dump()) for r in reqs]


@app.get("/scenarios")
def scenarios():
    return {k: {"label": v["label"], "hours": v["hours"]} for k, v in SCENARIOS.items()}


@app.post("/warmup")
def warmup(req: WarmupRequest):
    if len(req.users) < 20:
        raise HTTPException(400, "need at least 20 bot users")
    return get_scorer().warmup_bots([u.model_dump() for u in req.users], days=req.days, seed=req.seed)


@app.post("/sim/plan")
def sim_plan(req: PlanRequest):
    if req.scenario not in SCENARIOS:
        raise HTTPException(400, f"unknown scenario {req.scenario}")
    try:
        return get_scorer().plan(req.scenario, req.n, req.fraud_rate, req.seed)
    except RuntimeError as e:
        raise HTTPException(409, str(e))


@app.post("/sim/reset")
def sim_reset():
    scorer = get_scorer()
    scorer.reset()
    return {"status": "ok"}
