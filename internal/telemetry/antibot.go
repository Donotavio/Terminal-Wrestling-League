package telemetry

import (
	"fmt"
	"math"
	"strings"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
)

// TurnObservation captures one human decision used by anti-bot heuristics.
type TurnObservation struct {
	DecisionMS int
	IsOptimal  bool
}

// AntiBotMetrics are computed from a set of turn observations.
type AntiBotMetrics struct {
	DecisionCount       int
	MeanDecisionMS      float64
	DecisionVarianceMS2 float64
	OptimalPickRate     float64
}

// AntiBotEvaluation captures score, decision and textual reason.
type AntiBotEvaluation struct {
	Metrics        AntiBotMetrics
	SuspicionScore float64
	Flagged        bool
	Reason         string
}

func ComputeAntiBotMetrics(observations []TurnObservation) AntiBotMetrics {
	if len(observations) == 0 {
		return AntiBotMetrics{}
	}
	sum := 0.0
	optimal := 0
	for _, obs := range observations {
		sum += float64(obs.DecisionMS)
		if obs.IsOptimal {
			optimal++
		}
	}
	mean := sum / float64(len(observations))
	varianceAcc := 0.0
	for _, obs := range observations {
		delta := float64(obs.DecisionMS) - mean
		varianceAcc += delta * delta
	}
	variance := varianceAcc / float64(len(observations))
	return AntiBotMetrics{
		DecisionCount:       len(observations),
		MeanDecisionMS:      mean,
		DecisionVarianceMS2: variance,
		OptimalPickRate:     float64(optimal) / float64(len(observations)),
	}
}

func EvaluateAntiBot(metrics AntiBotMetrics, cfg storage.AntiBotConfig) AntiBotEvaluation {
	if cfg.MinDecisions <= 0 {
		cfg.MinDecisions = storage.DefaultAntiBotConfig().MinDecisions
	}

	score := 0.0
	reasonParts := make([]string, 0, 4)
	if metrics.MeanDecisionMS <= cfg.MaxMeanDecisionMS {
		score += 0.40
		reasonParts = append(reasonParts, "mean_decision_ok")
	} else {
		reasonParts = append(reasonParts, "mean_decision_high")
	}
	if metrics.DecisionVarianceMS2 <= cfg.MinDecisionVarianceMS2 {
		score += 0.30
		reasonParts = append(reasonParts, "variance_low")
	} else {
		reasonParts = append(reasonParts, "variance_high")
	}
	if metrics.OptimalPickRate >= cfg.MinOptimalPickRate {
		score += 0.30
		reasonParts = append(reasonParts, "optimal_rate_high")
	} else {
		reasonParts = append(reasonParts, "optimal_rate_low")
	}

	score = math.Round(score*1000) / 1000
	flagged := metrics.DecisionCount >= cfg.MinDecisions && score >= cfg.SuspicionScoreThreshold
	if metrics.DecisionCount < cfg.MinDecisions {
		reasonParts = append(reasonParts, fmt.Sprintf("insufficient_decisions:%d", metrics.DecisionCount))
	}

	return AntiBotEvaluation{
		Metrics:        metrics,
		SuspicionScore: score,
		Flagged:        flagged,
		Reason:         strings.Join(reasonParts, ";"),
	}
}
