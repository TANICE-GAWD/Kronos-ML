// Package sim is the live demo traffic generator: hundreds of bot accounts
// sending money at once, a few of them fraudsters, all going through the real
// transfer path (escrow -> fraud score -> launch or hold).
//
// The behaviour (who pays whom, how much, which attacks) is planned by the
// Python risk service from the same simulator used to build the training data;
// this package only executes the plan in real time and keeps a scorecard.
package sim

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"backend/internal/models"
	"backend/internal/risk"
)

const (
	BotPrefix   = "bot_"
	BotPassword = "kronos-bot-2026"
	BotBalance  = 5000.0
	// DefaultBots keeps the demo light enough for an ordinary laptop: every
	// WebSocket frame carries all users and wallets.
	DefaultBots = 100
)

var botPlanets = []string{"Earth", "Earth", "Earth", "Mars", "Mars", "Venus", "Venus", "Mercury", "Jupiter", "Saturn"}

// Executor runs one transfer through the real transfer path.
type Executor interface {
	Execute(ctx context.Context, senderID uuid.UUID, receiverUsername string, amount float64,
		simTime time.Time, truth string) (band string, err error)
}

// Registrar creates accounts (auth.AuthService satisfies it).
type Registrar interface {
	Register(ctx context.Context, username, password, homePlanet string) error
}

type UserLookup interface {
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	GetAllUsers(ctx context.Context) ([]*models.User, error)
}

// PatternScore is caught / total for one fraud pattern.
type PatternScore struct {
	Total  int `json:"total"`
	Caught int `json:"caught"`
}

// Status is shown in the simulation panel and the live scorecard.
type Status struct {
	State        string                   `json:"state"` // idle | seeding | warming | planning | running | done | stopped | error
	Message      string                   `json:"message"`
	Bots         int                      `json:"bots"`
	Scenario     string                   `json:"scenario"`
	Label        string                   `json:"label"`
	Total        int                      `json:"total"`
	Done         int                      `json:"done"`
	Failed       int                      `json:"failed"`
	FraudPlanned int                      `json:"fraud_planned"`
	Bands        map[string]int           `json:"bands"`
	Caught       int                      `json:"caught"`       // fraud held (high)
	Watched      int                      `json:"watched"`      // fraud flagged medium
	Missed       int                      `json:"missed"`       // fraud that launched as low / unscored
	FalseAlarms  int                      `json:"false_alarms"` // normal transfers held
	ByPattern    map[string]*PatternScore `json:"by_pattern"`
	StartedAt    time.Time                `json:"started_at"`
	DurationS    int                      `json:"duration_s"`
	SimClock     string                   `json:"sim_clock"`
}

type Simulator struct {
	mu     sync.RWMutex
	st     Status
	cancel context.CancelFunc
	client *risk.Client
	exec   Executor
	reg    Registrar
	users  UserLookup
	db     *sql.DB
	bots   map[string]uuid.UUID // username -> id
	warmed bool
}

func New(client *risk.Client, exec Executor, reg Registrar, users UserLookup, db *sql.DB) *Simulator {
	return &Simulator{
		client: client, exec: exec, reg: reg, users: users, db: db,
		bots: map[string]uuid.UUID{},
		st:   Status{State: "idle", Bands: map[string]int{}, ByPattern: map[string]*PatternScore{}},
	}
}

// ResetWarmed resets the warmed flag and tells the risk client to reset its scorer state.
func (s *Simulator) ResetWarmed(ctx context.Context) error {
	s.mu.Lock()
	s.warmed = false
	s.st = Status{State: "idle", Bands: map[string]int{}, ByPattern: map[string]*PatternScore{}}
	s.mu.Unlock()

	if s.client != nil {
		return s.client.Reset(ctx)
	}
	return nil
}

func (s *Simulator) set(f func(st *Status)) {
	s.mu.Lock()
	f(&s.st)
	s.mu.Unlock()
}

// Status returns a copy for the WebSocket and REST.
func (s *Simulator) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.st
	c.Bands = map[string]int{}
	for k, v := range s.st.Bands {
		c.Bands[k] = v
	}
	c.ByPattern = map[string]*PatternScore{}
	for k, v := range s.st.ByPattern {
		cp := *v
		c.ByPattern[k] = &cp
	}
	return c
}

func (s *Simulator) busy() bool {
	st := s.Status().State
	return st == "seeding" || st == "warming" || st == "planning" || st == "running"
}

