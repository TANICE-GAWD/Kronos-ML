package transport

import (
	"backend/internal/models/packet"
	"backend/internal/repository"
	"backend/internal/risk"
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

func planetCurrencyIDForHub(planetName string) string {

	planetToCurrency := map[string]string{
		"earth":         "EARTH",
		"mars":          "MARS",
		"venus":         "VENUS",
		"jupiter":       "JUPITER",
		"saturn":        "SATURN",
		"mercury":       "MERCURY",
		"moon":          "MOON",
		"asteroid":      "ASTEROID",
		"asteroid belt": "ASTEROID",
	}

	normalizedPlanet := strings.ToLower(strings.TrimSpace(planetName))

	if currencyID, exists := planetToCurrency[normalizedPlanet]; exists {
		return currencyID
	}

	return strings.ToUpper(planetName)
}

type Hub struct {
	register        chan *Client
	unregister      chan *Client
	clients         map[*Client]bool
	broadcast       chan packet.StateUpdate
	walletRepo      repository.WalletRepository
	transactionRepo repository.TransactionRepository
	userRepo        repository.UserRepository
	// Fraud Shield (optional): desk snapshot and simulation status.
	desk      *risk.Desk
	simStatus func() interface{}
	// Full account list, refreshed off the broadcast loop. Frames only carry
	// the accounts a packet, transfer, or alert actually names.
	dirMu sync.RWMutex
	dir   accountDir
}

type accountDir struct {
	users   map[uuid.UUID]map[string]interface{}
	wallets map[string]interface{}
	analyst uuid.UUID
	loaded  bool
}

// Caps on what every frame carries. With hundreds of demo bots the full lists
// make frames several hundred KB and browsers fall behind. The complete review
// queue is always available from GET /api/fraud/queue.
const (
	maxTransactionsInState = 50
	maxHeldInState         = 50
	maxAlertsInState       = 40
)

// SetFraud attaches the fraud desk and simulation status to every state frame.
func (h *Hub) SetFraud(desk *risk.Desk, simStatus func() interface{}) {
	h.desk = desk
	h.simStatus = simStatus
}

func NewHub(walletRepo repository.WalletRepository, transactionRepo repository.TransactionRepository, userRepo repository.UserRepository) *Hub {
	return &Hub{
		clients:         make(map[*Client]bool),
		register:        make(chan *Client),
		unregister:      make(chan *Client),
		broadcast:       make(chan packet.StateUpdate),
		walletRepo:      walletRepo,
		transactionRepo: transactionRepo,
		userRepo:        userRepo,
	}
}

func (h *Hub) Run() {
	go h.refreshDirectory()
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
			}
		case stateSnapshot := <-h.broadcast:

			enrichedState := h.EnrichState(stateSnapshot)

			for client := range h.clients {
				select {
				case client.send <- enrichedState:
				default:
					// Skip this frame. Disconnecting a client that is still
					// writing the last one left the dashboard frozen until reload.
				}
			}
		}
	}

}

func (h *Hub) Broadcast(state packet.StateUpdate) {
	h.broadcast <- state
}

func (h *Hub) EnrichState(state packet.StateUpdate) packet.StateUpdate {
	ctx := context.Background()
	if state.Packets == nil {
		state.Packets = map[uuid.UUID]packet.Packet{}
	}
	state = h.attachActivity(ctx, state)
	state.Users, state.Wallets = h.frameAccounts(state)
	return state
}

func (h *Hub) refreshDirectory() {
	h.reloadDirectory()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		h.reloadDirectory()
	}
}

func (h *Hub) reloadDirectory() {
	ctx := context.Background()
	users, err := h.userRepo.GetAllUsers(ctx)
	if err != nil {
		log.Printf("[Hub] Error fetching users: %v", err)
		return
	}
	userPlanetCurrency := make(map[string]string, len(users))
	usersMap := make(map[uuid.UUID]map[string]interface{}, len(users))
	var analyst uuid.UUID
	for _, u := range users {
		userPlanetCurrency[u.ID.String()] = planetCurrencyIDForHub(u.HomePlanet)
		usersMap[u.ID] = map[string]interface{}{
			"id":          u.ID.String(),
			"username":    u.Username,
			"home_planet": u.HomePlanet,
			"created_at":  u.CreatedAt,
			"updated_at":  u.UpdatedAt,
		}
		if u.Username == "analyst" {
			analyst = u.ID
		}
	}

	walletsMap := map[string]interface{}{}
	wallets, err := h.walletRepo.GetAllWallets(ctx)
	if err != nil {
		log.Printf("[Hub] Error fetching wallets: %v", err)
	} else {
		selectedWalletByUser := make(map[string]interface{})
		selectedWalletMeta := make(map[string]struct {
			currencyID string
			updatedAt  time.Time
		})
		for _, w := range wallets {
			userKey := w.UserID.String()
			walletData := map[string]interface{}{
				"id":                w.ID.String(),
				"user_id":           w.UserID.String(),
				"currency_id":       w.CurrencyID,
				"available_balance": w.AvailableBalance,
				"locked_balance":    w.LockedBalance,
				"created_at":        w.CreatedAt,
				"updated_at":        w.UpdatedAt,
			}
			preferredCurrency := userPlanetCurrency[userKey]
			current, exists := selectedWalletMeta[userKey]
			if !exists {
				selectedWalletByUser[userKey] = walletData
				selectedWalletMeta[userKey] = struct {
					currencyID string
					updatedAt  time.Time
				}{
					currencyID: strings.ToUpper(strings.TrimSpace(w.CurrencyID)),
					updatedAt:  w.UpdatedAt,
				}
				continue
			}
			candidateCurrency := strings.ToUpper(strings.TrimSpace(w.CurrencyID))
			currentPreferred := current.currencyID == preferredCurrency
			candidatePreferred := candidateCurrency == preferredCurrency
			chooseCandidate := false
			if candidatePreferred && !currentPreferred {
				chooseCandidate = true
			} else if candidatePreferred == currentPreferred && w.UpdatedAt.After(current.updatedAt) {
				chooseCandidate = true
			}
			if chooseCandidate {
				selectedWalletByUser[userKey] = walletData
				selectedWalletMeta[userKey] = struct {
					currencyID string
					updatedAt  time.Time
				}{
					currencyID: candidateCurrency,
					updatedAt:  w.UpdatedAt,
				}
			}
		}
		for userKey, walletData := range selectedWalletByUser {
			walletsMap[userKey] = walletData
		}
	}

	h.dirMu.Lock()
	h.dir = accountDir{users: usersMap, wallets: walletsMap, analyst: analyst, loaded: true}
	h.dirMu.Unlock()
}

