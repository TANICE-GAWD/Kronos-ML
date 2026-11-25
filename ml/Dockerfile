# Kronos Fraud Shield risk service. Needs trained models in ml/models (make train).
FROM python:3.12-slim
WORKDIR /app
COPY requirements-service.txt .
RUN pip install --no-cache-dir -r requirements-service.txt
COPY config.yaml .
COPY kronos_ml ./kronos_ml
COPY service ./service
COPY models/deployed.joblib models/model_card.json ./models/
EXPOSE 8000
CMD ["sh", "-c", "uvicorn service.app:app --host 0.0.0.0 --port ${PORT:-8000}"]
