package storage

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProfileAndTutorialRunIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	player, err := repos.Create(ctx, "m5_profile_player")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}

	profile, err := repos.GetOrCreateProfile(ctx, player.ID)
	if err != nil {
		t.Fatalf("get or create profile: %v", err)
	}
	if profile.TutorialCompleted {
		t.Fatalf("tutorial completed = true, want false")
	}

	started := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	ended := started.Add(45 * time.Second)
	run, err := repos.CreateTutorialRun(ctx, TutorialRun{
		PlayerID:  player.ID,
		Result:    TutorialResultLoss,
		StartedAt: started,
		EndedAt:   ended,
	})
	if err != nil {
		t.Fatalf("create tutorial run: %v", err)
	}
	if run.ID == "" {
		t.Fatalf("tutorial run id is empty")
	}
	if run.DurationMS != 45000 {
		t.Fatalf("duration_ms = %d, want 45000", run.DurationMS)
	}

	profile, err = repos.MarkTutorialCompleted(ctx, player.ID, ended)
	if err != nil {
		t.Fatalf("mark tutorial completed: %v", err)
	}
	if !profile.TutorialCompleted {
		t.Fatalf("tutorial completed = false, want true")
	}
	if profile.TutorialCompletedAt == nil {
		t.Fatalf("tutorial_completed_at is nil")
	}
	if profile.TutorialRuns != 1 {
		t.Fatalf("tutorial_runs = %d, want 1", profile.TutorialRuns)
	}
}

func TestGetOrCreateProfileConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	playerEntity, err := repos.Create(ctx, "m5_profile_concurrent")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}

	const workers = 16
	start := make(chan struct{})
	errCh := make(chan error, workers)
	var wg sync.WaitGroup

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := repos.GetOrCreateProfile(ctx, playerEntity.ID)
			errCh <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("get or create profile in concurrent call: %v", err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM player_profiles WHERE player_id = $1`, playerEntity.ID).Scan(&count); err != nil {
		t.Fatalf("count player_profiles: %v", err)
	}
	if count != 1 {
		t.Fatalf("player_profiles count = %d, want 1", count)
	}
}

func TestAntiBotConfigAndFlagIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	cfg, err := repos.LoadAntiBotConfig(ctx)
	if err != nil {
		t.Fatalf("load anti bot config: %v", err)
	}
	if cfg.MinDecisions != 12 {
		t.Fatalf("min decisions = %d, want 12", cfg.MinDecisions)
	}

	p1, err := repos.Create(ctx, "m5_antibot_p1")
	if err != nil {
		t.Fatalf("create player1: %v", err)
	}
	p2, err := repos.Create(ctx, "m5_antibot_p2")
	if err != nil {
		t.Fatalf("create player2: %v", err)
	}

	start := time.Date(2026, 3, 3, 13, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	winner := p1.ID
	finalized, err := repos.FinalizeMatch(ctx, FinalizeMatchParams{
		Player1ID:  p1.ID,
		Player2ID:  p2.ID,
		WinnerID:   &winner,
		ResultType: MatchResultKO,
		StartedAt:  start,
		EndedAt:    end,
	})
	if err != nil {
		t.Fatalf("finalize match: %v", err)
	}

	flag, err := repos.CreateAntiBotFlag(ctx, AntiBotFlag{
		PlayerID:            p1.ID,
		SeasonID:            finalized.Season.ID,
		MatchID:             finalized.Match.ID,
		DecisionCount:       16,
		MeanDecisionMS:      150,
		DecisionVarianceMS2: 1200,
		OptimalPickRate:     0.9,
		SuspicionScore:      0.8,
		Flagged:             true,
		Reason:              "threshold exceeded",
	})
	if err != nil {
		t.Fatalf("create anti bot flag: %v", err)
	}
	if flag.ID == "" {
		t.Fatalf("anti bot flag id is empty")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM anti_bot_flags`).Scan(&count); err != nil {
		t.Fatalf("count anti_bot_flags: %v", err)
	}
	if count != 1 {
		t.Fatalf("anti_bot_flags count = %d, want 1", count)
	}
}

func TestTelemetryEventsAndSummariesIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newIsolatedPool(t, ctx)
	defer cleanup()

	if err := ApplyMigrations(ctx, pool, migrationDir(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repos := NewSQLRepositories(pool, nil)
	p1, err := repos.Create(ctx, "m5_telemetry_p1")
	if err != nil {
		t.Fatalf("create player1: %v", err)
	}
	p2, err := repos.Create(ctx, "m5_telemetry_p2")
	if err != nil {
		t.Fatalf("create player2: %v", err)
	}

	start := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Second)
	winner := p1.ID
	finalized, err := repos.FinalizeMatch(ctx, FinalizeMatchParams{
		Player1ID:  p1.ID,
		Player2ID:  p2.ID,
		WinnerID:   &winner,
		ResultType: MatchResultKO,
		StartedAt:  start,
		EndedAt:    end,
	})
	if err != nil {
		t.Fatalf("finalize match: %v", err)
	}

	err = repos.InsertTurnTelemetryBatch(ctx, []MatchTurnTelemetry{
		{
			MatchID:           finalized.Match.ID,
			Turn:              1,
			PlayerID:          p1.ID,
			IsHuman:           true,
			Action:            "Strike",
			TargetZone:        "Head",
			DecisionMS:        140,
			Success:           true,
			HPBefore:          100,
			HPAfter:           100,
			StaminaBefore:     100,
			StaminaAfter:      92,
			MomentumBefore:    0,
			MomentumAfter:     8,
			IsOptimalChoice:   true,
			OptimalAction:     "Strike",
			OptimalTargetZone: "Head",
		},
		{
			MatchID:           finalized.Match.ID,
			Turn:              1,
			PlayerID:          p2.ID,
			IsHuman:           true,
			Action:            "Block",
			TargetZone:        "Torso",
			DecisionMS:        220,
			Success:           false,
			HPBefore:          100,
			HPAfter:           88,
			StaminaBefore:     100,
			StaminaAfter:      95,
			MomentumBefore:    0,
			MomentumAfter:     0,
			IsOptimalChoice:   false,
			OptimalAction:     "Dodge",
			OptimalTargetZone: "Legs",
		},
	})
	if err != nil {
		t.Fatalf("insert turn telemetry batch: %v", err)
	}

	_, err = repos.InsertMatchSummaryTelemetry(ctx, MatchSummaryTelemetry{
		MatchID:            finalized.Match.ID,
		SeasonID:           finalized.Season.ID,
		Player1ID:          p1.ID,
		Player2ID:          p2.ID,
		WinnerID:           &winner,
		ResultType:         string(MatchResultKO),
		TurnCount:          1,
		DurationMS:         90000,
		AvgDecisionMSP1:    140,
		AvgDecisionMSP2:    220,
		AvgStaminaP1:       92,
		AvgStaminaP2:       95,
		AvgMomentumP1:      8,
		AvgMomentumP2:      0,
		MaxComboP1:         1,
		MaxComboP2:         0,
		StunEvents:         0,
		SubmissionAttempts: 0,
		QueueWaitMSP1:      1500,
		QueueWaitMSP2:      1200,
	})
	if err != nil {
		t.Fatalf("insert match summary telemetry: %v", err)
	}

	if _, err := repos.CreateSessionTelemetryEvent(ctx, SessionTelemetryEvent{
		PlayerID:       &p1.ID,
		Handle:         "m5_telemetry_p1",
		RemoteAddrHash: "abc123",
		EventType:      "login_success",
		Detail:         map[string]any{"phase": "m5"},
	}); err != nil {
		t.Fatalf("create session telemetry event: %v", err)
	}
	if _, err := repos.CreateQueueTelemetryEvent(ctx, QueueTelemetryEvent{
		PlayerID:    p1.ID,
		EventType:   "join",
		QueueWaitMS: 0,
	}); err != nil {
		t.Fatalf("create queue telemetry event: %v", err)
	}
	if _, err := repos.CreateSpectatorTelemetryEvent(ctx, SpectatorTelemetryEvent{
		SpectatorPlayerID: p1.ID,
		TargetPlayerID:    p2.ID,
		MatchID:           &finalized.Match.ID,
		EventType:         "watch_attached",
		WaitMS:            500,
		Detail:            map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("create spectator telemetry event: %v", err)
	}
	sessionID := "11111111-1111-1111-1111-111111111111"
	optionKey := "1"
	if _, err := repos.CreateNavigationTelemetryEvent(ctx, NavigationTelemetryEvent{
		PlayerID:  p1.ID,
		SessionID: &sessionID,
		State:     NavigationStateLobby,
		EventType: "menu_selected",
		Source:    NavigationSourceMenu,
		OptionKey: &optionKey,
		Detail:    map[string]any{"resolved_command": "play"},
	}); err != nil {
		t.Fatalf("create navigation telemetry event: %v", err)
	}

	assertTableCount(t, pool, "telemetry_match_turns", 2)
	assertTableCount(t, pool, "telemetry_match_summaries", 1)
	assertTableCount(t, pool, "telemetry_session_events", 1)
	assertTableCount(t, pool, "telemetry_queue_events", 1)
	assertTableCount(t, pool, "telemetry_spectator_events", 1)
	assertTableCount(t, pool, "telemetry_navigation_events", 1)
}
