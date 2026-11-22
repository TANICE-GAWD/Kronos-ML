# Kronos Fraud Shield. Run `make help` for the targets.
PY      ?= $(CURDIR)/.venv/bin/python
ML      := cd ml &&
GO      ?= go

.PHONY: help setup data features train eval robustness notebooks serve test test-ml test-go test-web demo seed-demo up down all

help:
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/'

setup: ## python venv + go modules + npm install
	test -d .venv || python3 -m venv .venv
	$(PY) -m pip install -q -r ml/requirements.txt
	$(PY) -m ipykernel install --user --name kronos-ml --display-name "Kronos ML" >/dev/null
	cd backend && $(GO) mod download
	cd kronos && npm ci

data: ## simulate users + transactions -> ml/data/
	$(ML) $(PY) -m kronos_ml.simulator.generate

features: ## past-only behavioural features -> ml/data/features.parquet
	$(ML) $(PY) -m kronos_ml.features.build

train: features ## tune + train baseline, IF, LOF, OCSVM, ensemble -> ml/models/
	$(ML) $(PY) -m kronos_ml.models.train

eval: ## metrics + figures -> report/results.md, report/figures/
	$(ML) $(PY) -m kronos_ml.eval.report

robustness: ## eval + re-run at 0.5% / 0.1% fraud over 5 seeds (slow)
	$(ML) $(PY) -m kronos_ml.eval.report --robustness

notebooks: ## build and execute notebooks 01-05 (outputs saved)
	$(ML) $(PY) notebooks/build_notebooks.py --execute

all: data train robustness notebooks ## the whole ML pipeline, end to end

serve: ## risk service on :8000
	$(ML) $(PY) -m uvicorn service.app:app --port 8000

test: test-ml test-go test-web ## every test suite

test-ml:
	$(ML) $(PY) -m pytest -q

test-go:
	cd backend && $(GO) test ./internal/...

test-web:
	cd kronos && npx vitest run

up: ## postgres + backend + risk service + frontend in docker
	docker compose up --build -d

down:
	docker compose down

seed-demo: ## create 300 bot users (backend must be running; logs in as analyst)
	$(PY) scripts/seed_demo.py

demo: seed-demo ## seed bots, then start a scenario from the Fraud Desk
	@echo "Open http://localhost:5173, log in as analyst / analyst, then Fraud Desk -> Mixed attack -> Start"
