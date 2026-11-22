package transport

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"backend/internal/risk"
)

// AdminUsernames come from ADMIN_USERNAMES (comma separated), default "analyst".
func AdminUsernames() map[string]bool {
	raw := os.Getenv("ADMIN_USERNAMES")
	if raw == "" {
		raw = "analyst"
	}
	out := map[string]bool{}
	for _, u := range strings.Split(raw, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out[u] = true
		}
	}
	return out
}

// AdminOnly lets only fraud analysts through.
func AdminOnly(admins map[string]bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		name, _ := GetUsername(ctx)
		if !admins[name] {
			ctx.JSON(http.StatusForbidden, gin.H{"error": "fraud desk is for analysts only"})
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}

// MeHandler tells the frontend who is logged in and whether they are an analyst.
func MeHandler(admins map[string]bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		name, _ := GetUsername(ctx)
		id, _ := GetUserID(ctx)
		ctx.JSON(http.StatusOK, gin.H{"id": id, "username": name, "is_analyst": admins[name]})
	}
}

func FraudQueueHandler(desk *risk.Desk) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"held": desk.HeldCases(), "alerts": desk.Alerts()})
	}
}

func FraudStatsHandler(desk *risk.Desk) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.JSON(http.StatusOK, desk.Stats()) }
}

type reviewRequest struct {
	Note string `json:"note"`
}

// FraudDecisionHandler handles POST /api/fraud/:txID/release and /block.
func FraudDecisionHandler(svc *TransferService, decision string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := uuid.Parse(ctx.Param("txID"))
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid transaction id"})
			return
		}
		var body reviewRequest
		_ = ctx.ShouldBindJSON(&body)
		c := svc.Desk.Take(id)
		if c == nil {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "transaction is not waiting for review"})
			return
		}
		if decision == "release" {
			err = svc.Release(ctx, c)
		} else {
			err = svc.Block(ctx, c)
		}
		if err != nil {
			svc.Desk.Restore(c)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var reviewer *uuid.UUID
		if s, ok := GetUserID(ctx); ok {
			if r, err := uuid.Parse(s); err == nil {
				reviewer = &r
			}
		}
		frozen := svc.Desk.Decide(c, decision, reviewer, body.Note)
		ctx.JSON(http.StatusOK, gin.H{"tx_id": id, "decision": decision, "status": c.Status, "sender_frozen": frozen})
	}
}

type historyPoint struct {
	TxID      string    `json:"tx_id"`
	Amount    float64   `json:"amount"`
	Receiver  string    `json:"receiver"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type graphEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

// FraudCaseHandler returns everything the case drawer shows: the case, the
// sender's last 20 transfers and the money-flow graph around the sender.
func FraudCaseHandler(desk *risk.Desk, db *sql.DB) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := uuid.Parse(ctx.Param("txID"))
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid transaction id"})
			return
		}
		c := desk.Get(id)
		if c == nil {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "case not found"})
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		history, edges := []historyPoint{}, []graphEdge{}
		if db != nil {
			rows, err := db.QueryContext(cctx, `
				SELECT t.id, t.amount, u.username, t.status, t.created_at
				FROM transactions t JOIN users u ON u.id = t.receiver_id
				WHERE t.sender_id = $1 ORDER BY t.created_at DESC LIMIT 20`, c.SenderID)
			if err == nil {
				for rows.Next() {
					var h historyPoint
					if rows.Scan(&h.TxID, &h.Amount, &h.Receiver, &h.Status, &h.CreatedAt) == nil {
						history = append(history, h)
					}
				}
				rows.Close()
			}
			// Money flow among the sender, the receiver and everyone they touched in the last day.
			rows, err = db.QueryContext(cctx, `
				WITH hub AS (SELECT unnest(ARRAY[$1::uuid, $2::uuid]) AS id),
				ring AS (
					SELECT id FROM hub
					UNION SELECT receiver_id FROM transactions WHERE sender_id IN (SELECT id FROM hub) AND created_at > now() - interval '1 day'
					UNION SELECT sender_id FROM transactions WHERE receiver_id IN (SELECT id FROM hub) AND created_at > now() - interval '1 day'
				)
				SELECT s.username, r.username, SUM(t.amount), COUNT(*)
				FROM transactions t JOIN users s ON s.id = t.sender_id JOIN users r ON r.id = t.receiver_id
				WHERE t.sender_id IN (SELECT id FROM ring) AND t.receiver_id IN (SELECT id FROM ring)
				  AND t.created_at > now() - interval '1 day'
				GROUP BY s.username, r.username ORDER BY SUM(t.amount) DESC LIMIT 60`, c.SenderID, c.ReceiverID)
			if err == nil {
				for rows.Next() {
					var e graphEdge
					if rows.Scan(&e.From, &e.To, &e.Amount, &e.Count) == nil {
						edges = append(edges, e)
					}
				}
				rows.Close()
			}
		}
		ctx.JSON(http.StatusOK, gin.H{"case": c, "history": history, "graph": edges})
	}
}

// FraudModelHandler proxies the model card from the risk service.
func FraudModelHandler(client *risk.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if client == nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "risk service not configured"})
			return
		}
		m, err := client.Model(ctx)
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, m)
	}
}
