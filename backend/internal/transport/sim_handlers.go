package transport

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"backend/internal/risk"
	"backend/internal/sim"
)

// SimExecutor adapts TransferService to the demo traffic generator.
type SimExecutor struct{ Svc *TransferService }

func (e SimExecutor) Execute(ctx context.Context, senderID uuid.UUID, receiverUsername string, amount float64,
	simTime time.Time, truth string) (string, error) {
	sender, err := e.Svc.Users.GetUserByID(ctx, senderID)
	if err != nil {
		return "", err
	}
	req := TransferRequest{ReceiverUsername: receiverUsername, Amount: amount, CurrencyID: planetCurrencyID(sender.HomePlanet)}
	res, _, err := e.Svc.Execute(ctx, senderID, req, ExecOptions{SimTime: &simTime, Simulated: true, Truth: truth})
	if err != nil {
		return "", err
	}
	if res.Risk == nil {
		return risk.BandUnscored, nil
	}
	return res.Risk.Band, nil
}

func SimSeedHandler(s *sim.Simulator) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body struct {
			N int `json:"n"`
		}
		_ = ctx.ShouldBindJSON(&body)
		if body.N <= 0 {
			body.N = sim.DefaultBots
		}
		if body.N > 1000 {
			body.N = 1000
		}
		if err := s.Seed(body.N); err != nil {
			ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusAccepted, s.Status())
	}
}

func SimStartHandler(s *sim.Simulator, desk *risk.Desk) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req sim.StartRequest
		_ = ctx.ShouldBindJSON(&req)
		if err := desk.ClearSimulated(ctx.Request.Context()); err != nil {
			log.Printf("[Sim] failed to clear simulated cases: %v", err)
		}
		if err := s.Start(req); err != nil {
			ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusAccepted, s.Status())
	}
}

func SimStopHandler(s *sim.Simulator) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		s.Stop()
		ctx.JSON(http.StatusOK, s.Status())
	}
}

func SimStatusHandler(s *sim.Simulator) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.JSON(http.StatusOK, s.Status()) }
}

func SimResetHandler(s *sim.Simulator, desk *risk.Desk) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if err := desk.ClearSimulated(ctx.Request.Context()); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "clear database: " + err.Error()})
			return
		}
		s.Stop()
		if err := s.ResetWarmed(ctx.Request.Context()); err != nil {
			log.Printf("[Sim] failed to reset risk service: %v", err)
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "message": "all simulated past runs data cleared entirely"})
	}
}
