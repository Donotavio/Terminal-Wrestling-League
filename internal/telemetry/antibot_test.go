package telemetry

import (
	"math"
	"testing"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
)

func TestComputeAntiBotMetrics(t *testing.T) {
	metrics := ComputeAntiBotMetrics([]TurnObservation{
		{DecisionMS: 100, IsOptimal: true},
		{DecisionMS: 200, IsOptimal: false},
		{DecisionMS: 300, IsOptimal: true},
	})

	if metrics.DecisionCount != 3 {
		t.Fatalf("decision count = %d, want 3", metrics.DecisionCount)
	}
	if math.Abs(metrics.MeanDecisionMS-200) > 0.000001 {
		t.Fatalf("mean decision ms = %f, want 200", metrics.MeanDecisionMS)
	}
	if math.Abs(metrics.DecisionVarianceMS2-6666.666666666667) > 0.000001 {
		t.Fatalf("variance = %f, want 6666.666666666667", metrics.DecisionVarianceMS2)
	}
	if math.Abs(metrics.OptimalPickRate-(2.0/3.0)) > 0.000001 {
		t.Fatalf("optimal pick rate = %f, want 0.666666...", metrics.OptimalPickRate)
	}
}

func TestEvaluateAntiBotFlagsWithThreshold(t *testing.T) {
	cfg := storage.DefaultAntiBotConfig()
	cfg.MinDecisions = 3
	cfg.SuspicionScoreThreshold = 0.7
	eval := EvaluateAntiBot(AntiBotMetrics{
		DecisionCount:       3,
		MeanDecisionMS:      120,
		DecisionVarianceMS2: 1200,
		OptimalPickRate:     0.9,
	}, cfg)

	if !eval.Flagged {
		t.Fatalf("expected flagged=true, got false")
	}
	if eval.SuspicionScore < 0.9 {
		t.Fatalf("unexpected low suspicion score: %f", eval.SuspicionScore)
	}
}
