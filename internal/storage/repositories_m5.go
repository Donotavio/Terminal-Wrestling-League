package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TutorialResult string

const (
	TutorialResultWin  TutorialResult = "win"
	TutorialResultLoss TutorialResult = "loss"
	TutorialResultDraw TutorialResult = "draw"
)

type PlayerProfile struct {
	PlayerID            string
	TutorialCompleted   bool
	TutorialCompletedAt *time.Time
	TutorialRuns        int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type TutorialRun struct {
	ID         string
	PlayerID   string
	Result     TutorialResult
	StartedAt  time.Time
	EndedAt    time.Time
	DurationMS int
	CreatedAt  time.Time
}

type MatchTurnTelemetry struct {
	MatchID           string
	Turn              int
	PlayerID          string
	IsHuman           bool
	Action            string
	TargetZone        string
	DecisionMS        int
	Success           bool
	HPBefore          int
	HPAfter           int
	StaminaBefore     int
	StaminaAfter      int
	MomentumBefore    int
	MomentumAfter     int
	IsOptimalChoice   bool
	OptimalAction     string
	OptimalTargetZone string
	CreatedAt         time.Time
}

type MatchSummaryTelemetry struct {
	MatchID            string
	SeasonID           string
	Player1ID          string
	Player2ID          string
	WinnerID           *string
	ResultType         string
	TurnCount          int
	DurationMS         int
	AvgDecisionMSP1    float64
	AvgDecisionMSP2    float64
	AvgStaminaP1       float64
	AvgStaminaP2       float64
	AvgMomentumP1      float64
	AvgMomentumP2      float64
	MaxComboP1         int
	MaxComboP2         int
	StunEvents         int
	SubmissionAttempts int
	QueueWaitMSP1      int64
	QueueWaitMSP2      int64
	CreatedAt          time.Time
}

type AntiBotConfig struct {
	MinDecisions            int
	MaxMeanDecisionMS       float64
	MinDecisionVarianceMS2  float64
	MinOptimalPickRate      float64
	SuspicionScoreThreshold float64
	OptimalityEpsilon       float64
}

type AntiBotFlag struct {
	ID                  string
	PlayerID            string
	SeasonID            string
	MatchID             string
	DecisionCount       int
	MeanDecisionMS      float64
	DecisionVarianceMS2 float64
	OptimalPickRate     float64
	SuspicionScore      float64
	Flagged             bool
	Reason              string
	CreatedAt           time.Time
}

type SessionTelemetryEvent struct {
	ID             string
	PlayerID       *string
	Handle         string
	RemoteAddrHash string
	EventType      string
	Detail         map[string]any
	CreatedAt      time.Time
}

type QueueTelemetryEvent struct {
	ID          string
	PlayerID    string
	EventType   string
	QueueWaitMS int64
	CreatedAt   time.Time
}

type SpectatorTelemetryEvent struct {
	ID                string
	SpectatorPlayerID string
	TargetPlayerID    string
	MatchID           *string
	EventType         string
	WaitMS            int64
	Detail            map[string]any
	CreatedAt         time.Time
}

func DefaultAntiBotConfig() AntiBotConfig {
	return AntiBotConfig{
		MinDecisions:            12,
		MaxMeanDecisionMS:       180,
		MinDecisionVarianceMS2:  2500,
		MinOptimalPickRate:      0.82,
		SuspicionScoreThreshold: 0.75,
		OptimalityEpsilon:       0.05,
	}
}

func (r *SQLRepositories) GetOrCreateProfile(ctx context.Context, playerID string) (PlayerProfile, error) {
	if r.pool == nil {
		return PlayerProfile{}, fmt.Errorf("nil db pool")
	}
	if playerID == "" {
		return PlayerProfile{}, fmt.Errorf("player id is required")
	}

	profile, err := r.getProfile(ctx, playerID)
	if err == nil {
		return profile, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return PlayerProfile{}, err
	}

	now := r.nowFn()
	profile = PlayerProfile{
		PlayerID:          playerID,
		TutorialCompleted: false,
		TutorialRuns:      0,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO player_profiles (
		   player_id, tutorial_completed, tutorial_completed_at, tutorial_runs, created_at, updated_at
		 ) VALUES ($1, $2, $3, $4, $5, $6)`,
		profile.PlayerID,
		profile.TutorialCompleted,
		profile.TutorialCompletedAt,
		profile.TutorialRuns,
		profile.CreatedAt,
		profile.UpdatedAt,
	)
	if err != nil {
		return PlayerProfile{}, fmt.Errorf("insert player profile: %w", err)
	}
	return profile, nil
}

func (r *SQLRepositories) MarkTutorialCompleted(ctx context.Context, playerID string, now time.Time) (PlayerProfile, error) {
	if r.pool == nil {
		return PlayerProfile{}, fmt.Errorf("nil db pool")
	}
	if playerID == "" {
		return PlayerProfile{}, fmt.Errorf("player id is required")
	}
	if now.IsZero() {
		now = r.nowFn()
	}

	_, err := r.pool.Exec(ctx,
		`INSERT INTO player_profiles (
		   player_id, tutorial_completed, tutorial_completed_at, tutorial_runs, created_at, updated_at
		 ) VALUES ($1, TRUE, $2, 0, $2, $2)
		 ON CONFLICT (player_id)
		 DO UPDATE SET
		   tutorial_completed = TRUE,
		   tutorial_completed_at = COALESCE(player_profiles.tutorial_completed_at, EXCLUDED.tutorial_completed_at),
		   updated_at = EXCLUDED.updated_at`,
		playerID,
		now.UTC(),
	)
	if err != nil {
		return PlayerProfile{}, fmt.Errorf("mark tutorial completed: %w", err)
	}
	return r.getProfile(ctx, playerID)
}

func (r *SQLRepositories) CreateTutorialRun(ctx context.Context, run TutorialRun) (TutorialRun, error) {
	if r.pool == nil {
		return TutorialRun{}, fmt.Errorf("nil db pool")
	}
	if run.PlayerID == "" {
		return TutorialRun{}, fmt.Errorf("player id is required")
	}
	if run.Result != TutorialResultWin && run.Result != TutorialResultLoss && run.Result != TutorialResultDraw {
		return TutorialRun{}, fmt.Errorf("invalid tutorial result %q", run.Result)
	}
	if run.StartedAt.IsZero() || run.EndedAt.IsZero() {
		return TutorialRun{}, fmt.Errorf("started_at and ended_at are required")
	}
	if run.EndedAt.Before(run.StartedAt) {
		return TutorialRun{}, fmt.Errorf("ended_at must be >= started_at")
	}
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = r.nowFn()
	}
	run.DurationMS = int(run.EndedAt.Sub(run.StartedAt).Milliseconds())

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return TutorialRun{}, fmt.Errorf("begin tutorial run tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO tutorial_runs (
		   id, player_id, result, started_at, ended_at, duration_ms, created_at
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		run.ID,
		run.PlayerID,
		run.Result,
		run.StartedAt.UTC(),
		run.EndedAt.UTC(),
		run.DurationMS,
		run.CreatedAt.UTC(),
	)
	if err != nil {
		return TutorialRun{}, fmt.Errorf("insert tutorial run: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO player_profiles (
		   player_id, tutorial_completed, tutorial_completed_at, tutorial_runs, created_at, updated_at
		 ) VALUES ($1, FALSE, NULL, 1, $2, $2)
		 ON CONFLICT (player_id)
		 DO UPDATE SET
		   tutorial_runs = player_profiles.tutorial_runs + 1,
		   updated_at = EXCLUDED.updated_at`,
		run.PlayerID,
		run.CreatedAt.UTC(),
	)
	if err != nil {
		return TutorialRun{}, fmt.Errorf("increment tutorial run counter: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return TutorialRun{}, fmt.Errorf("commit tutorial run tx: %w", err)
	}
	return run, nil
}

func (r *SQLRepositories) LoadAntiBotConfig(ctx context.Context) (AntiBotConfig, error) {
	if r.pool == nil {
		return AntiBotConfig{}, fmt.Errorf("nil db pool")
	}
	cfg := DefaultAntiBotConfig()
	rows, err := r.pool.Query(ctx, `SELECT key, value_numeric FROM anti_bot_config`)
	if err != nil {
		return AntiBotConfig{}, fmt.Errorf("query anti bot config: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value *float64
		if err := rows.Scan(&key, &value); err != nil {
			return AntiBotConfig{}, fmt.Errorf("scan anti bot config: %w", err)
		}
		if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
			continue
		}
		switch key {
		case "min_decisions":
			cfg.MinDecisions = int(*value)
		case "max_mean_decision_ms":
			cfg.MaxMeanDecisionMS = *value
		case "min_decision_variance_ms2":
			cfg.MinDecisionVarianceMS2 = *value
		case "min_optimal_pick_rate":
			cfg.MinOptimalPickRate = *value
		case "suspicion_score_threshold":
			cfg.SuspicionScoreThreshold = *value
		case "optimality_epsilon":
			cfg.OptimalityEpsilon = *value
		}
	}
	if err := rows.Err(); err != nil {
		return AntiBotConfig{}, fmt.Errorf("iterate anti bot config: %w", err)
	}
	return cfg, nil
}

func (r *SQLRepositories) CreateAntiBotFlag(ctx context.Context, flag AntiBotFlag) (AntiBotFlag, error) {
	if r.pool == nil {
		return AntiBotFlag{}, fmt.Errorf("nil db pool")
	}
	if flag.PlayerID == "" || flag.SeasonID == "" || flag.MatchID == "" {
		return AntiBotFlag{}, fmt.Errorf("player_id, season_id and match_id are required")
	}
	if flag.ID == "" {
		flag.ID = uuid.NewString()
	}
	if flag.CreatedAt.IsZero() {
		flag.CreatedAt = r.nowFn()
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO anti_bot_flags (
		   id, player_id, season_id, match_id, decision_count,
		   mean_decision_ms, decision_variance_ms2, optimal_pick_rate,
		   suspicion_score, flagged, reason, created_at
		 ) VALUES (
		   $1, $2, $3, $4, $5,
		   $6, $7, $8,
		   $9, $10, $11, $12
		 )`,
		flag.ID,
		flag.PlayerID,
		flag.SeasonID,
		flag.MatchID,
		flag.DecisionCount,
		flag.MeanDecisionMS,
		flag.DecisionVarianceMS2,
		flag.OptimalPickRate,
		flag.SuspicionScore,
		flag.Flagged,
		flag.Reason,
		flag.CreatedAt.UTC(),
	)
	if err != nil {
		return AntiBotFlag{}, fmt.Errorf("insert anti bot flag: %w", err)
	}
	return flag, nil
}

func (r *SQLRepositories) InsertTurnTelemetryBatch(ctx context.Context, rows []MatchTurnTelemetry) error {
	if r.pool == nil {
		return fmt.Errorf("nil db pool")
	}
	if len(rows) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin turn telemetry tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, row := range rows {
		if row.MatchID == "" || row.PlayerID == "" || row.Turn <= 0 {
			return fmt.Errorf("invalid turn telemetry row")
		}
		createdAt := row.CreatedAt
		if createdAt.IsZero() {
			createdAt = r.nowFn()
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO telemetry_match_turns (
			   match_id, turn, player_id, is_human, action, target_zone,
			   decision_ms, success, hp_before, hp_after,
			   stamina_before, stamina_after, momentum_before, momentum_after,
			   is_optimal_choice, optimal_action, optimal_target_zone, created_at
			 ) VALUES (
			   $1, $2, $3, $4, $5, $6,
			   $7, $8, $9, $10,
			   $11, $12, $13, $14,
			   $15, $16, $17, $18
			 )`,
			row.MatchID,
			row.Turn,
			row.PlayerID,
			row.IsHuman,
			row.Action,
			row.TargetZone,
			row.DecisionMS,
			row.Success,
			row.HPBefore,
			row.HPAfter,
			row.StaminaBefore,
			row.StaminaAfter,
			row.MomentumBefore,
			row.MomentumAfter,
			row.IsOptimalChoice,
			row.OptimalAction,
			row.OptimalTargetZone,
			createdAt.UTC(),
		)
		if err != nil {
			return fmt.Errorf("insert turn telemetry: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit turn telemetry tx: %w", err)
	}
	return nil
}

func (r *SQLRepositories) InsertMatchSummaryTelemetry(ctx context.Context, summary MatchSummaryTelemetry) (MatchSummaryTelemetry, error) {
	if r.pool == nil {
		return MatchSummaryTelemetry{}, fmt.Errorf("nil db pool")
	}
	if summary.MatchID == "" || summary.SeasonID == "" || summary.Player1ID == "" || summary.Player2ID == "" {
		return MatchSummaryTelemetry{}, fmt.Errorf("match_id, season_id and player ids are required")
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = r.nowFn()
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO telemetry_match_summaries (
		   match_id, season_id, player1_id, player2_id, winner_id, result_type,
		   turn_count, duration_ms,
		   avg_decision_ms_p1, avg_decision_ms_p2,
		   avg_stamina_p1, avg_stamina_p2,
		   avg_momentum_p1, avg_momentum_p2,
		   max_combo_p1, max_combo_p2,
		   stun_events, submission_attempts,
		   queue_wait_ms_p1, queue_wait_ms_p2, created_at
		 ) VALUES (
		   $1, $2, $3, $4, $5, $6,
		   $7, $8,
		   $9, $10,
		   $11, $12,
		   $13, $14,
		   $15, $16,
		   $17, $18,
		   $19, $20, $21
		 )
		 ON CONFLICT (match_id)
		 DO UPDATE SET
		   season_id = EXCLUDED.season_id,
		   player1_id = EXCLUDED.player1_id,
		   player2_id = EXCLUDED.player2_id,
		   winner_id = EXCLUDED.winner_id,
		   result_type = EXCLUDED.result_type,
		   turn_count = EXCLUDED.turn_count,
		   duration_ms = EXCLUDED.duration_ms,
		   avg_decision_ms_p1 = EXCLUDED.avg_decision_ms_p1,
		   avg_decision_ms_p2 = EXCLUDED.avg_decision_ms_p2,
		   avg_stamina_p1 = EXCLUDED.avg_stamina_p1,
		   avg_stamina_p2 = EXCLUDED.avg_stamina_p2,
		   avg_momentum_p1 = EXCLUDED.avg_momentum_p1,
		   avg_momentum_p2 = EXCLUDED.avg_momentum_p2,
		   max_combo_p1 = EXCLUDED.max_combo_p1,
		   max_combo_p2 = EXCLUDED.max_combo_p2,
		   stun_events = EXCLUDED.stun_events,
		   submission_attempts = EXCLUDED.submission_attempts,
		   queue_wait_ms_p1 = EXCLUDED.queue_wait_ms_p1,
		   queue_wait_ms_p2 = EXCLUDED.queue_wait_ms_p2,
		   created_at = EXCLUDED.created_at`,
		summary.MatchID,
		summary.SeasonID,
		summary.Player1ID,
		summary.Player2ID,
		summary.WinnerID,
		summary.ResultType,
		summary.TurnCount,
		summary.DurationMS,
		summary.AvgDecisionMSP1,
		summary.AvgDecisionMSP2,
		summary.AvgStaminaP1,
		summary.AvgStaminaP2,
		summary.AvgMomentumP1,
		summary.AvgMomentumP2,
		summary.MaxComboP1,
		summary.MaxComboP2,
		summary.StunEvents,
		summary.SubmissionAttempts,
		summary.QueueWaitMSP1,
		summary.QueueWaitMSP2,
		summary.CreatedAt.UTC(),
	)
	if err != nil {
		return MatchSummaryTelemetry{}, fmt.Errorf("insert match summary telemetry: %w", err)
	}
	return summary, nil
}

func (r *SQLRepositories) CreateSessionTelemetryEvent(ctx context.Context, event SessionTelemetryEvent) (SessionTelemetryEvent, error) {
	if r.pool == nil {
		return SessionTelemetryEvent{}, fmt.Errorf("nil db pool")
	}
	if event.Handle == "" || event.RemoteAddrHash == "" || event.EventType == "" {
		return SessionTelemetryEvent{}, fmt.Errorf("handle, remote addr hash and event type are required")
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = r.nowFn()
	}
	detailJSON, err := marshalDetail(event.Detail)
	if err != nil {
		return SessionTelemetryEvent{}, err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO telemetry_session_events (
		   id, player_id, handle, remote_addr_hash, event_type, detail_json, created_at
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ID,
		event.PlayerID,
		event.Handle,
		event.RemoteAddrHash,
		event.EventType,
		detailJSON,
		event.CreatedAt.UTC(),
	)
	if err != nil {
		return SessionTelemetryEvent{}, fmt.Errorf("insert session telemetry event: %w", err)
	}
	return event, nil
}

func (r *SQLRepositories) CreateQueueTelemetryEvent(ctx context.Context, event QueueTelemetryEvent) (QueueTelemetryEvent, error) {
	if r.pool == nil {
		return QueueTelemetryEvent{}, fmt.Errorf("nil db pool")
	}
	if event.PlayerID == "" || event.EventType == "" {
		return QueueTelemetryEvent{}, fmt.Errorf("player id and event type are required")
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = r.nowFn()
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO telemetry_queue_events (
		   id, player_id, event_type, queue_wait_ms, created_at
		 ) VALUES ($1, $2, $3, $4, $5)`,
		event.ID,
		event.PlayerID,
		event.EventType,
		event.QueueWaitMS,
		event.CreatedAt.UTC(),
	)
	if err != nil {
		return QueueTelemetryEvent{}, fmt.Errorf("insert queue telemetry event: %w", err)
	}
	return event, nil
}

func (r *SQLRepositories) CreateSpectatorTelemetryEvent(ctx context.Context, event SpectatorTelemetryEvent) (SpectatorTelemetryEvent, error) {
	if r.pool == nil {
		return SpectatorTelemetryEvent{}, fmt.Errorf("nil db pool")
	}
	if event.SpectatorPlayerID == "" || event.TargetPlayerID == "" || event.EventType == "" {
		return SpectatorTelemetryEvent{}, fmt.Errorf("spectator id, target id and event type are required")
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = r.nowFn()
	}
	detailJSON, err := marshalDetail(event.Detail)
	if err != nil {
		return SpectatorTelemetryEvent{}, err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO telemetry_spectator_events (
		   id, spectator_player_id, target_player_id, match_id, event_type, wait_ms, detail_json, created_at
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID,
		event.SpectatorPlayerID,
		event.TargetPlayerID,
		event.MatchID,
		event.EventType,
		event.WaitMS,
		detailJSON,
		event.CreatedAt.UTC(),
	)
	if err != nil {
		return SpectatorTelemetryEvent{}, fmt.Errorf("insert spectator telemetry event: %w", err)
	}
	return event, nil
}

func (r *SQLRepositories) getProfile(ctx context.Context, playerID string) (PlayerProfile, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT player_id, tutorial_completed, tutorial_completed_at, tutorial_runs, created_at, updated_at
		 FROM player_profiles
		 WHERE player_id = $1`,
		playerID,
	)
	var profile PlayerProfile
	if err := row.Scan(
		&profile.PlayerID,
		&profile.TutorialCompleted,
		&profile.TutorialCompletedAt,
		&profile.TutorialRuns,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlayerProfile{}, ErrNotFound
		}
		return PlayerProfile{}, fmt.Errorf("get player profile: %w", err)
	}
	return profile, nil
}

func marshalDetail(detail map[string]any) ([]byte, error) {
	if detail == nil {
		detail = map[string]any{}
	}
	data, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal detail json: %w", err)
	}
	return data, nil
}
