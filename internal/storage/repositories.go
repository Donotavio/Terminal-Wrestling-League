package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/ranking"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type SeasonStatus string

const (
	SeasonStatusActive SeasonStatus = "active"
	SeasonStatusClosed SeasonStatus = "closed"
)

type MatchResultType string

const (
	MatchResultKO         MatchResultType = "ko"
	MatchResultSubmission MatchResultType = "submission"
	MatchResultAbandon    MatchResultType = "abandon"
	MatchResultDraw       MatchResultType = "draw"
)

type Player struct {
	ID        string
	Handle    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Season struct {
	ID        string
	Number    int
	StartsAt  time.Time
	EndsAt    time.Time
	Status    SeasonStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Match struct {
	ID         string
	SeasonID   string
	Player1ID  string
	Player2ID  string
	WinnerID   *string
	ResultType MatchResultType
	StartedAt  time.Time
	EndedAt    time.Time
	DurationMS int
	CreatedAt  time.Time
}

type RatingEntry struct {
	PlayerID    string
	SeasonID    string
	Rating      float64
	RD          float64
	Sigma       float64
	LastMatchAt *time.Time
	UpdatedAt   time.Time
}

type MatchReplayTurnWrite struct {
	Turn       int
	RelativeMS int64
	Inputs     []combat.TurnInput
	Checksums  combat.TurnChecksum
}

type MatchReplayWrite struct {
	Seed         uint64
	InitialState combat.MatchState
	Turns        []MatchReplayTurnWrite
}

type MatchReplayTurn struct {
	Turn       int
	RelativeMS int64
	Inputs     []combat.TurnInput
	Checksums  combat.TurnChecksum
	CreatedAt  time.Time
}

type MatchReplay struct {
	MatchID      string
	Seed         uint64
	InitialState combat.MatchState
	Turns        []MatchReplayTurn
	CreatedAt    time.Time
}

type FinalizeMatchParams struct {
	MatchID    string
	Player1ID  string
	Player2ID  string
	WinnerID   *string
	ResultType MatchResultType
	StartedAt  time.Time
	EndedAt    time.Time
	Replay     *MatchReplayWrite
}

type FinalizedMatch struct {
	Match        Match
	Season       Season
	Player1Old   ranking.Rating
	Player1New   ranking.Rating
	Player2Old   ranking.Rating
	Player2New   ranking.Rating
	Player1ID    string
	Player2ID    string
	Player1Score ranking.MatchScore
	Player2Score ranking.MatchScore
}

type PlayerRepository interface {
	Create(ctx context.Context, handle string) (Player, error)
	GetByID(ctx context.Context, id string) (Player, error)
	GetByHandle(ctx context.Context, handle string) (Player, error)
}

type SeasonRepository interface {
	GetActive(ctx context.Context) (Season, error)
	EnsureActive(ctx context.Context, now time.Time) (Season, error)
	CloseExpiredAndCreateNext(ctx context.Context, now time.Time) (Season, error)
}

type MatchRepository interface {
	CreateMatch(ctx context.Context, match Match) (Match, error)
	FinalizeMatch(ctx context.Context, params FinalizeMatchParams) (FinalizedMatch, error)
	GetMatchReplay(ctx context.Context, matchID string) (MatchReplay, error)
}

type RatingRepository interface {
	GetOrCreateForActiveSeason(ctx context.Context, playerID string, now time.Time) (RatingEntry, error)
	Upsert(ctx context.Context, entry RatingEntry) error
}

// SQLRepositories implements all repository contracts using pgxpool.
type SQLRepositories struct {
	pool           *pgxpool.Pool
	rankingService ranking.Service
	nowFn          func() time.Time
	seasonDuration time.Duration
}

func NewSQLRepositories(pool *pgxpool.Pool, rankingService ranking.Service) *SQLRepositories {
	if rankingService == nil {
		rankingService = ranking.NewGlicko2Service(ranking.DefaultConfig())
	}
	return &SQLRepositories{
		pool:           pool,
		rankingService: rankingService,
		nowFn:          func() time.Time { return time.Now().UTC() },
		seasonDuration: 30 * 24 * time.Hour,
	}
}

func (r *SQLRepositories) Create(ctx context.Context, handle string) (Player, error) {
	if r.pool == nil {
		return Player{}, fmt.Errorf("nil db pool")
	}
	now := r.nowFn()
	p := Player{ID: uuid.NewString(), Handle: handle, CreatedAt: now, UpdatedAt: now}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO players (id, handle, created_at, updated_at)
		 VALUES ($1, $2, $3, $4)`,
		p.ID, p.Handle, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return Player{}, fmt.Errorf("insert player: %w", err)
	}
	return p, nil
}

func (r *SQLRepositories) GetByID(ctx context.Context, id string) (Player, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, handle, created_at, updated_at
		 FROM players
		 WHERE id = $1`,
		id,
	)
	var p Player
	if err := row.Scan(&p.ID, &p.Handle, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Player{}, ErrNotFound
		}
		return Player{}, fmt.Errorf("get player by id: %w", err)
	}
	return p, nil
}

