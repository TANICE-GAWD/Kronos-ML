package transport

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/engine"
	"backend/internal/finance"
	"backend/internal/models"
	"backend/internal/models/packet"
	"backend/internal/repository"
	"backend/internal/risk"
)

// TransferService is the one path every transfer takes, from the REST handler
// and from the demo traffic generator alike:
//
//	validate -> lock funds (escrow) -> create pending tx -> SCORE ->
//	  low / medium: launch the packet (unchanged physics + settlement)
//	  high:         status = held, funds stay locked, packet waits for review
//
// If the risk service is unavailable the transfer launches as before (fail-open).
type TransferService struct {
	Scheduler *engine.Scheduler
	Ledger    *finance.Ledger
	Users     repository.UserRepository
	Wallets   repository.WalletRepository
	Txs       repository.TransactionRepository
	Scorer    risk.Scorer // nil = Fraud Shield disabled
	Desk      *risk.Desk
	DB        *sql.DB
}

// ExecOptions carries demo-only metadata. Truth is the simulator's label; it is
// shown on the demo scorecard and never sent to the scorer.
type ExecOptions struct {
	SimTime   *time.Time
	Simulated bool
	Truth     string
}

// TransferResult is returned to the client.
type TransferResult struct {
	ID       uuid.UUID         `json:"id"`
	Status   string            `json:"status"` // active | held
	Sender   map[string]string `json:"sender"`
	Receiver map[string]string `json:"receiver"`
	Amount   float64           `json:"amount"`
	Currency string            `json:"currency"`
	Risk     *TransferRisk     `json:"risk,omitempty"`
}

type TransferRisk struct {
	Score   float64             `json:"score"`
	Band    string              `json:"band"`
	Reasons []packet.RiskReason `json:"reasons"`
	Message string              `json:"message"`
}

