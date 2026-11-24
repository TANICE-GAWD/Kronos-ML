package risk

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"backend/internal/models/packet"
)

const (
	maxAlerts         = 60
	maxIndexedCases   = 5000 // medium/high cases the case drawer can still open
	freezeAfterBlocks = 3
)

// Case is one scored transfer the desk knows about.
type Case struct {
	TxID              uuid.UUID           `json:"tx_id"`
	SenderID          uuid.UUID           `json:"sender_id"`
	ReceiverID        uuid.UUID           `json:"receiver_id"`
	Sender            string              `json:"sender"`
	Receiver          string              `json:"receiver"`
	Amount            float64             `json:"amount"`
	Currency          string              `json:"currency"`
	OriginPlanet      string              `json:"origin_planet"`
	DestinationPlanet string              `json:"destination_planet"`
	Score             float64             `json:"score"`
	Band              string              `json:"band"`
	Reasons           []packet.RiskReason `json:"reasons"`
	ModelVersion      string              `json:"model_version"`
	Status            string              `json:"status"` // launched | watch | held | released | blocked
	CreatedAt         time.Time           `json:"created_at"`
	SimTime           string              `json:"sim_time,omitempty"`
	Simulated         bool                `json:"simulated"`
	Truth             string              `json:"truth,omitempty"` // demo ground truth, never sent to the scorer
	Packet            *packet.Packet      `json:"-"`
}

// Stats are the fraud-desk counters shown in the KPI strip.
type Stats struct {
	Scored       int            `json:"scored"`
	Unscored     int            `json:"unscored"`
	ByBand       map[string]int `json:"by_band"`
	HeldNow      int            `json:"held_now"`
	Released     int            `json:"released"`
	Blocked      int            `json:"blocked"`
	Frozen       int            `json:"frozen"`
	AvgScore     float64        `json:"avg_score"`
	Histogram    [10]int        `json:"histogram"`
	AlertsByMin  []MinuteCount  `json:"alerts_by_minute"`
	TPS          float64        `json:"tps"`
	ModelVersion string         `json:"model_version"`
}

type MinuteCount struct {
	Minute int64 `json:"minute"` // unix minute
	High   int   `json:"high"`
	Medium int   `json:"medium"`
}

// Desk is the in-memory fraud desk, persisted to Postgres where it matters.
type Desk struct {
	mu       sync.RWMutex
	db       *sql.DB
	held     map[uuid.UUID]*Case
	alerts   []*Case
	index    map[uuid.UUID]*Case
	order    []uuid.UUID
	stats    Stats
	sumScore float64
	blocks   map[uuid.UUID]int
	frozen   map[uuid.UUID]bool
	recent   []time.Time // scoring times, for transfers-per-second
}

func NewDesk(db *sql.DB) *Desk {
	d := &Desk{
		db:     db,
		held:   map[uuid.UUID]*Case{},
		index:  map[uuid.UUID]*Case{},
		blocks: map[uuid.UUID]int{},
		frozen: map[uuid.UUID]bool{},
		stats:  Stats{ByBand: map[string]int{}},
	}
	d.loadFrozen()
	return d
}

func (d *Desk) loadFrozen() {
	if d.db == nil {
		return
	}
	rows, err := d.db.Query(`SELECT id FROM users WHERE is_frozen`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			d.frozen[id] = true
		}
	}
	d.stats.Frozen = len(d.frozen)
}

// LoadHeld rebuilds the review queue from Postgres after a restart, so held
// transfers (whose funds stay locked) are never orphaned.
func (d *Desk) LoadHeld(ctx context.Context) (int, error) {
	if d.db == nil {
		return 0, nil
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT t.id, t.sender_id, t.receiver_id, s.username, r.username, t.amount,
		       t.origin_planet, t.destination_planet, t.created_at,
		       rs.score, rs.band, rs.reasons, rs.model_version, rs.is_simulated,
		       rs.sender_wallet_id, rs.receiver_wallet_id, COALESCE(rs.payload_currency, ''),
		       COALESCE(rs.sender_currency, ''), COALESCE(rs.truth, '')
		FROM transactions t
		JOIN risk_scores rs ON rs.tx_id = t.id
		JOIN users s ON s.id = t.sender_id
		JOIN users r ON r.id = t.receiver_id
		WHERE t.status = 'held' AND rs.sender_wallet_id IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		c := &Case{Status: "held"}
		var reasons []byte
		var sw, rw uuid.UUID
		var pc string
		if err := rows.Scan(&c.TxID, &c.SenderID, &c.ReceiverID, &c.Sender, &c.Receiver, &c.Amount,
			&c.OriginPlanet, &c.DestinationPlanet, &c.CreatedAt, &c.Score, &c.Band, &reasons,
			&c.ModelVersion, &c.Simulated, &sw, &rw, &pc, &c.Currency, &c.Truth); err != nil {
			return n, err
		}
		_ = json.Unmarshal(reasons, &c.Reasons)
		c.Packet = &packet.Packet{
			ID: c.TxID, SenderID: c.SenderID, ReceiverID: c.ReceiverID,
			OriginPlanet: c.OriginPlanet, DestinationPlanet: c.DestinationPlanet,
			Payload: packet.Payload{Amount: c.Amount, CurrencyID: pc, SenderWalletID: sw, ReceiverWalletID: rw},
			Status:  packet.Active, DilationFactor: 1.0, Velocity: 50.0,
			RiskScore: c.Score, RiskBand: c.Band, Reasons: c.Reasons,
		}
		d.mu.Lock()
		d.held[c.TxID] = c
		d.index[c.TxID] = c
		d.mu.Unlock()
		n++
	}
	d.mu.Lock()
	d.stats.HeldNow = len(d.held)
	d.mu.Unlock()
	return n, rows.Err()
}

