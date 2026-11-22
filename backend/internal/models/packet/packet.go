// to define the model of star credit(money)

// needs : >> identity, payload, time, status, co-ordinaates

package packet

import (
	 "time"
	 "github.com/google/uuid"
	 "math"
	 
	 

)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type EnumStatus string

const (
	Active EnumStatus = "active" 
	Stalled EnumStatus = "stalled"
	Destroyed EnumStatus = "destroyed"
	Settled EnumStatus = "settled"
)

type Payload struct {
	Amount     float64 `json:"amount"`
	CurrencyID string  `json:"currency_id"`
	SenderWalletID   uuid.UUID `json:"sender_wallet_id"`
	ReceiverWalletID uuid.UUID `json:"receiver_wallet_id"`
}

type StateUpdate struct {
	Timestamp    int64                          `json:"timestamp"`
	Packets      map[uuid.UUID]Packet           `json:"packets"`
	Wallets      map[string]interface{}         `json:"wallets"`
	Transactions []map[string]interface{}       `json:"transactions"`
	Users        map[uuid.UUID]map[string]interface{} `json:"users"`
	// Fraud Shield: transfers held for review, the latest alerts, desk
	// counters and the demo simulation status. Filled in by the hub.
	Held      []interface{} `json:"held"`
	Alerts    []interface{} `json:"alerts"`
	RiskStats interface{}   `json:"riskStats,omitempty"`
	Sim       interface{}   `json:"sim,omitempty"`
}

// RiskReason is one human-readable reason behind a fraud score.
type RiskReason struct {
	Code   string  `json:"code"`
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
}

type Packet struct {
	ID         uuid.UUID `json:"id"`
	SenderID   uuid.UUID `json:"sender_id"`
	ReceiverID uuid.UUID `json:"receiver_id"`
	Start      Point     `json:"start"`
	End        Point     `json:"end"`
	OriginPlanet string `json:"origin_planet"`
	DestinationPlanet string `json:"destination_planet"`
	CurrentPos Point     `json:"current_pos"`
	Payload    Payload   `json:"payload"`
	LaunchTime time.Time `json:"launch_time"`
	// ETA        time.Time `json:"eta"`
	Status     EnumStatus `json:"status"`
	DilationFactor float64 `json:"dilationfactor"`
	Velocity       float64   `json:"velocity"`
	TravelT        float64   `json:"travel_t"`
	// Fraud Shield risk result. The physics engine ignores these fields.
	RiskScore float64      `json:"risk_score"`
	RiskBand  string       `json:"risk_band,omitempty"`
	Reasons   []RiskReason `json:"reasons,omitempty"`
}


func (p Packet) Distance(a Point, b Point) float64 {


	return math.Sqrt(
			math.Pow(b.X-a.X, 2) +
			math.Pow(b.Y-a.Y, 2) +
			math.Pow(b.Z-a.Z, 2),
	)
}

// for me : When the packet is created: during gin handler creation

// p.Velocity = SpeedOfLight
// p.DilationFactor = 1

// Then movement becomes:

// move = p.Velocity * p.DilationFactor * deltaTime