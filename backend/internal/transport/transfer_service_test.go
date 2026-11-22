package transport

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend/internal/engine"
	"backend/internal/finance"
	"backend/internal/models"
	"backend/internal/models/packet"
	"backend/internal/repository"
	"backend/internal/risk"
)

// ---- in-memory fakes for the repositories ----

type fakeUsers struct{ byID map[uuid.UUID]*models.User }

func (f *fakeUsers) CreateUser(ctx context.Context, u *models.User) error { return nil }
func (f *fakeUsers) GetUserByUsername(ctx context.Context, name string) (*models.User, error) {
	for _, u := range f.byID {
		if u.Username == name {
			return u, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeUsers) GetUserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeUsers) GetAllUsers(ctx context.Context) ([]*models.User, error) { return nil, nil }
func (f *fakeUsers) SearchUsersByUsername(ctx context.Context, p string, l int) ([]*models.User, error) {
	return nil, nil
}

type fakeWallets struct {
	mu sync.Mutex
	ws []*models.Wallet
}

func (f *fakeWallets) CreateWallet(ctx context.Context, w *models.Wallet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.ID = uuid.New()
	f.ws = append(f.ws, w)
	return nil
}
func (f *fakeWallets) GetWalletByUserIDAndCurrency(ctx context.Context, u uuid.UUID, c string) (*models.Wallet, error) {
	for _, w := range f.ws {
		if w.UserID == u && w.CurrencyID == c {
			return w, nil
		}
	}
	return nil, errors.New("not found")
}
func (f *fakeWallets) UpdateWalletBalance(ctx context.Context, id uuid.UUID, a, l float64) error { return nil }
func (f *fakeWallets) LockFundsInWallet(ctx context.Context, id uuid.UUID, amount float64) error {
	for _, w := range f.ws {
		if w.ID == id {
			w.AvailableBalance -= amount
			w.LockedBalance += amount
			return nil
		}
	}
	return errors.New("wallet not found")
}
func (f *fakeWallets) GetAllWallets(ctx context.Context) ([]*models.Wallet, error) { return f.ws, nil }
func (f *fakeWallets) GetUserWealthSummary(ctx context.Context, u uuid.UUID) ([]repository.WealthSummary, error) {
	return nil, nil
}
func (f *fakeWallets) GetUserWalletsWithCurrencyInfo(ctx context.Context, u uuid.UUID) ([]repository.WalletWithCurrencyInfo, error) {
	return nil, nil
}

type fakeTxs struct {
	created []*models.Transaction
	voided  []uuid.UUID
	wallets *fakeWallets
}

func (f *fakeTxs) CreateTransaction(ctx context.Context, tx *models.Transaction) error {
	f.created = append(f.created, tx)
	return nil
}
func (f *fakeTxs) SettleTransaction(ctx context.Context, id, s, r uuid.UUID, a float64, c string) error {
	return nil
}
func (f *fakeTxs) VoidTransaction(ctx context.Context, id, senderWallet uuid.UUID, amount float64, c string) error {
	f.voided = append(f.voided, id)
	for _, w := range f.wallets.ws {
		if w.ID == senderWallet {
			w.LockedBalance -= amount
			w.AvailableBalance += amount
		}
	}
	return nil
}
func (f *fakeTxs) GetTransaction(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	return nil, nil
}
func (f *fakeTxs) GetAllTransactions(ctx context.Context) ([]*models.Transaction, error) { return nil, nil }
func (f *fakeTxs) InitiateTransferWithProcedure(ctx context.Context, s, r uuid.UUID, a float64, c, o, d string) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (f *fakeTxs) SettleTransactionWithProcedure(ctx context.Context, id uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeTxs) VoidTransactionWithProcedure(ctx context.Context, id uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeTxs) GetUserTransactionHistory(ctx context.Context, u uuid.UUID) ([]repository.UserTransactionRecord, error) {
	return nil, nil
}
func (f *fakeTxs) GetTransactionStatusHistory(ctx context.Context, id uuid.UUID) ([]repository.StatusHistoryRecord, error) {
	return nil, nil
}

type fakeScorer struct {
	band string
	err  error
	seen []risk.ScoreRequest
}

func (f *fakeScorer) Score(ctx context.Context, req risk.ScoreRequest) (*risk.Result, error) {
	f.seen = append(f.seen, req)
	if f.err != nil {
		return nil, f.err
	}
	score := map[string]float64{"low": 0.1, "medium": 0.6, "high": 0.93}[f.band]
	return &risk.Result{Score: score, Band: f.band, ModelVersion: "test",
		Reasons: []packet.RiskReason{{Code: "AMOUNT_SPIKE", Text: "big", Weight: 1}}}, nil
}

type world struct {
	svc      *TransferService
	sched    *engine.Scheduler
	wallets  *fakeWallets
	txs      *fakeTxs
	sender   *models.User
	receiver *models.User
}

func newWorld(scorer risk.Scorer) *world {
	now := time.Now()
	s := &models.User{ID: uuid.New(), Username: "alice", HomePlanet: "Earth", CreatedAt: now}
	r := &models.User{ID: uuid.New(), Username: "bob", HomePlanet: "Mars", CreatedAt: now}
	users := &fakeUsers{byID: map[uuid.UUID]*models.User{s.ID: s, r.ID: r}}
	wallets := &fakeWallets{}
	_ = wallets.CreateWallet(context.Background(), &models.Wallet{UserID: s.ID, CurrencyID: "EARTH", AvailableBalance: 1000})
	txs := &fakeTxs{wallets: wallets}
	ledger := finance.NewLedger()
	sched := engine.NewScheduler(packet.Point{}, ledger, txs)
	svc := &TransferService{Scheduler: sched, Ledger: ledger, Users: users, Wallets: wallets, Txs: txs,
		Desk: risk.NewDesk(nil), Scorer: scorer}
	return &world{svc: svc, sched: sched, wallets: wallets, txs: txs, sender: s, receiver: r}
}

func (w *world) send(t *testing.T, amount float64) (*TransferResult, int) {
	t.Helper()
	res, status, err := w.svc.Execute(context.Background(), w.sender.ID,
		TransferRequest{ReceiverUsername: "bob", Amount: amount, CurrencyID: "EARTH"}, ExecOptions{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res, status
}

func TestLowRiskLaunchesPacket(t *testing.T) {
	w := newWorld(&fakeScorer{band: "low"})
	res, status := w.send(t, 50)
	if status != http.StatusOK || res.Status != "active" {
		t.Fatalf("status=%d res=%+v", status, res)
	}
	if len(w.sched.GetState()) != 1 {
		t.Fatal("packet should be in flight")
	}
	if p := w.sched.GetState()[res.ID]; p.RiskBand != "low" {
		t.Fatalf("packet should carry its risk band, got %q", p.RiskBand)
	}
}

func TestHighRiskIsHeldAndFundsStayLocked(t *testing.T) {
	w := newWorld(&fakeScorer{band: "high"})
	res, status := w.send(t, 300)
	if status != http.StatusAccepted || res.Status != "held" || res.Risk.Band != "high" {
		t.Fatalf("status=%d res=%+v", status, res)
	}
	if len(w.sched.GetState()) != 0 {
		t.Fatal("held packet must not launch")
	}
	sw := w.wallets.ws[0]
	if sw.LockedBalance != 300 || sw.AvailableBalance != 700 {
		t.Fatalf("funds should stay locked: %+v", sw)
	}
	if len(w.svc.Desk.HeldCases()) != 1 {
		t.Fatal("case should be in the review queue")
	}
}

func TestRiskServiceDownFailsOpen(t *testing.T) {
	w := newWorld(&fakeScorer{err: errors.New("connection refused")})
	res, status := w.send(t, 20)
	if status != http.StatusOK || res.Status != "active" || res.Risk != nil {
		t.Fatalf("should launch unscored: status=%d res=%+v", status, res)
	}
	if w.svc.Desk.Stats().Unscored != 1 {
		t.Fatal("unscored transfer should be counted")
	}
}

func TestBlockRefundsSender(t *testing.T) {
	w := newWorld(&fakeScorer{band: "high"})
	res, _ := w.send(t, 400)
	c := w.svc.Desk.Take(res.ID)
	if err := w.svc.Block(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	sw := w.wallets.ws[0]
	if sw.AvailableBalance != 1000 || sw.LockedBalance != 0 || len(w.txs.voided) != 1 {
		t.Fatalf("block should refund through the void path: %+v", sw)
	}
	if len(w.sched.GetState()) != 0 {
		t.Fatal("a blocked transfer must never launch")
	}
}

func TestReleaseLaunchesHeldPacket(t *testing.T) {
	w := newWorld(&fakeScorer{band: "high"})
	res, _ := w.send(t, 100)
	c := w.svc.Desk.Take(res.ID)
	if err := w.svc.Release(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.sched.GetState()[res.ID]; !ok {
		t.Fatal("released packet should be in flight")
	}
}

func TestFrozenSenderIsRefused(t *testing.T) {
	w := newWorld(&fakeScorer{band: "high"})
	for i := 0; i < 3; i++ {
		res, _ := w.send(t, 10)
		w.svc.Desk.Decide(w.svc.Desk.Take(res.ID), "block", nil, "")
	}
	_, status, err := w.svc.Execute(context.Background(), w.sender.ID,
		TransferRequest{ReceiverUsername: "bob", Amount: 10, CurrencyID: "EARTH"}, ExecOptions{})
	if err == nil || status != http.StatusForbidden {
		t.Fatalf("frozen account should be refused, got status %d err %v", status, err)
	}
}

func TestSimTimeIsForwardedButTruthIsNot(t *testing.T) {
	f := &fakeScorer{band: "low"}
	w := newWorld(f)
	at := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)
	_, _, err := w.svc.Execute(context.Background(), w.sender.ID,
		TransferRequest{ReceiverUsername: "bob", Amount: 5, CurrencyID: "EARTH"},
		ExecOptions{SimTime: &at, Simulated: true, Truth: "micro_drain"})
	if err != nil {
		t.Fatal(err)
	}
	if f.seen[0].Timestamp[:19] != "2026-05-01T03:00:00" {
		t.Fatalf("sim time not forwarded: %s", f.seen[0].Timestamp)
	}
	// ScoreRequest has no field for the label: the scorer can never see the truth.
}
