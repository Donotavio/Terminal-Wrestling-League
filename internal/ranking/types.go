package ranking

import "time"

// Rating stores Glicko-2 player parameters.
type Rating struct {
	R     float64
	RD    float64
	Sigma float64
}

// MatchScore is the player's score in one match.
type MatchScore float64

const (
	ScoreLoss MatchScore = 0.0
	ScoreDraw MatchScore = 0.5
	ScoreWin  MatchScore = 1.0
)

func (s MatchScore) IsValid() bool {
	return s == ScoreLoss || s == ScoreDraw || s == ScoreWin
}

// MatchResult is a persisted or domain representation of one match outcome.
type MatchResult struct {
	PlayerID   string
	OpponentID string
	Score      MatchScore
	PlayedAt   time.Time
}

// OpponentResult stores one opponent rating and observed score.
type OpponentResult struct {
	Opponent Rating
	Score    MatchScore
}

// Service updates ratings after a rating period.
type Service interface {
	Update(current Rating, opponents []OpponentResult) (Rating, error)
}