func (r *SQLRepositories) GetByHandle(ctx context.Context, handle string) (Player, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, handle, created_at, updated_at
		 FROM players
		 WHERE handle = $1`,
		handle,
	)
	var p Player
	if err := row.Scan(&p.ID, &p.Handle, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Player{}, ErrNotFound
		}
		return Player{}, fmt.Errorf("get player by handle: %w", err)
	}
	return p, nil
}

func (r *SQLRepositories) GetActive(ctx context.Context) (Season, error) {
	if r.pool == nil {
		return Season{}, fmt.Errorf("nil db pool")
	}
	return getActiveSeasonFromQuerier(ctx, r.pool)
}

func (r *SQLRepositories) EnsureActive(ctx context.Context, now time.Time) (Season, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Season{}, fmt.Errorf("begin ensure active tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	season, err := r.ensureActiveSeasonTx(ctx, tx, now.UTC())
	if err != nil {
		return Season{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Season{}, fmt.Errorf("commit ensure active tx: %w", err)
	}
	return season, nil
}

func (r *SQLRepositories) CloseExpiredAndCreateNext(ctx context.Context, now time.Time) (Season, error) {
	return r.EnsureActive(ctx, now)
}

func (r *SQLRepositories) CreateMatch(ctx context.Context, m Match) (Match, error) {
	if r.pool == nil {
		return Match{}, fmt.Errorf("nil db pool")
	}
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = r.nowFn()
	}
	if m.StartedAt.IsZero() || m.EndedAt.IsZero() {
		return Match{}, fmt.Errorf("started_at and ended_at are required")
	}
	if m.DurationMS < 0 {
		return Match{}, fmt.Errorf("duration must be >= 0")
	}

	_, err := r.pool.Exec(ctx,
		`INSERT INTO matches (
		   id, season_id, player1_id, player2_id, winner_id,
		   result_type, started_at, ended_at, duration_ms, created_at
		 ) VALUES (
		   $1, $2, $3, $4, $5,
		   $6, $7, $8, $9, $10
		 )`,
		m.ID, m.SeasonID, m.Player1ID, m.Player2ID, m.WinnerID,
		m.ResultType, m.StartedAt, m.EndedAt, m.DurationMS, m.CreatedAt,
	)
	if err != nil {
		return Match{}, fmt.Errorf("insert match: %w", err)
	}
	return m, nil
}

func (r *SQLRepositories) FinalizeMatch(ctx context.Context, params FinalizeMatchParams) (FinalizedMatch, error) {
	if r.pool == nil {
		return FinalizedMatch{}, fmt.Errorf("nil db pool")
	}
	if params.Player1ID == "" || params.Player2ID == "" {
		return FinalizedMatch{}, fmt.Errorf("both players are required")
	}
	if params.Player1ID == params.Player2ID {
		return FinalizedMatch{}, fmt.Errorf("player ids must be different")
	}
	if params.StartedAt.IsZero() || params.EndedAt.IsZero() {
		return FinalizedMatch{}, fmt.Errorf("started_at and ended_at are required")
	}
	if params.EndedAt.Before(params.StartedAt) {
		return FinalizedMatch{}, fmt.Errorf("ended_at must be >= started_at")
	}
	if params.MatchID == "" {
		params.MatchID = uuid.NewString()
	}

	score1, score2, err := scoresForResult(params)
	if err != nil {
		return FinalizedMatch{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return FinalizedMatch{}, fmt.Errorf("begin finalize tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	endedUTC := params.EndedAt.UTC()
	season, err := r.ensureActiveSeasonTx(ctx, tx, endedUTC)
	if err != nil {
		return FinalizedMatch{}, err
	}

	entry1, err := r.getOrCreateRatingTx(ctx, tx, params.Player1ID, season, endedUTC)
	if err != nil {
		return FinalizedMatch{}, err
	}
	entry2, err := r.getOrCreateRatingTx(ctx, tx, params.Player2ID, season, endedUTC)
	if err != nil {
		return FinalizedMatch{}, err
	}

	old1 := ranking.Rating{R: entry1.Rating, RD: entry1.RD, Sigma: entry1.Sigma}
	old2 := ranking.Rating{R: entry2.Rating, RD: entry2.RD, Sigma: entry2.Sigma}

	new1, err := r.rankingService.Update(old1, []ranking.OpponentResult{{Opponent: old2, Score: score1}})
	if err != nil {
		return FinalizedMatch{}, fmt.Errorf("update rating player1: %w", err)
	}
	new2, err := r.rankingService.Update(old2, []ranking.OpponentResult{{Opponent: old1, Score: score2}})
	if err != nil {
		return FinalizedMatch{}, fmt.Errorf("update rating player2: %w", err)
	}

	durationMS := int(params.EndedAt.Sub(params.StartedAt).Milliseconds())
	createdAt := r.nowFn()
	match := Match{
		ID:         params.MatchID,
		SeasonID:   season.ID,
		Player1ID:  params.Player1ID,
		Player2ID:  params.Player2ID,
		WinnerID:   params.WinnerID,
		ResultType: params.ResultType,
		StartedAt:  params.StartedAt.UTC(),
		EndedAt:    endedUTC,
		DurationMS: durationMS,
		CreatedAt:  createdAt,
	}
	if err := insertMatchTx(ctx, tx, match); err != nil {
		return FinalizedMatch{}, err
	}
	if params.Replay != nil {
		if err := insertMatchReplayTx(ctx, tx, match.ID, *params.Replay, createdAt); err != nil {
			return FinalizedMatch{}, err
		}
	}

	if err := insertMatchResultTx(ctx, tx, match.ID, params.Player1ID, params.Player2ID, score1, old1, new1, createdAt); err != nil {
		return FinalizedMatch{}, err
	}
	if err := insertMatchResultTx(ctx, tx, match.ID, params.Player2ID, params.Player1ID, score2, old2, new2, createdAt); err != nil {
		return FinalizedMatch{}, err
	}

	entry1.Rating = new1.R
	entry1.RD = new1.RD
	entry1.Sigma = new1.Sigma
	entry1.LastMatchAt = &endedUTC
	entry1.UpdatedAt = createdAt
	if err := upsertRatingTx(ctx, tx, entry1); err != nil {
		return FinalizedMatch{}, err
	}

	entry2.Rating = new2.R
	entry2.RD = new2.RD
	entry2.Sigma = new2.Sigma
	entry2.LastMatchAt = &endedUTC
	entry2.UpdatedAt = createdAt
	if err := upsertRatingTx(ctx, tx, entry2); err != nil {
		return FinalizedMatch{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return FinalizedMatch{}, fmt.Errorf("commit finalize tx: %w", err)
	}

	return FinalizedMatch{
		Match:        match,
		Season:       season,
		Player1Old:   old1,
		Player1New:   new1,
		Player2Old:   old2,
		Player2New:   new2,
		Player1ID:    params.Player1ID,
		Player2ID:    params.Player2ID,
		Player1Score: score1,
		Player2Score: score2,
	}, nil
}

func (r *SQLRepositories) GetMatchReplay(ctx context.Context, matchID string) (MatchReplay, error) {
	if r.pool == nil {
		return MatchReplay{}, fmt.Errorf("nil db pool")
	}
	if matchID == "" {
		return MatchReplay{}, fmt.Errorf("match id is required")
	}

	row := r.pool.QueryRow(ctx,
		`SELECT seed_text, initial_state_json, created_at
		 FROM match_replays
		 WHERE match_id = $1`,
		matchID,
	)
	var seedText string
	var initialStateJSON []byte
	var createdAt time.Time
	if err := row.Scan(&seedText, &initialStateJSON, &createdAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MatchReplay{}, ErrNotFound
		}
		return MatchReplay{}, fmt.Errorf("get match replay: %w", err)
	}

	seed, err := strconv.ParseUint(seedText, 10, 64)
	if err != nil {
		return MatchReplay{}, fmt.Errorf("parse replay seed: %w", err)
	}
	var initialState combat.MatchState
	if err := json.Unmarshal(initialStateJSON, &initialState); err != nil {
		return MatchReplay{}, fmt.Errorf("decode replay initial state: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT turn, relative_ms, inputs_json, state_hash_text, roll_hash_text, created_at
		 FROM match_replay_turns
		 WHERE match_id = $1
		 ORDER BY turn ASC`,
		matchID,
	)
	if err != nil {
		return MatchReplay{}, fmt.Errorf("query replay turns: %w", err)
	}
	defer rows.Close()

	turns := make([]MatchReplayTurn, 0, 32)
	for rows.Next() {
		var turn MatchReplayTurn
		var inputsJSON []byte
		var stateHashText string
		var rollHashText string
		if err := rows.Scan(
			&turn.Turn,
			&turn.RelativeMS,
			&inputsJSON,
			&stateHashText,
			&rollHashText,
			&turn.CreatedAt,
		); err != nil {
			return MatchReplay{}, fmt.Errorf("scan replay turn: %w", err)
		}
		if err := json.Unmarshal(inputsJSON, &turn.Inputs); err != nil {
			return MatchReplay{}, fmt.Errorf("decode replay turn inputs: %w", err)
		}
		turn.Checksums.StateHash, err = strconv.ParseUint(stateHashText, 10, 64)
		if err != nil {
			return MatchReplay{}, fmt.Errorf("parse replay state hash: %w", err)
		}
		turn.Checksums.RollHash, err = strconv.ParseUint(rollHashText, 10, 64)
		if err != nil {
			return MatchReplay{}, fmt.Errorf("parse replay roll hash: %w", err)
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return MatchReplay{}, fmt.Errorf("iterate replay turns: %w", err)
	}

	return MatchReplay{
		MatchID:      matchID,
		Seed:         seed,
		InitialState: initialState,
		Turns:        turns,
		CreatedAt:    createdAt,
	}, nil
}