// IsFrozen reports whether an account was frozen after repeated blocks.
func (d *Desk) IsFrozen(id uuid.UUID) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.frozen[id]
}

// Record adds a scored (or unscored) transfer. High-band cases are held.
func (d *Desk) Record(c *Case) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	d.recent = append(d.recent, now)
	cut := 0
	for cut < len(d.recent) && now.Sub(d.recent[cut]) > 10*time.Second {
		cut++
	}
	d.recent = d.recent[cut:]
	if c.Band == BandUnscored {
		d.stats.Unscored++
		return
	}
	d.stats.Scored++
	d.stats.ByBand[c.Band]++
	d.sumScore += c.Score
	d.stats.AvgScore = d.sumScore / float64(d.stats.Scored)
	bin := int(c.Score * 10)
	if bin > 9 {
		bin = 9
	}
	if bin < 0 {
		bin = 0
	}
	d.stats.Histogram[bin]++
	if c.ModelVersion != "" {
		d.stats.ModelVersion = c.ModelVersion
	}
	if c.Band == BandHigh || c.Band == BandMedium {
		minute := now.Unix() / 60
		n := len(d.stats.AlertsByMin)
		if n == 0 || d.stats.AlertsByMin[n-1].Minute != minute {
			d.stats.AlertsByMin = append(d.stats.AlertsByMin, MinuteCount{Minute: minute})
			if len(d.stats.AlertsByMin) > 30 {
				d.stats.AlertsByMin = d.stats.AlertsByMin[1:]
			}
			n = len(d.stats.AlertsByMin)
		}
		if c.Band == BandHigh {
			d.stats.AlertsByMin[n-1].High++
		} else {
			d.stats.AlertsByMin[n-1].Medium++
		}
		d.alerts = append(d.alerts, c)
		if len(d.alerts) > maxAlerts {
			d.alerts = d.alerts[len(d.alerts)-maxAlerts:]
		}
		d.index[c.TxID] = c
		d.order = append(d.order, c.TxID)
		if len(d.order) > maxIndexedCases {
			old := d.order[0]
			d.order = d.order[1:]
			if _, stillHeld := d.held[old]; !stillHeld {
				delete(d.index, old)
			}
		}
	}
	if c.Band == BandHigh {
		d.held[c.TxID] = c
		d.stats.HeldNow = len(d.held)
	}
}

// ClearSimulated deletes all simulated transactions from both DB and in-memory.
func (d *Desk) ClearSimulated(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.db != nil {
		_, err := d.db.ExecContext(ctx, `
			DELETE FROM transactions 
			WHERE id IN (SELECT tx_id FROM risk_scores WHERE is_simulated = TRUE)
		`)
		if err != nil {
			log.Printf("[Desk] failed to clear simulated transactions from DB: %v", err)
			return err
		}
		_, _ = d.db.ExecContext(ctx, `UPDATE users SET is_frozen = FALSE WHERE username LIKE 'bot\_%'`)
	}

	for id, c := range d.held {
		if c.Simulated {
			delete(d.held, id)
		}
	}

	var newAlerts []*Case
	for _, c := range d.alerts {
		if !c.Simulated {
			newAlerts = append(newAlerts, c)
		}
	}
	d.alerts = newAlerts

	var newOrder []uuid.UUID
	for _, id := range d.order {
		if c, ok := d.index[id]; ok {
			if c.Simulated {
				delete(d.index, id)
			} else {
				newOrder = append(newOrder, id)
			}
		}
	}
	d.order = newOrder

	d.frozen = map[uuid.UUID]bool{}
	d.loadFrozen()

	d.stats.Scored = 0
	d.stats.Unscored = 0
	d.stats.ByBand = map[string]int{}
	d.sumScore = 0.0
	d.stats.AvgScore = 0.0
	d.stats.Histogram = [10]int{}
	d.stats.AlertsByMin = nil

	for _, c := range d.alerts {
		d.stats.Scored++
		d.stats.ByBand[c.Band]++
		d.sumScore += c.Score
		bin := int(c.Score * 10)
		if bin > 9 { bin = 9 }
		if bin < 0 { bin = 0 }
		d.stats.Histogram[bin]++
	}
	if d.stats.Scored > 0 {
		d.stats.AvgScore = d.sumScore / float64(d.stats.Scored)
	}
	d.stats.HeldNow = len(d.held)

	return nil
}

