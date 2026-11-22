package risk

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newCase(band string, score float64, sender uuid.UUID) *Case {
	return &Case{TxID: uuid.New(), SenderID: sender, Band: band, Score: score, CreatedAt: time.Now(), Status: "launched"}
}

func TestRecordHoldsOnlyHigh(t *testing.T) {
	d := NewDesk(nil)
	s := uuid.New()
	d.Record(newCase(BandLow, 0.1, s))
	d.Record(newCase(BandMedium, 0.6, s))
	d.Record(newCase(BandHigh, 0.9, s))
	d.Record(&Case{TxID: uuid.New(), Band: BandUnscored})
	st := d.Stats()
	if st.Scored != 3 || st.Unscored != 1 || st.HeldNow != 1 {
		t.Fatalf("stats %+v", st)
	}
	if len(d.HeldCases()) != 1 || len(d.Alerts()) != 2 {
		t.Fatalf("held=%d alerts=%d", len(d.HeldCases()), len(d.Alerts()))
	}
	if st.Histogram[1] != 1 || st.Histogram[6] != 1 || st.Histogram[9] != 1 {
		t.Fatalf("histogram %+v", st.Histogram)
	}
}

func TestQueueSortedByScore(t *testing.T) {
	d := NewDesk(nil)
	for _, sc := range []float64{0.82, 0.99, 0.9} {
		d.Record(newCase(BandHigh, sc, uuid.New()))
	}
	h := d.HeldCases()
	if h[0].Score != 0.99 || h[2].Score != 0.82 {
		t.Fatalf("not sorted: %v %v %v", h[0].Score, h[1].Score, h[2].Score)
	}
}

func TestTakeIsExclusive(t *testing.T) {
	d := NewDesk(nil)
	c := newCase(BandHigh, 0.9, uuid.New())
	d.Record(c)
	if d.Take(c.TxID) == nil || d.Take(c.TxID) != nil {
		t.Fatal("a held case must be taken exactly once")
	}
	d.Restore(c)
	if d.Stats().HeldNow != 1 {
		t.Fatal("restore failed")
	}
}

func TestThreeBlocksFreezeTheSender(t *testing.T) {
	d := NewDesk(nil)
	sender := uuid.New()
	frozen := false
	for i := 0; i < 3; i++ {
		c := newCase(BandHigh, 0.95, sender)
		d.Record(c)
		frozen = d.Decide(d.Take(c.TxID), "block", nil, "")
	}
	if !frozen || !d.IsFrozen(sender) {
		t.Fatal("sender should be frozen after 3 blocks")
	}
	if st := d.Stats(); st.Blocked != 3 || st.Frozen != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestReleaseCounts(t *testing.T) {
	d := NewDesk(nil)
	c := newCase(BandHigh, 0.85, uuid.New())
	d.Record(c)
	d.Decide(d.Take(c.TxID), "release", nil, "looks fine")
	if c.Status != "released" || d.Stats().Released != 1 || d.Get(c.TxID) == nil {
		t.Fatal("release not recorded")
	}
}
