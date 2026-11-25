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
	s.set(func(st *Status) { st.State, st.Message = "seeding", fmt.Sprintf("mapping %d active accounts", n) })
	go func() {
		ctx := context.Background()
		if err := s.seed(ctx, n); err != nil {
			s.set(func(st *Status) { st.State, st.Message = "error", err.Error() })
			return
		}
		s.set(func(st *Status) { st.State, st.Message = "warming", "mapping 30-day transaction histories" })
		if err := s.warm(ctx); err != nil {
			s.set(func(st *Status) { st.State, st.Message = "error", "warmup: "+err.Error() })
			return
		}
		s.set(func(st *Status) { st.State, st.Message = "idle", fmt.Sprintf("%d active accounts mapped", len(s.bots)) })
	}()
	return nil
}

func (s *Simulator) seed(ctx context.Context, n int) error {
	sem := make(chan struct{}, 4) // bcrypt is CPU heavy
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 1; i <= n; i++ {
		name := getBotName(i)
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
			s.set(func(st *Status) { st.Message = fmt.Sprintf("mapped %d / %d active accounts", done, n) })
		}
	}
	wg.Wait()
	if firstErr != nil {
		log.Printf("[Sim] some bots failed to register: %v", firstErr)
	}
	if s.db != nil {
		activeNames := make([]string, n)
		for i := 1; i <= n; i++ {
			activeNames[i-1] = getBotName(i)
		}

		query := `DELETE FROM users WHERE username != 'analyst'`
		args := []interface{}{}
		if len(activeNames) > 0 {
			query += ` AND username NOT IN (`
			for idx, name := range activeNames {
				if idx > 0 {
					query += ","
				}
				query += fmt.Sprintf("$%d", idx+1)
				args = append(args, name)
			}
			query += `)`
		}
		res, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("remove extra bots: %w", err)
		}
		if k, _ := res.RowsAffected(); k > 0 {
			log.Printf("[Sim] removed %d extra bots", k)
			s.mu.Lock()
			s.bots = map[string]uuid.UUID{}
			s.warmed = false
			s.mu.Unlock()
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE wallets w SET available_balance = GREATEST(w.available_balance, $1)
			FROM users u WHERE u.id = w.user_id AND u.username != 'analyst'`, BotBalance); err != nil {
			return fmt.Errorf("top up bot wallets: %w", err)
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE users SET is_frozen = FALSE WHERE username != 'analyst'`)
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
		if u.Username != "analyst" {
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
			s.set(func(st *Status) { st.State, st.Message = "warming", "mapping 30-day transaction histories" })
			if err := s.warm(ctx); err != nil {
				s.set(func(st *Status) { st.State, st.Message = "error", "warmup: "+err.Error() })
				return
			}
		}
		plan, err := s.client.PlanScenario(ctx, req.Scenario, req.N, req.FraudRate)
		if err != nil && strings.Contains(err.Error(), "warm") {
			// The risk service restarted and lost the bots' history: warm again.
			s.set(func(st *Status) { st.State, st.Message = "warming", "risk service restarted, re-syncing transaction histories" })
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

var indianNames = []string{
	"rahul", "priya", "amit", "neha", "rohit", "ananya", "vikram", "deepika", "sanjay", "pooja",
	"arjun", "shruti", "karan", "isha", "rishi", "sneha", "kabir", "aditi", "vijay", "meera",
	"ajay", "tanvi", "manish", "divya", "raj", "swati", "vivek", "riya", "anil", "komal",
	"abhishek", "kajal", "varun", "kavya", "rohan", "nisha", "suresh", "aarti", "sameer", "nehal",
	"akash", "shreya", "siddharth", "kiran", "alok", "harish", "pallavi", "yash", "tanu",
	"nitin", "sapna", "pankaj", "reema", "sunil", "monika", "deepak", "kirti", "lalit", "shalini",
	"tarun", "payal", "gaurav", "mamta", "rajesh", "richa", "manoj", "jyoti", "sandeep", "sheetal",
	"preeti", "ashok", "anjali", "vinay", "rekha", "arvind", "savita", "poonam",
	"dev", "sonal", "nakul", "ritika", "madhav", "anita", "raghav", "priyanka", "mayank", "rashmi",
	"harshal", "shilpa", "nikhil", "sonam", "kartik", "kareena", "vaibhav", "katrina", "pranav", "alia",
	"rishabh", "shraddha", "dhruv", "janhvi", "ayush", "sara", "ishaan", "anushka", "aditya", "kriti",
	"chetan", "kiara", "tushar", "parineeti", "piyush", "sonakshi", "aman", "ileana", "aniket", "jacqueline",
	"sourabh", "nargis", "abshay", "bipasa", "mallika", "uday", "ameesha", "hritik", "susmita",
	"ranbir", "aishwarya", "saif", "karishma", "madhuri", "govinda", "juhi", "anil_kapoor", "raveena",
	"sunny_deol", "karina", "bobby_deol", "twinkle", "suniel_shetty", "shilpa_shetty", "akshaye_khanna", "tabu", "manisha", "urmila",
}

var englishNames = []string{
	"john", "emily", "michael", "sarah", "david", "jessica", "james", "ashley", "robert", "amanda",
	"william", "brittany", "joseph", "megan", "charles", "stephanie", "thomas", "elizabeth", "christopher", "heather",
	"daniel", "nicole", "matthew", "melissa", "donald", "amber", "mark", "rachel", "paul", "danielle",
	"steven", "samantha", "andrew", "tiffany", "kevin", "kayla", "brian", "courtney", "george", "justin",
	"richard", "edward", "laura", "ronald", "timothy", "karen", "jason", "nancy",
	"jeffrey", "lisa", "ryan", "betty", "gary", "dorothy", "jacob", "sandra", "nicholas",
	"eric", "kimberly", "stephen", "donna", "jonathan", "carol", "larry", "michelle",
	"scott", "brandon", "helen", "frank", "benjamin", "deborah", "gregory",
	"samuel", "rebecca", "raymond", "sharon", "patrick", "alexander", "cynthia", "jack", "kathleen",
	"dennis", "amy", "jerry", "shirley", "tyler", "angela", "aaron", "henry", "anna",
	"douglas", "brenda", "peter", "pamela", "jose", "walter", "emma", "kyle", "katherine",
	"harold", "christine", "carl", "debra", "arthur", "gerald", "carolyn", "roger", "janet",
	"keith", "maria", "jeremy", "lawrence", "frances", "terry", "joyce", "albert", "evelyn",
	"sean", "joan", "christian", "kelly", "christina", "billy", "lauren", "bruce", "victoria",
	"ralph", "judith", "roy", "cheryl", "eugene", "wayne", "alice", "louis", "ann",
}

var hollywoodNames = []string{
	"jacksparrow", "tonystark", "lukeskywalker", "harrypotter", "neo_matrix", "joker", "brucewayne", "peterparker", "indiana_jones", "terminator",
	"forrest_gump", "marty_mcfly", "dominic_toretto", "john_wick", "walter_white", "tyler_durden", "han_solo", "darth_vader", "katniss_everdeen", "sherlock_holmes",
	"james_bond", "optimus_prime", "bilbo_baggins", "gandalf", "captain_america", "thor", "wolverine", "lara_croft", "robocop", "maximus",
	"aragorn", "legolas", "frodo", "saruman", "gollum", "sauron", "elrond", "gimli", "ironman", "spiderman",
	"hulk", "blackwidow", "hawkeye", "doctorstrange", "blackpanther", "antman", "starlord", "gamora", "groot", "rocket_raccoon",
	"thanos", "loki", "deadpool", "magneto", "prof_x", "cyclops", "storm", "jean_grey", "beast",
	"mystique", "blade", "ghostrider", "morbius", "venom", "carnage", "sandman", "doc_ock", "green_goblin", "electro",
	"daredevil", "punisher", "electra", "bullseye", "kingpin", "luke_cage", "iron_fist", "jessica_jones", "john_constantine", "rorschach",
	"dr_manhattan", "nite_owl", "silk_spectre", "ozymandias", "comedian", "v_for_vendetta", "guy_fawkes", "pennywise", "freddy_krueger", "jason_voorhees",
	"michael_myers", "leatherface", "chucky", "pinhead", "jigsaw", "xenomorph", "predator", "godzilla", "king_kong", "frankenstein",
}

var animeNames = []string{
	"goku", "naruto", "luffy", "sasuke", "light_yagami", "ichigo", "deku", "tanjiro", "eren_yeager", "itachi",
	"gojo_satoru", "killua", "zoro", "saitama", "kakashi", "edward_elric", "lelouch", "kaneki", "ryuk", "asta",
	"levi", "midoriya", "nezuko", "mikasa", "vegeta", "hisoka", "madara", "gaara", "ash_ketchum", "yugi",
	"gon", "kurapika", "leorio", "natsu", "lucy", "gray", "erza", "happy", "gajeel", "laxus",
	"all_might", "bakugo", "todoroki", "uraraka", "iida", "shigaraki", "dabi", "toga", "all_for_one", "hawks",
	"shinji", "asuka", "rei", "kaworu", "gendo", "misato", "ritsuko", "kaji", "mari", "penpen",
	"zenitsu", "inosuke", "kanao", "genya", "muzan", "kokushibo", "douma", "akaza",
}

var bollywoodNames = []string{
	"raj_ddlj", "kabir_singh", "baburao", "circuit", "gabbar_singh", "chulbul_pandey", "mogambo", "singham", "raees", "don_srk",
	"krrish", "munnabhai", "pk_alien", "bajirao", "barfi", "rancho", "kancha_cheena", "shahenshah", "shakaal", "crime_master_gogo",
	"rowdy_rathore", "sardar_khan", "faizal_khan", "bhallaladeva", "baahubali", "pushpa_raj", "rocky_bhai", "salaar", "devdas", "muthu",
	"babu_bhaiya", "shyam_hera_pheri", "raju_hera_pheri", "anuradha_laxmi", "totla_tiwari", "kachra_seth", "khadak_singh", "nanji_bhai", "kabira", "ghanshyam",
	"choocha", "hunny", "lali", "zafar", "bholi_punjaban", "pandit_ji", "mcafee", "sher_singh", "chatur_silencer", "millimetre",
	"virus_sahasrabuddhe", "farhan", "raju_rastogi", "joy_lobo", "suhas_tandon", "rancho_original", "shambhu", "mishra_ji", "dubey_ji", "sharma_ji",
}

func getBotName(i int) string {
	idx := i - 1
	var baseName string
	cycle := idx / 10
	category := idx % 10

	switch category {
	case 0, 1, 2: // 30% Indian
		nameIdx := cycle*3 + category
		baseName = indianNames[nameIdx%len(indianNames)]
		if nameIdx >= len(indianNames) {
			baseName = fmt.Sprintf("%s%d", baseName, nameIdx/len(indianNames)+1)
		}
	case 3, 4, 5: // 30% English
		nameIdx := cycle*3 + (category - 3)
		baseName = englishNames[nameIdx%len(englishNames)]
		if nameIdx >= len(englishNames) {
			baseName = fmt.Sprintf("%s%d", baseName, nameIdx/len(englishNames)+1)
		}
	case 6, 7: // 20% Hollywood
		nameIdx := cycle*2 + (category - 6)
		baseName = hollywoodNames[nameIdx%len(hollywoodNames)]
		if nameIdx >= len(hollywoodNames) {
			baseName = fmt.Sprintf("%s%d", baseName, nameIdx/len(hollywoodNames)+1)
		}
	case 8: // 10% Anime
		nameIdx := cycle
		baseName = animeNames[nameIdx%len(animeNames)]
		if nameIdx >= len(animeNames) {
			baseName = fmt.Sprintf("%s%d", baseName, nameIdx/len(animeNames)+1)
		}
	case 9: // 10% Bollywood
		nameIdx := cycle
		baseName = bollywoodNames[nameIdx%len(bollywoodNames)]
		if nameIdx >= len(bollywoodNames) {
			baseName = fmt.Sprintf("%s%d", baseName, nameIdx/len(bollywoodNames)+1)
		}
	}

	return baseName
}