func (r *SQLRepositories) GetOrCreateForActiveSeason(ctx context.Context, playerID string, now time.Time) (RatingEntry, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RatingEntry{}, fmt.Errorf("begin get/create rating tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	season, err := r.ensureActiveSeasonTx(ctx, tx, now.UTC())
	if err != nil {
		return RatingEntry{}, err
	}
	entry, err := r.getOrCreateRatingTx(ctx, tx, playerID, season, now.UTC())
	if err != nil {
		return RatingEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RatingEntry{}, fmt.Errorf("commit get/create rating tx: %w", err)
	}
	return entry, nil
}

func (r *SQLRepositories) Upsert(ctx context.Context, entry RatingEntry) error {
	if r.pool == nil {
		return fmt.Errorf("nil db pool")
	}
	return upsertRatingExec(ctx, r.pool, entry)
}

func (r *SQLRepositories) ensureActiveSeasonTx(ctx context.Context, tx pgx.Tx, nowUTC time.Time) (Season, error) {
	season, err := getActiveSeasonForUpdate(ctx, tx)
	if err == nil {
		if nowUTC.Before(season.EndsAt) {
			return season, nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE seasons
			 SET status = 'closed', updated_at = $2
			 WHERE id = $1`,
			season.ID, nowUTC,
		); err != nil {
			return Season{}, fmt.Errorf("close expired season: %w", err)
		}
		return createNextSeasonTx(ctx, tx, season.Number+1, season.EndsAt.UTC(), r.seasonDuration, nowUTC)
	}
	if !errors.Is(err, ErrNotFound) {
		return Season{}, err
	}
	return createNextSeasonTx(ctx, tx, 1, nowUTC, r.seasonDuration, nowUTC)
}

func (r *SQLRepositories) getOrCreateRatingTx(ctx context.Context, tx pgx.Tx, playerID string, season Season, nowUTC time.Time) (RatingEntry, error) {
	entry, err := getRatingBySeasonTx(ctx, tx, playerID, season.ID)
	if err == nil {
		return entry, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return RatingEntry{}, err
	}

	prev, err := getLatestRatingBeforeSeasonTx(ctx, tx, playerID, season.Number)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return RatingEntry{}, err
	}

	base := ranking.DefaultConfig().DefaultRating
	if err == nil {
		base = ranking.ApplySeasonSoftReset(ranking.Rating{R: prev.Rating, RD: prev.RD, Sigma: prev.Sigma})
	}

	entry = RatingEntry{
		PlayerID:  playerID,
		SeasonID:  season.ID,
		Rating:    base.R,
		RD:        base.RD,
		Sigma:     base.Sigma,
		UpdatedAt: nowUTC,
	}
	if err := upsertRatingTx(ctx, tx, entry); err != nil {
		return RatingEntry{}, err
	}
	return entry, nil
}

func getActiveSeasonFromQuerier(ctx context.Context, q queryRower) (Season, error) {
	row := q.QueryRow(ctx,
		`SELECT id, number, starts_at, ends_at, status, created_at, updated_at
		 FROM seasons
		 WHERE status = 'active'
		 ORDER BY number DESC
		 LIMIT 1`,
	)
	var s Season
	if err := row.Scan(&s.ID, &s.Number, &s.StartsAt, &s.EndsAt, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Season{}, ErrNotFound
		}
		return Season{}, fmt.Errorf("get active season: %w", err)
	}
	return s, nil
}

func getActiveSeasonForUpdate(ctx context.Context, tx pgx.Tx) (Season, error) {
	row := tx.QueryRow(ctx,
		`SELECT id, number, starts_at, ends_at, status, created_at, updated_at
		 FROM seasons
		 WHERE status = 'active'
		 ORDER BY number DESC
		 LIMIT 1
		 FOR UPDATE`,
	)
	var s Season
	if err := row.Scan(&s.ID, &s.Number, &s.StartsAt, &s.EndsAt, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Season{}, ErrNotFound
		}
		return Season{}, fmt.Errorf("get active season for update: %w", err)
	}
	return s, nil
}

func createNextSeasonTx(ctx context.Context, tx pgx.Tx, number int, startsAt time.Time, duration time.Duration, nowUTC time.Time) (Season, error) {
	s := Season{
		ID:        uuid.NewString(),
		Number:    number,
		StartsAt:  startsAt,
		EndsAt:    startsAt.Add(duration),
		Status:    SeasonStatusActive,
		CreatedAt: nowUTC,
		UpdatedAt: nowUTC,
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO seasons (id, number, starts_at, ends_at, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		s.ID, s.Number, s.StartsAt, s.EndsAt, s.Status, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return Season{}, fmt.Errorf("create active season: %w", err)
	}
	return s, nil
}

func scoresForResult(params FinalizeMatchParams) (ranking.MatchScore, ranking.MatchScore, error) {
	switch params.ResultType {
	case MatchResultDraw:
		if params.WinnerID != nil {
			return 0, 0, fmt.Errorf("draw result cannot have winner")
		}
		return ranking.ScoreDraw, ranking.ScoreDraw, nil
	case MatchResultKO, MatchResultSubmission, MatchResultAbandon:
		if params.WinnerID == nil {
			return 0, 0, fmt.Errorf("winner is required for result type %s", params.ResultType)
		}
		switch *params.WinnerID {
		case params.Player1ID:
			return ranking.ScoreWin, ranking.ScoreLoss, nil
		case params.Player2ID:
			return ranking.ScoreLoss, ranking.ScoreWin, nil
		default:
			return 0, 0, fmt.Errorf("winner id %s is not one of the players", *params.WinnerID)
		}
	default:
		return 0, 0, fmt.Errorf("unsupported result type %s", params.ResultType)
	}
}

func insertMatchTx(ctx context.Context, tx pgx.Tx, m Match) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO matches (
		   id, season_id, player1_id, player2_id, winner_id,
		   result_type, started_at, ended_at, duration_ms, created_at
		 ) VALUES (
		   $1, $2, $3, $4, $5,
		   $6, $7, $8, $9, $10
		 )`,
		m.ID, m.SeasonID, m.Player1ID, m.Player2ID, m.WinnerID,
		m.ResultType, m.StartedAt, m.EndedAt, m.DurationMS, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert match: %w", err)
	}
	return nil
}

func insertMatchReplayTx(ctx context.Context, tx pgx.Tx, matchID string, replay MatchReplayWrite, createdAt time.Time) error {
	initialStateJSON, err := json.Marshal(replay.InitialState)
	if err != nil {
		return fmt.Errorf("encode replay initial state: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO match_replays (
		   match_id, seed_text, initial_state_json, turn_count, created_at
		 ) VALUES ($1, $2, $3, $4, $5)`,
		matchID, strconv.FormatUint(replay.Seed, 10), initialStateJSON, len(replay.Turns), createdAt,
	)
	if err != nil {
		return fmt.Errorf("insert match replay: %w", err)
	}

	for _, turn := range replay.Turns {
		inputsJSON, err := json.Marshal(turn.Inputs)
		if err != nil {
			return fmt.Errorf("encode replay turn inputs (turn %d): %w", turn.Turn, err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO match_replay_turns (
			   match_id, turn, relative_ms, inputs_json, state_hash_text, roll_hash_text, created_at
			 ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			matchID,
			turn.Turn,
			turn.RelativeMS,
			inputsJSON,
			strconv.FormatUint(turn.Checksums.StateHash, 10),
			strconv.FormatUint(turn.Checksums.RollHash, 10),
			createdAt,
		)
		if err != nil {
			return fmt.Errorf("insert replay turn %d: %w", turn.Turn, err)
		}
	}

	return nil
}

func insertMatchResultTx(
	ctx context.Context,
	tx pgx.Tx,
	matchID, playerID, opponentID string,
	score ranking.MatchScore,
	before ranking.Rating,
	after ranking.Rating,
	createdAt time.Time,
) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO match_results (
		   match_id, player_id, opponent_id, score,
		   rating_before, rd_before, sigma_before,
		   rating_after, rd_after, sigma_after, created_at
		 ) VALUES (
		   $1, $2, $3, $4,
		   $5, $6, $7,
		   $8, $9, $10, $11
		 )`,
		matchID, playerID, opponentID, score,
		before.R, before.RD, before.Sigma,
		after.R, after.RD, after.Sigma, createdAt,
	)
	if err != nil {
		return fmt.Errorf("insert match result for player %s: %w", playerID, err)
	}
	return nil
}

func getRatingBySeasonTx(ctx context.Context, tx pgx.Tx, playerID, seasonID string) (RatingEntry, error) {
	row := tx.QueryRow(ctx,
		`SELECT player_id, season_id, rating, rd, sigma, last_match_at, updated_at
		 FROM player_ratings
		 WHERE player_id = $1 AND season_id = $2`,
		playerID, seasonID,
	)
	var e RatingEntry
	if err := row.Scan(&e.PlayerID, &e.SeasonID, &e.Rating, &e.RD, &e.Sigma, &e.LastMatchAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RatingEntry{}, ErrNotFound
		}
		return RatingEntry{}, fmt.Errorf("get rating by season: %w", err)
	}
	return e, nil
}

func getLatestRatingBeforeSeasonTx(ctx context.Context, tx pgx.Tx, playerID string, seasonNumber int) (RatingEntry, error) {
	row := tx.QueryRow(ctx,
		`SELECT pr.player_id, pr.season_id, pr.rating, pr.rd, pr.sigma, pr.last_match_at, pr.updated_at
		 FROM player_ratings pr
		 JOIN seasons s ON s.id = pr.season_id
		 WHERE pr.player_id = $1 AND s.number < $2
		 ORDER BY s.number DESC
		 LIMIT 1`,
		playerID, seasonNumber,
	)
	var e RatingEntry
	if err := row.Scan(&e.PlayerID, &e.SeasonID, &e.Rating, &e.RD, &e.Sigma, &e.LastMatchAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RatingEntry{}, ErrNotFound
		}
		return RatingEntry{}, fmt.Errorf("get previous season rating: %w", err)
	}
	return e, nil
}