// Get returns a held or recently alerted case.
func (d *Desk) Get(id uuid.UUID) *Case {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if c, ok := d.held[id]; ok {
		return c
	}
	return d.index[id]
}

// Take removes a case from the held queue so only one reviewer can decide it.
func (d *Desk) Take(id uuid.UUID) *Case {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.held[id]
	if !ok {
		return nil
	}
	delete(d.held, id)
	d.stats.HeldNow = len(d.held)
	return c
}

// Restore puts a case back if a decision failed half-way.
func (d *Desk) Restore(c *Case) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held[c.TxID] = c
	d.stats.HeldNow = len(d.held)
}

// Decide records an analyst decision. Returns true if the sender got frozen.
func (d *Desk) Decide(c *Case, decision string, reviewer *uuid.UUID, note string) bool {
	d.mu.Lock()
	frozenNow := false
	if decision == "release" {
		c.Status = "released"
		d.stats.Released++
	} else {
		c.Status = "blocked"
		d.stats.Blocked++
		d.blocks[c.SenderID]++
		if d.blocks[c.SenderID] >= freezeAfterBlocks && !d.frozen[c.SenderID] {
			d.frozen[c.SenderID] = true
			d.stats.Frozen = len(d.frozen)
			frozenNow = true
		}
	}
	d.mu.Unlock()
	if d.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := d.db.ExecContext(ctx,
			`INSERT INTO fraud_reviews (tx_id, reviewer_id, decision, note) VALUES ($1, $2, $3, $4)`,
			c.TxID, reviewer, decision, note); err != nil {
			log.Printf("[Desk] save review %s: %v", c.TxID, err)
		}
		if frozenNow {
			_, _ = d.db.ExecContext(ctx, `UPDATE users SET is_frozen = TRUE WHERE id = $1`, c.SenderID)
		}
	}
	return frozenNow
}

// SaveScore persists the score for audit (model version, reasons, band).
func (d *Desk) SaveScore(c *Case) {
	if d.db == nil {
		return
	}
	reasons, _ := json.Marshal(c.Reasons)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var sw, rw *uuid.UUID
	var pc string
	if c.Packet != nil {
		sw, rw, pc = &c.Packet.Payload.SenderWalletID, &c.Packet.Payload.ReceiverWalletID, c.Packet.Payload.CurrencyID
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO risk_scores (tx_id, score, band, reasons, model_version, is_simulated,
		                          sender_wallet_id, receiver_wallet_id, payload_currency, sender_currency, truth)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')) ON CONFLICT (tx_id) DO NOTHING`,
		c.TxID, c.Score, c.Band, string(reasons), c.ModelVersion, c.Simulated, sw, rw, pc, c.Currency, c.Truth)
	if err != nil {
		log.Printf("[Desk] save score %s: %v", c.TxID, err)
	}
}

// HeldCases returns the review queue, highest score first.
func (d *Desk) HeldCases() []*Case {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*Case, 0, len(d.held))
	for _, c := range d.held {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].TxID.String() < out[j].TxID.String()
	})
	return out
}

// Alerts returns the latest medium/high alerts, newest first.
func (d *Desk) Alerts() []*Case {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*Case, len(d.alerts))
	for i, c := range d.alerts {
		out[len(d.alerts)-1-i] = c
	}
	return out
}

// Stats returns a copy of the counters.
func (d *Desk) Stats() Stats {
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := d.stats
	s.ByBand = map[string]int{}
	for k, v := range d.stats.ByBand {
		s.ByBand[k] = v
	}
	s.AlertsByMin = append([]MinuteCount(nil), d.stats.AlertsByMin...)
	n := 0
	for _, t := range d.recent {
		if time.Since(t) <= 10*time.Second {
			n++
		}
	}
	s.TPS = float64(n) / 10.0
	return s
}

// Snapshot is what the WebSocket hub attaches to every state frame.
func (d *Desk) Snapshot() (held []interface{}, alerts []interface{}, stats Stats) {
	// Copies, so the hub can serialise while reviewers change statuses.
	heldCases, alertCases := d.HeldCases(), d.Alerts()
	d.mu.RLock()
	for _, c := range heldCases {
		held = append(held, *c)
	}
	for _, c := range alertCases {
		alerts = append(alerts, *c)
	}
	d.mu.RUnlock()
	if held == nil {
		held = []interface{}{}
	}
	if alerts == nil {
		alerts = []interface{}{}
	}
	return held, alerts, d.Stats()
}