func (s *TransferService) setStatus(ctx context.Context, id uuid.UUID, status string) error {
	if s.DB == nil {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE transactions SET status = $2 WHERE id = $1`, id, status)
	return err
}

func (s *TransferService) resolveSenderWallet(ctx context.Context, senderID uuid.UUID, currency string, amount float64) (*models.Wallet, string, error) {
	w, err := s.Wallets.GetWalletByUserIDAndCurrency(ctx, senderID, currency)
	if err == nil && w != nil {
		return w, currency, nil
	}
	all, err := s.Wallets.GetAllWallets(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve sender wallet")
	}
	var pick *models.Wallet
	for _, x := range all {
		if x.UserID == senderID && x.AvailableBalance >= amount {
			pick = x
			break
		}
	}
	if pick == nil {
		for _, x := range all {
			if x.UserID == senderID {
				pick = x
				break
			}
		}
	}
	if pick == nil {
		return nil, "", fmt.Errorf("sender does not have any wallet")
	}
	log.Printf("[Transfer] Fallback currency selected for sender %s: %s", senderID, pick.CurrencyID)
	return pick, pick.CurrencyID, nil
}

func (s *TransferService) ensureReceiverWallet(ctx context.Context, receiverID uuid.UUID, currency string, now time.Time) (*models.Wallet, error) {
	w, err := s.Wallets.GetWalletByUserIDAndCurrency(ctx, receiverID, currency)
	if err == nil && w != nil {
		return w, nil
	}
	w = &models.Wallet{UserID: receiverID, CurrencyID: currency, CreatedAt: now, UpdatedAt: now}
	if err := s.Wallets.CreateWallet(ctx, w); err != nil {
		refetch, ferr := s.Wallets.GetWalletByUserIDAndCurrency(ctx, receiverID, currency)
		if ferr != nil || refetch == nil {
			return nil, fmt.Errorf("failed to prepare receiver wallet")
		}
		return refetch, nil
	}
	return w, nil
}

// Execute runs one transfer. It returns the HTTP status to use.
func (s *TransferService) Execute(ctx context.Context, senderID uuid.UUID, req TransferRequest, opt ExecOptions) (*TransferResult, int, error) {
	sender, err := s.Users.GetUserByID(ctx, senderID)
	if err != nil {
		return nil, http.StatusUnauthorized, fmt.Errorf("sender account not found")
	}
	if s.Desk != nil && s.Desk.IsFrozen(senderID) {
		return nil, http.StatusForbidden, fmt.Errorf("account frozen by the fraud desk after repeated blocked transfers")
	}
	receiver, err := s.Users.GetUserByUsername(ctx, strings.TrimSpace(req.ReceiverUsername))
	if err != nil {
		return nil, http.StatusNotFound, fmt.Errorf("receiver not found")
	}
	if senderID == receiver.ID {
		return nil, http.StatusBadRequest, fmt.Errorf("cannot transfer to yourself")
	}

	now := time.Now()
	originPos, ok := engine.GetPlanetPosition(sender.HomePlanet, now)
	if !ok {
		return nil, http.StatusBadRequest, fmt.Errorf("sender's home planet not found in celestial map")
	}
	destPos, ok := engine.GetPlanetPosition(receiver.HomePlanet, now)
	if !ok {
		return nil, http.StatusBadRequest, fmt.Errorf("receiver's home planet not found in celestial map")
	}

	currency := strings.ToUpper(strings.TrimSpace(req.CurrencyID))
	receiverCurrency := planetCurrencyID(receiver.HomePlanet)
	senderWallet, currency, err := s.resolveSenderWallet(ctx, senderID, currency, req.Amount)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	receiverWallet, err := s.ensureReceiverWallet(ctx, receiver.ID, receiverCurrency, now)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if senderWallet.AvailableBalance < req.Amount {
		return nil, http.StatusBadRequest, fmt.Errorf("insufficient balance")
	}
	balanceBefore := senderWallet.AvailableBalance

	txID := uuid.New()
	if err := s.Wallets.LockFundsInWallet(ctx, senderWallet.ID, req.Amount); err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed to lock funds: %w", err)
	}
	tx := &models.Transaction{
		ID: txID, SenderID: senderID, ReceiverID: receiver.ID, Amount: req.Amount,
		Status: models.TransactionPending, OriginPlanet: sender.HomePlanet,
		DestinationPlanet: receiver.HomePlanet, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Txs.CreateTransaction(ctx, tx); err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to create transaction record")
	}

	p := &packet.Packet{
		ID: txID, SenderID: senderID, ReceiverID: receiver.ID,
		Start: originPos, End: destPos,
		OriginPlanet: sender.HomePlanet, DestinationPlanet: receiver.HomePlanet,
		CurrentPos: originPos,
		Payload: packet.Payload{
			Amount: req.Amount, CurrencyID: receiverCurrency,
			SenderWalletID: senderWallet.ID, ReceiverWalletID: receiverWallet.ID,
		},
		LaunchTime: now, Status: packet.Active, DilationFactor: 1.0, Velocity: SpeedOfLight,
	}

	// ---- Fraud Shield: score before the packet launches ----
	c := s.score(ctx, sender, receiver, p, currency, balanceBefore, now, opt)
	p.RiskScore, p.RiskBand, p.Reasons = c.Score, c.Band, c.Reasons

	res := &TransferResult{
		ID: txID, Status: "active",
		Sender:   map[string]string{"id": senderID.String(), "planet": sender.HomePlanet},
		Receiver: map[string]string{"id": receiver.ID.String(), "planet": receiver.HomePlanet},
		Amount:   req.Amount, Currency: receiverCurrency,
	}
	if c.Band != risk.BandUnscored {
		res.Risk = &TransferRisk{Score: c.Score, Band: c.Band, Reasons: c.Reasons, Message: bandMessage(c.Band)}
	}

	if c.Band == risk.BandHigh {
		if err := s.setStatus(ctx, txID, "held"); err != nil {
			log.Printf("[Transfer] could not mark %s held: %v", txID, err)
		}
		c.Status = "held"
		s.recordCase(c)
		res.Status = "held"
		log.Printf("[FraudShield] HELD %s score=%.2f %s -> %s %.2f", txID, c.Score, sender.Username, receiver.Username, req.Amount)
		return res, http.StatusAccepted, nil
	}

	if c.Band == risk.BandMedium {
		c.Status = "watch"
	} else {
		c.Status = "launched"
	}
	s.recordCase(c)
	s.Scheduler.AddPacket(p)
	log.Printf("[Transfer] Packet launched: %s from %s to %s (risk %s %.2f)", txID, sender.HomePlanet, receiver.HomePlanet, c.Band, c.Score)
	return res, http.StatusOK, nil
}

func (s *TransferService) recordCase(c *risk.Case) {
	if s.Desk == nil {
		return
	}
	s.Desk.Record(c)
	if c.Band != risk.BandUnscored {
		go s.Desk.SaveScore(c)
	}
}

func (s *TransferService) score(ctx context.Context, sender, receiver *models.User, p *packet.Packet,
	currency string, balance float64, now time.Time, opt ExecOptions) *risk.Case {
	c := &risk.Case{
		TxID: p.ID, SenderID: sender.ID, ReceiverID: receiver.ID,
		Sender: sender.Username, Receiver: receiver.Username,
		Amount: p.Payload.Amount, Currency: currency,
		OriginPlanet: sender.HomePlanet, DestinationPlanet: receiver.HomePlanet,
		Band: risk.BandUnscored, CreatedAt: now, Packet: p,
		Simulated: opt.Simulated, Truth: opt.Truth,
	}
	if s.Scorer == nil {
		return c
	}
	ts := now
	if opt.SimTime != nil {
		ts = *opt.SimTime
		c.SimTime = ts.UTC().Format(time.RFC3339)
	}
	r, err := s.Scorer.Score(ctx, risk.ScoreRequest{
		TxID: p.ID.String(), SenderID: sender.ID.String(), ReceiverID: receiver.ID.String(),
		Amount: p.Payload.Amount, Currency: currency,
		OriginPlanet: sender.HomePlanet, DestinationPlanet: receiver.HomePlanet,
		Timestamp:       ts.UTC().Format(time.RFC3339Nano),
		SenderBalance:   balance,
		SenderCreatedAt: sender.CreatedAt.UTC().Format(time.RFC3339), ReceiverCreatedAt: receiver.CreatedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("[FraudShield] risk_unavailable for %s: %v (fail-open)", p.ID, err)
		return c
	}
	c.Score, c.Band, c.Reasons, c.ModelVersion = r.Score, r.Band, r.Reasons, r.ModelVersion
	return c
}

func bandMessage(band string) string {
	switch band {
	case risk.BandHigh:
		return "Held for security review. The funds stay reserved until an analyst approves or blocks the transfer."
	case risk.BandMedium:
		return "Sent. This transfer looked a little unusual and is being monitored."
	default:
		return "Sent."
	}
}

// Release launches a held transfer. The packet starts from where the planet is now.
func (s *TransferService) Release(ctx context.Context, c *risk.Case) error {
	if err := s.setStatus(ctx, c.TxID, models.TransactionPending); err != nil {
		return err
	}
	p := c.Packet
	now := time.Now()
	if pos, ok := engine.GetPlanetPosition(p.OriginPlanet, now); ok {
		p.Start, p.CurrentPos = pos, pos
	}
	if pos, ok := engine.GetPlanetPosition(p.DestinationPlanet, now); ok {
		p.End = pos
	}
	p.LaunchTime = now
	p.Status = packet.Active
	s.Scheduler.AddPacket(p)
	return nil
}

// Block refunds the sender through the same void path the black hole uses.
func (s *TransferService) Block(ctx context.Context, c *risk.Case) error {
	p := c.Packet
	if err := s.Txs.VoidTransaction(ctx, p.ID, p.Payload.SenderWalletID, p.Payload.Amount, p.Payload.CurrencyID); err != nil {
		return err
	}
	if err := s.Ledger.Void(p.ID); err != nil {
		log.Printf("[FraudShield] ledger void (non-fatal): %v", err)
	}
	return s.setStatus(ctx, c.TxID, "blocked")
}
