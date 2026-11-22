-- Kronos Fraud Shield: risk scores, analyst reviews and the held / blocked statuses.
-- Idempotent: safe to run on every start (cmd/api runs it automatically).

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check
    CHECK (status IN ('pending', 'settled', 'failed', 'held', 'blocked'));

CREATE TABLE IF NOT EXISTS risk_scores (
    tx_id         UUID PRIMARY KEY REFERENCES transactions(id) ON DELETE CASCADE,
    score         DOUBLE PRECISION NOT NULL,
    band          VARCHAR(10) NOT NULL CHECK (band IN ('low', 'medium', 'high', 'unscored')),
    reasons       JSONB NOT NULL DEFAULT '[]'::jsonb,
    model_version VARCHAR(100) NOT NULL,
    is_simulated  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- Enough of the packet payload to rebuild held transfers after a restart.
ALTER TABLE risk_scores ADD COLUMN IF NOT EXISTS sender_wallet_id UUID;
ALTER TABLE risk_scores ADD COLUMN IF NOT EXISTS receiver_wallet_id UUID;
ALTER TABLE risk_scores ADD COLUMN IF NOT EXISTS payload_currency VARCHAR(50);
ALTER TABLE risk_scores ADD COLUMN IF NOT EXISTS sender_currency VARCHAR(50);
ALTER TABLE risk_scores ADD COLUMN IF NOT EXISTS truth VARCHAR(50);
CREATE INDEX IF NOT EXISTS idx_risk_scores_band ON risk_scores(band, created_at DESC);

CREATE TABLE IF NOT EXISTS fraud_reviews (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tx_id       UUID NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    reviewer_id UUID REFERENCES users(id) ON DELETE SET NULL,
    decision    VARCHAR(10) NOT NULL CHECK (decision IN ('release', 'block')),
    note        TEXT,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_fraud_reviews_tx ON fraud_reviews(tx_id);

ALTER TABLE users ADD COLUMN IF NOT EXISTS is_frozen BOOLEAN NOT NULL DEFAULT FALSE;

-- The risk service looks up a sender's recent history on every score.
CREATE INDEX IF NOT EXISTS idx_transactions_sender_created ON transactions(sender_id, created_at DESC);