func upsertRatingTx(ctx context.Context, tx pgx.Tx, entry RatingEntry) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO player_ratings (
		   player_id, season_id, rating, rd, sigma, last_match_at, updated_at
		 ) VALUES (
		   $1, $2, $3, $4, $5, $6, $7
		 )
		 ON CONFLICT (player_id, season_id)
		 DO UPDATE SET
		   rating = EXCLUDED.rating,
		   rd = EXCLUDED.rd,
		   sigma = EXCLUDED.sigma,
		   last_match_at = EXCLUDED.last_match_at,
		   updated_at = EXCLUDED.updated_at`,
		entry.PlayerID, entry.SeasonID, entry.Rating, entry.RD, entry.Sigma, entry.LastMatchAt, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert player rating: %w", err)
	}
	return nil
}

func upsertRatingExec(ctx context.Context, execer queryExecer, entry RatingEntry) error {
	_, err := execer.Exec(ctx,
		`INSERT INTO player_ratings (
		   player_id, season_id, rating, rd, sigma, last_match_at, updated_at
		 ) VALUES (
		   $1, $2, $3, $4, $5, $6, $7
		 )
		 ON CONFLICT (player_id, season_id)
		 DO UPDATE SET
		   rating = EXCLUDED.rating,
		   rd = EXCLUDED.rd,
		   sigma = EXCLUDED.sigma,
		   last_match_at = EXCLUDED.last_match_at,
		   updated_at = EXCLUDED.updated_at`,
		entry.PlayerID, entry.SeasonID, entry.Rating, entry.RD, entry.Sigma, entry.LastMatchAt, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert player rating: %w", err)
	}
	return nil
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type queryExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}