// Seed creates n bot accounts (idempotent), tops up their wallets and gives
// them a synthetic history inside the risk service.
func (s *Simulator) Seed(n int) error {
	if s.busy() {
		return fmt.Errorf("simulation busy")
	}
	if s.client == nil {
		return fmt.Errorf("risk service not configured (RISK_SERVICE_URL)")
	}
	s.set(func(st *Status) { st.State, st.Message = "seeding", fmt.Sprintf("creating %d bot accounts", n) })
	go func() {
		ctx := context.Background()
		if err := s.seed(ctx, n); err != nil {
			s.set(func(st *Status) { st.State, st.Message = "error", err.Error() })
			return
		}
		s.set(func(st *Status) { st.State, st.Message = "warming", "giving bots a 30-day history" })
		if err := s.warm(ctx); err != nil {
			s.set(func(st *Status) { st.State, st.Message = "error", "warmup: "+err.Error() })
			return
		}
		s.set(func(st *Status) { st.State, st.Message = "idle", fmt.Sprintf("%d bots ready", len(s.bots)) })
	}()
	return nil
}

func (s *Simulator) seed(ctx context.Context, n int) error {
	sem := make(chan struct{}, 4) // bcrypt is CPU heavy
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("%s%03d", BotPrefix, i)
		planet := botPlanets[i%len(botPlanets)]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := s.users.GetUserByUsername(ctx, name); err == nil {
				return
			}
			if err := s.reg.Register(ctx, name, BotPassword, planet); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
		if i%25 == 0 {
			done := i
			s.set(func(st *Status) { st.Message = fmt.Sprintf("created %d / %d bots", done, n) })
		}
	}
	wg.Wait()
	if firstErr != nil {
		log.Printf("[Sim] some bots failed to register: %v", firstErr)
	}
	if s.db != nil {
		// Seeding fewer bots than before removes the extras (and their demo
		// transfers), so a smaller demo really is smaller.
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM users WHERE username LIKE 'bot\_%'
			AND substring(username from 5) ~ '^[0-9]+$' AND substring(username from 5)::int > $1`, n)
		if err != nil {
			return fmt.Errorf("remove extra bots: %w", err)
		}
		if k, _ := res.RowsAffected(); k > 0 {
			log.Printf("[Sim] removed %d bots above bot_%03d", k, n)
			s.mu.Lock()
			s.bots = map[string]uuid.UUID{}
			s.warmed = false
			s.mu.Unlock()
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE wallets w SET available_balance = GREATEST(w.available_balance, $1)
			FROM users u WHERE u.id = w.user_id AND u.username LIKE 'bot\_%'`, BotBalance); err != nil {
			return fmt.Errorf("top up bot wallets: %w", err)
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE users SET is_frozen = FALSE WHERE username LIKE 'bot\_%'`)
	}
	return nil
}

func (s *Simulator) loadBots(ctx context.Context) ([]risk.BotUser, error) {
	all, err := s.users.GetAllUsers(ctx)
	if err != nil {
		return nil, err
	}
	var out []risk.BotUser
	s.mu.Lock()
	for _, u := range all {
		if strings.HasPrefix(u.Username, BotPrefix) {
			s.bots[u.Username] = u.ID
			out = append(out, risk.BotUser{ID: u.ID.String(), Username: u.Username, HomePlanet: strings.ToLower(u.HomePlanet)})
		}
	}
	s.st.Bots = len(out)
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

func (s *Simulator) warm(ctx context.Context) error {
	bots, err := s.loadBots(ctx)
	if err != nil {
		return err
	}
	if len(bots) < 20 {
		return fmt.Errorf("only %d bots; seed at least 20", len(bots))
	}
	if _, err := s.client.Warmup(ctx, bots); err != nil {
		return err
	}
	s.mu.Lock()
	s.warmed = true
	s.mu.Unlock()
	return nil
}

// StartRequest configures a run.
type StartRequest struct {
	Scenario  string   `json:"scenario"`
	N         int      `json:"n"`
	FraudRate *float64 `json:"fraud_rate"`
	DurationS int      `json:"duration_s"`
	Workers   int      `json:"workers"`
}

// Start plans a scenario and replays it in real time.
func (s *Simulator) Start(req StartRequest) error {
	if s.busy() {
		return fmt.Errorf("simulation busy")
	}
	if s.client == nil {
		return fmt.Errorf("risk service not configured (RISK_SERVICE_URL)")
	}
	if req.Scenario == "" {
		req.Scenario = "mixed_attack"
	}
	if req.N <= 0 {
		req.N = 100
	}
	if req.DurationS <= 0 {
		req.DurationS = 120
	}
	if req.Workers <= 0 {
		req.Workers = 4
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.warmed = false // force re-warmup because we are resetting the risk service
	warmed := false
	s.st = Status{State: "planning", Message: "planning scenario", Scenario: req.Scenario, Bots: s.st.Bots,
		Bands: map[string]int{}, ByPattern: map[string]*PatternScore{}, DurationS: req.DurationS}
	s.mu.Unlock()
	go func() {
		// Reset the python risk service scorer to empty state
		if err := s.client.Reset(ctx); err != nil {
			log.Printf("[Sim] failed to reset risk service: %v", err)
		}

		if !warmed {
			s.set(func(st *Status) { st.State, st.Message = "warming", "giving bots a 30-day history" })
			if err := s.warm(ctx); err != nil {
				s.set(func(st *Status) { st.State, st.Message = "error", "warmup: "+err.Error() })
				return
			}
		}
		plan, err := s.client.PlanScenario(ctx, req.Scenario, req.N, req.FraudRate)
		if err != nil && strings.Contains(err.Error(), "warm") {
			// The risk service restarted and lost the bots' history: warm again.
			s.set(func(st *Status) { st.State, st.Message = "warming", "risk service restarted, re-warming bots" })
			if werr := s.warm(ctx); werr == nil {
				plan, err = s.client.PlanScenario(ctx, req.Scenario, req.N, req.FraudRate)
			}
		}
		if err != nil {
			s.set(func(st *Status) { st.State, st.Message = "error", "plan: "+err.Error() })
			return
		}
		s.run(ctx, plan, req)
	}()
	return nil
}

// Stop cancels the running scenario.
func (s *Simulator) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

func (s *Simulator) run(ctx context.Context, plan *risk.Plan, req StartRequest) {
	items := plan.Items
	s.set(func(st *Status) {
		st.State, st.Label, st.Total, st.FraudPlanned = "running", plan.Label, len(items), plan.NFraud
		st.Message, st.StartedAt = fmt.Sprintf("%s: %d transfers, %d fraudulent", plan.Label, len(items), plan.NFraud), time.Now()
		for _, it := range items {
			if it.IsFraud == 1 {
				if st.ByPattern[it.FraudType] == nil {
					st.ByPattern[it.FraudType] = &PatternScore{}
				}
				st.ByPattern[it.FraudType].Total++
			}
		}
	})
	if len(items) == 0 {
		s.set(func(st *Status) { st.State = "done" })
		return
	}
	first, _ := time.Parse(time.RFC3339, items[0].SimTime)
	last, _ := time.Parse(time.RFC3339, items[len(items)-1].SimTime)
	span := last.Sub(first).Seconds()
	if span <= 0 {
		span = 1
	}
	realSpan := float64(req.DurationS)
	start := time.Now()

	jobs := make(chan risk.PlanItem, len(items))
	var wg sync.WaitGroup
	for w := 0; w < req.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				s.execute(ctx, it)
			}
		}()
	}
	// Bursts stay bursts: each item fires at its simulated time, compressed.
	for _, it := range items {
		t, _ := time.Parse(time.RFC3339, it.SimTime)
		due := start.Add(time.Duration((t.Sub(first).Seconds() / span) * realSpan * float64(time.Second)))
		select {
		case <-ctx.Done():
		case <-time.After(time.Until(due)):
		}
		if ctx.Err() != nil {
			break
		}
		s.set(func(st *Status) { st.SimClock = it.SimTime })
		jobs <- it
	}
	close(jobs)
	wg.Wait()
	s.set(func(st *Status) {
		if ctx.Err() != nil {
			st.State, st.Message = "stopped", "stopped by analyst"
		} else {
			st.State = "done"
			st.Message = fmt.Sprintf("%s finished: caught %d of %d fraudulent transfers, %d false alarms",
				st.Label, st.Caught, st.FraudPlanned, st.FalseAlarms)
		}
	})
}

func (s *Simulator) execute(ctx context.Context, it risk.PlanItem) {
	if ctx.Err() != nil {
		return
	}
	senderID, err := uuid.Parse(it.SenderID)
	if err != nil {
		return
	}
	simTime, _ := time.Parse(time.RFC3339, it.SimTime)
	truth := ""
	if it.IsFraud == 1 {
		truth = it.FraudType
	}
	band, err := s.exec.Execute(ctx, senderID, it.ReceiverUsername, it.Amount, simTime, truth)
	s.set(func(st *Status) {
		st.Done++
		if err != nil {
			st.Failed++
			return
		}
		st.Bands[band]++
		fraud := it.IsFraud == 1
		switch {
		case fraud && band == risk.BandHigh:
			st.Caught++
			st.ByPattern[it.FraudType].Caught++
		case fraud && band == risk.BandMedium:
			st.Watched++
		case fraud:
			st.Missed++
		case band == risk.BandHigh:
			st.FalseAlarms++
		}
	})
}