// frameAccounts keeps a frame small enough for the browser to keep up.
// The full account directory was several megabytes per update, so the socket
// filled and the desk stopped moving until a reload.
func (h *Hub) frameAccounts(state packet.StateUpdate) (map[uuid.UUID]map[string]interface{}, map[string]interface{}) {
	h.dirMu.RLock()
	dir := h.dir
	h.dirMu.RUnlock()

	users := map[uuid.UUID]map[string]interface{}{}
	wallets := map[string]interface{}{}
	if !dir.loaded {
		return users, wallets
	}

	ids := map[uuid.UUID]struct{}{}
	if dir.analyst != uuid.Nil {
		ids[dir.analyst] = struct{}{}
	}
	for _, p := range state.Packets {
		ids[p.SenderID] = struct{}{}
		ids[p.ReceiverID] = struct{}{}
	}
	for _, tx := range state.Transactions {
		addAccountID(ids, tx["sender_id"])
		addAccountID(ids, tx["receiver_id"])
	}
	for _, item := range append(state.Held, state.Alerts...) {
		switch c := item.(type) {
		case risk.Case:
			ids[c.SenderID] = struct{}{}
			ids[c.ReceiverID] = struct{}{}
		case *risk.Case:
			if c != nil {
				ids[c.SenderID] = struct{}{}
				ids[c.ReceiverID] = struct{}{}
			}
		}
	}
	for id := range ids {
		if u, ok := dir.users[id]; ok {
			users[id] = u
		}
		if w, ok := dir.wallets[id.String()]; ok {
			wallets[id.String()] = w
		}
	}
	return users, wallets
}

func addAccountID(ids map[uuid.UUID]struct{}, v interface{}) {
	s, ok := v.(string)
	if !ok {
		return
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return
	}
	ids[id] = struct{}{}
}

func (h *Hub) attachActivity(ctx context.Context, state packet.StateUpdate) packet.StateUpdate {
	transactions, err := h.transactionRepo.GetAllTransactions(ctx)
	if err != nil {
		log.Printf("[Hub] Error fetching transactions: %v", err)
	} else {
		txList := make([]map[string]interface{}, 0)
		if len(transactions) > maxTransactionsInState {
			transactions = transactions[:maxTransactionsInState] // newest first
		}
		for _, tx := range transactions {
			txList = append(txList, map[string]interface{}{
				"id":                 tx.ID.String(),
				"sender_id":          tx.SenderID.String(),
				"receiver_id":        tx.ReceiverID.String(),
				"amount":             tx.Amount,
				"status":             tx.Status,
				"origin_planet":      tx.OriginPlanet,
				"destination_planet": tx.DestinationPlanet,
				"created_at":         tx.CreatedAt,
				"updated_at":         tx.UpdatedAt,
			})
		}
		state.Transactions = txList
	}

	if h.desk != nil {
		held, alerts, stats := h.desk.Snapshot()
		if len(held) > maxHeldInState {
			held = held[:maxHeldInState] // highest scores first
		}
		if len(alerts) > maxAlertsInState {
			alerts = alerts[:maxAlertsInState] // newest first
		}
		state.Held, state.Alerts, state.RiskStats = held, alerts, stats
	}
	if h.simStatus != nil {
		state.Sim = h.simStatus()
	}
	if state.Held == nil {
		state.Held = []interface{}{}
	}
	if state.Alerts == nil {
		state.Alerts = []interface{}{}
	}
	if state.Packets == nil {
		state.Packets = map[uuid.UUID]packet.Packet{}
	}
	return state
}
