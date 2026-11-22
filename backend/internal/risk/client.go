// Package risk connects Kronos to the Fraud Shield risk-scoring service and keeps
// the fraud desk: transfers held for review, live alerts and counters.
package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"backend/internal/models/packet"
)

const (
	BandLow      = "low"
	BandMedium   = "medium"
	BandHigh     = "high"
	BandUnscored = "unscored"
)

// ScoreRequest is what the Go backend knows about a transfer before it launches.
type ScoreRequest struct {
	TxID              string  `json:"tx_id"`
	SenderID          string  `json:"sender_id"`
	ReceiverID        string  `json:"receiver_id"`
	Amount            float64 `json:"amount"`
	Currency          string  `json:"currency"`
	OriginPlanet      string  `json:"origin_planet"`
	DestinationPlanet string  `json:"destination_planet"`
	Timestamp         string  `json:"timestamp"`
	SenderBalance     float64 `json:"sender_balance"`
	SenderCreatedAt   string  `json:"sender_created_at,omitempty"`
	ReceiverCreatedAt string  `json:"receiver_created_at,omitempty"`
}

// Result is the risk service's answer.
type Result struct {
	TxID         string              `json:"tx_id"`
	Score        float64             `json:"score"`
	ModelScore   float64             `json:"model_score"`
	RuleScore    float64             `json:"rule_score"`
	Band         string              `json:"band"`
	Reasons      []packet.RiskReason `json:"reasons"`
	ModelVersion string              `json:"model_version"`
	LatencyMs    float64             `json:"latency_ms"`
}

// Scorer is the part of the client the transfer path depends on (fakeable in tests).
type Scorer interface {
	Score(ctx context.Context, req ScoreRequest) (*Result, error)
}

// Client talks to the Python risk service over HTTP.
type Client struct {
	BaseURL string
	Timeout time.Duration
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{BaseURL: baseURL, Timeout: timeout, http: &http.Client{}}
}

func (c *Client) post(ctx context.Context, path string, body, out interface{}, timeout time.Duration) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out interface{}) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("risk service %s: %d %s", req.URL.Path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Score asks the risk service for a score. Callers must treat an error as
// "unscored" and let the transfer through (fail-open).
func (c *Client) Score(ctx context.Context, req ScoreRequest) (*Result, error) {
	var r Result
	if err := c.post(ctx, "/score", req, &r, c.Timeout); err != nil {
		return nil, err
	}
	return &r, nil
}

// Model returns the deployed model card.
func (c *Client) Model(ctx context.Context) (map[string]interface{}, error) {
	var m map[string]interface{}
	return m, c.get(ctx, "/model", &m)
}

// BotUser is a demo account passed to /warmup.
type BotUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	HomePlanet string `json:"home_planet"`
}

// Warmup gives demo bots a synthetic history inside the risk service.
func (c *Client) Warmup(ctx context.Context, users []BotUser) (map[string]interface{}, error) {
	var out map[string]interface{}
	return out, c.post(ctx, "/warmup", map[string]interface{}{"users": users}, &out, 60*time.Second)
}

// PlanItem is one transfer in a demo scenario.
type PlanItem struct {
	Seq              int     `json:"seq"`
	SimTime          string  `json:"sim_time"`
	SenderID         string  `json:"sender_id"`
	SenderUsername   string  `json:"sender_username"`
	ReceiverID       string  `json:"receiver_id"`
	ReceiverUsername string  `json:"receiver_username"`
	Amount           float64 `json:"amount"`
	IsFraud          int     `json:"is_fraud"`
	FraudType        string  `json:"fraud_type"`
}

// Plan is a demo scenario: normal and fraudulent transfers in simulated time.
type Plan struct {
	Scenario string         `json:"scenario"`
	Label    string         `json:"label"`
	SimStart string         `json:"sim_start"`
	SimEnd   string         `json:"sim_end"`
	Counts   map[string]int `json:"counts"`
	NFraud   int            `json:"n_fraud"`
	Items    []PlanItem     `json:"items"`
}

// PlanScenario asks the risk service to plan a scenario.
func (c *Client) PlanScenario(ctx context.Context, scenario string, n int, fraudRate *float64) (*Plan, error) {
	body := map[string]interface{}{"scenario": scenario, "n": n}
	if fraudRate != nil {
		body["fraud_rate"] = *fraudRate
	}
	var p Plan
	return &p, c.post(ctx, "/sim/plan", body, &p, 30*time.Second)
}
