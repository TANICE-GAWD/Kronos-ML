package risk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScoreSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/score" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req ScoreRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Amount != 42 || req.SenderID != "s" {
			t.Fatalf("request not forwarded: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(Result{Score: 0.91, Band: BandHigh, ModelVersion: "m1"})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, time.Second)
	r, err := c.Score(context.Background(), ScoreRequest{SenderID: "s", Amount: 42})
	if err != nil || r.Band != BandHigh || r.Score != 0.91 {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestScoreTimeoutIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 30*time.Millisecond)
	start := time.Now()
	if _, err := c.Score(context.Background(), ScoreRequest{}); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Fatal("timeout not enforced")
	}
}

func TestScoreServerErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL, time.Second).Score(context.Background(), ScoreRequest{}); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestScoreUnreachableIsAnError(t *testing.T) {
	if _, err := NewClient("http://127.0.0.1:1", 50*time.Millisecond).Score(context.Background(), ScoreRequest{}); err == nil {
		t.Fatal("expected connection error")
	}
}
