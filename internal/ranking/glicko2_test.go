package ranking

import (
	"math"
	"testing"
)

func TestGlicko2PaperVector(t *testing.T) {
	svc := NewGlicko2Service(DefaultConfig())
	current := Rating{R: 1500, RD: 200, Sigma: 0.06}
	opps := []OpponentResult{
		{Opponent: Rating{R: 1400, RD: 30, Sigma: 0.06}, Score: ScoreWin},
		{Opponent: Rating{R: 1550, RD: 100, Sigma: 0.06}, Score: ScoreLoss},
		{Opponent: Rating{R: 1700, RD: 300, Sigma: 0.06}, Score: ScoreLoss},
	}

	next, err := svc.Update(current, opps)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	if math.Abs(next.R-1464.06) > 0.2 {
		t.Fatalf("rating R = %.4f, want ~1464.06", next.R)
	}
	if math.Abs(next.RD-151.52) > 0.2 {
		t.Fatalf("rating RD = %.4f, want ~151.52", next.RD)
	}
	if math.Abs(next.Sigma-0.05999) > 0.002 {
		t.Fatalf("rating Sigma = %.6f, want ~0.05999", next.Sigma)
	}
}

func TestGlicko2DrawSupported(t *testing.T) {
	svc := NewGlicko2Service(DefaultConfig())
	current := Rating{R: 1500, RD: 350, Sigma: 0.06}
	next, err := svc.Update(current, []OpponentResult{{
		Opponent: Rating{R: 1500, RD: 350, Sigma: 0.06},
		Score:    ScoreDraw,
	}})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if math.IsNaN(next.R) || math.IsNaN(next.RD) || math.IsNaN(next.Sigma) {
		t.Fatalf("received NaN result: %+v", next)
	}
	if next.RD >= current.RD {
		t.Fatalf("expected RD reduction after played game; got current=%.4f next=%.4f", current.RD, next.RD)
	}
}

func TestGlicko2InvalidConfig(t *testing.T) {
	svc := NewGlicko2Service(Glicko2Config{
		Tau:           -0.5,
		Epsilon:       1e-6,
		DefaultRating: Rating{R: 1500, RD: 350, Sigma: 0.06},
	})
	_, err := svc.Update(Rating{R: 1500, RD: 350, Sigma: 0.06}, []OpponentResult{{
		Opponent: Rating{R: 1500, RD: 350, Sigma: 0.06},
		Score:    ScoreWin,
	}})
	if err == nil {
		t.Fatalf("expected error for invalid config")
	}
}

func TestSeasonSoftResetPolicy(t *testing.T) {
	prev := Rating{R: 1600, RD: 340, Sigma: 0.07}
	reset := ApplySeasonSoftReset(prev)

	if math.Abs(reset.R-1575) > 1e-9 {
		t.Fatalf("reset R = %.4f, want 1575", reset.R)
	}
	if math.Abs(reset.RD-350) > 1e-9 {
		t.Fatalf("reset RD = %.4f, want 350", reset.RD)
	}
	if math.Abs(reset.Sigma-0.07) > 1e-9 {
		t.Fatalf("reset sigma = %.5f, want 0.07", reset.Sigma)
	}
}

func TestGlicko2NoOpponentsIncreasesUncertainty(t *testing.T) {
	svc := NewGlicko2Service(DefaultConfig())
	current := Rating{R: 1500, RD: 200, Sigma: 0.06}
	next, err := svc.Update(current, nil)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if next.RD <= current.RD {
		t.Fatalf("expected RD increase with inactivity; got current=%.4f next=%.4f", current.RD, next.RD)
	}
}
