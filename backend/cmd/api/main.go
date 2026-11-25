package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"backend/internal/auth"
	kdb "backend/internal/db"
	"backend/internal/engine"
	"backend/internal/finance"
	"backend/internal/models/packet"
	"backend/internal/repository"
	"backend/internal/risk"
	"backend/internal/sim"
	"backend/internal/transport"
)

var BlackHole = packet.Point{X: 0, Y: 0, Z: 100}

func main() {
	
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "your-secret-key-change-in-production" 
		log.Println("Warning: JWT_SECRET not set, using insecure default")
	}

	
	postgresRepo, err := repository.NewPostgresRepository(databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer postgresRepo.Close()
	log.Println("✓ Database connected successfully")

	
	db := postgresRepo.GetDB()
	userRepository := repository.NewUserRepository(db)
	walletRepository := repository.NewWalletRepository(db)
	transactionRepository := repository.NewTransactionRepository(db)
	log.Println("✓ Repositories initialized")

	// Automatically initialize schema tables, procedures and views if database is empty
	if err := kdb.InitializeSchema(context.Background(), db); err != nil {
		log.Fatalf("Failed to initialize database schema: %v", err)
	}

	if err := repository.SeedCurrencies(context.Background(), db); err != nil {
		log.Fatalf("Failed to seed currencies: %v", err)
	}

	authService := auth.NewAuthService(userRepository, walletRepository, jwtSecret)
	log.Println("✓ Auth service initialized")

	// ---- Fraud Shield ----
	if err := kdb.ApplyRiskMigration(context.Background(), db); err != nil {
		log.Fatalf("Failed to apply Fraud Shield migration: %v", err)
	}
	desk := risk.NewDesk(db)
	if n, err := desk.LoadHeld(context.Background()); err != nil {
		log.Printf("Warning: could not reload held transfers: %v", err)
	} else if n > 0 {
		log.Printf("✓ Reloaded %d held transfers into the review queue", n)
	}
	var riskClient *risk.Client
	if url := os.Getenv("RISK_SERVICE_URL"); url != "" {
		timeout := 300 * time.Millisecond
		if v, err := time.ParseDuration(os.Getenv("RISK_TIMEOUT")); err == nil {
			timeout = v
		}
		riskClient = risk.NewClient(url, timeout)
		log.Printf("✓ Fraud Shield enabled: risk service %s (timeout %s, fail-open)", url, timeout)
	} else {
		log.Println("Warning: RISK_SERVICE_URL not set, transfers are not scored")
	}
	admins := transport.AdminUsernames()
	ensureAnalyst(authService, userRepository)

	
	ledger := finance.NewLedger()
	finance.SeedLedger(ledger)
	log.Println("✓ Ledger initialized")

	hub := transport.NewHub(walletRepository, transactionRepository, userRepository)
	scheduler := engine.NewScheduler(BlackHole, ledger, transactionRepository)

	transferSvc := &transport.TransferService{
		Scheduler: scheduler, Ledger: ledger, Users: userRepository, Wallets: walletRepository,
		Txs: transactionRepository, Desk: desk, DB: db,
	}
	if riskClient != nil {
		transferSvc.Scorer = riskClient
	}
	simulator := sim.New(riskClient, transport.SimExecutor{Svc: transferSvc}, authService, userRepository, db)
	hub.SetFraud(desk, func() interface{} { return simulator.Status() })
	stop := make(chan struct{})
	log.Println("✓ Engine components initialized")

	
	go hub.Run()
	go scheduler.Start(stop)
	log.Println("✓ Hub and scheduler started")

	
	// The physics loop runs at 60 FPS, but each enriched frame carries users,
	// wallets, transactions and the fraud desk. With hundreds of demo bots,
	// 60 frames/s overflow slow clients (the hub then drops them), so forward
	// at most 5 frames/s; the frontend already interpolates packet motion.
	go func() {
		var latest *packet.StateUpdate
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case s, ok := <-scheduler.UpdateChan:
				if !ok {
					return
				}
				latest = &s
			case <-tick.C:
				if latest != nil {
					hub.Broadcast(*latest)
					latest = nil
				}
			}
		}
	}()

	// The scheduler only emits frames while packets fly. Held transfers, alerts
	// and the simulation scorecard must update even when the sky is empty.
	go func() {
		t := time.NewTicker(700 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if len(scheduler.GetState()) == 0 {
				hub.Broadcast(packet.StateUpdate{Packets: scheduler.GetState(), Timestamp: time.Now().UnixMilli()})
			}
		}
	}()

	
	r := gin.Default()
	
	
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"https://kronos-lime.vercel.app", "http://localhost:8080", "http://localhost:5173", "http://localhost:5174", "http://localhost:3000", "http://127.0.0.1:5173", "http://127.0.0.1:5174", "http://127.0.0.1:3000"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	
	log.Println("✓ Gin router initialized with CORS enabled")

	
	public := r.Group("/api")
	{
		public.POST("/register", transport.RegisterHandler(authService))
		public.POST("/login", transport.LoginHandler(authService))
		public.GET("/users/search", transport.SearchHandler(userRepository))
		public.POST("/sim/reset", transport.SimResetHandler(simulator, desk))
	}
	log.Println("✓ Public routes registered: /api/register, /api/login, /api/users/search, /api/sim/reset")

	
	protected := r.Group("/api")
	protected.Use(transport.AuthMiddleware(authService))
	{
		protected.POST("/transfer", transport.TransferHandler(transferSvc))
		protected.GET("/me", transport.MeHandler(admins))
		protected.GET("/balance/:userID", transport.BalanceHandler(ledger))
		protected.GET("/history/:userID", transport.HistoryHandler(ledger))
		protected.GET("/user/me/wealth", transport.GetUserWealthHandler(walletRepository))
		protected.GET("/user/me/wallets-detailed", transport.GetUserWalletsDetailedHandler(walletRepository))
		// View-backed endpoints
		protected.GET("/user/me/transactions", transport.GetUserTransactionHistoryHandler(transactionRepository))
		protected.GET("/transactions/:txID/status-history", transport.GetTransactionStatusHistoryHandler(transactionRepository))
	}
	fraud := r.Group("/api")
	fraud.Use(transport.AuthMiddleware(authService), transport.AdminOnly(admins))
	{
		fraud.GET("/fraud/queue", transport.FraudQueueHandler(desk))
		fraud.GET("/fraud/stats", transport.FraudStatsHandler(desk))
		fraud.GET("/fraud/model", transport.FraudModelHandler(riskClient))
		fraud.GET("/fraud/case/:txID", transport.FraudCaseHandler(desk, db))
		fraud.POST("/fraud/:txID/release", transport.FraudDecisionHandler(transferSvc, "release"))
		fraud.POST("/fraud/:txID/block", transport.FraudDecisionHandler(transferSvc, "block"))
		fraud.POST("/sim/seed", transport.SimSeedHandler(simulator))
		fraud.POST("/sim/start", transport.SimStartHandler(simulator, desk))
		fraud.POST("/sim/stop", transport.SimStopHandler(simulator))
		fraud.GET("/sim/status", transport.SimStatusHandler(simulator))
	}
	log.Println("✓ Fraud Shield routes registered: /api/fraud/*, /api/sim/* (analysts only)")

	log.Println("✓ Protected routes registered: /api/transfer, /api/balance, /api/history, /api/user/me/wealth, /api/user/me/wallets-detailed, /api/user/me/transactions, /api/transactions/:txID/status-history")

	
	r.GET("/ws", func(ctx *gin.Context) {
		currentState := scheduler.GetState()
		transport.ServeWS(ctx, "client", hub, currentState)
	})
	log.Println("✓ WebSocket route registered: /ws")

	
	fmt.Println("\n  Kronos Backend API Server Starting on :8080")
	fmt.Println("   Database: Connected to PostgreSQL")
	fmt.Println("   Auth: JWT-based authentication enabled")
	fmt.Println("   Routes: Public (/api/register, /api/login) + Protected + WebSocket")
	fmt.Println()

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// ensureAnalyst creates the demo fraud-analyst account (analyst / analyst) if missing.
func ensureAnalyst(a *auth.AuthService, users repository.UserRepository) {
	ctx := context.Background()
	if _, err := users.GetUserByUsername(ctx, "analyst"); err == nil {
		return
	}
	pw := os.Getenv("ANALYST_PASSWORD")
	if pw == "" {
		pw = "analyst"
	}
	if err := a.Register(ctx, "analyst", pw, "Earth"); err != nil {
		log.Printf("Warning: could not create analyst account: %v", err)
		return
	}
	log.Println("✓ Demo analyst account created (username: analyst)")
}
