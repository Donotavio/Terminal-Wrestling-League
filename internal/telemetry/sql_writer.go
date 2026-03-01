package telemetry

import (
	"context"
	"fmt"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
)

type sqlWriterStore interface {
	CreateSessionTelemetryEvent(ctx context.Context, event storage.SessionTelemetryEvent) (storage.SessionTelemetryEvent, error)
	CreateQueueTelemetryEvent(ctx context.Context, event storage.QueueTelemetryEvent) (storage.QueueTelemetryEvent, error)
	CreateSpectatorTelemetryEvent(ctx context.Context, event storage.SpectatorTelemetryEvent) (storage.SpectatorTelemetryEvent, error)
	CreateNavigationTelemetryEvent(ctx context.Context, event storage.NavigationTelemetryEvent) (storage.NavigationTelemetryEvent, error)
	InsertTurnTelemetryBatch(ctx context.Context, rows []storage.MatchTurnTelemetry) error
	InsertMatchSummaryTelemetry(ctx context.Context, summary storage.MatchSummaryTelemetry) (storage.MatchSummaryTelemetry, error)
	CreateAntiBotFlag(ctx context.Context, flag storage.AntiBotFlag) (storage.AntiBotFlag, error)
}

// SQLWriter persists telemetry events and aggregates in PostgreSQL.
type SQLWriter struct {
	store sqlWriterStore
}

func NewSQLWriter(store sqlWriterStore) *SQLWriter {
	return &SQLWriter{store: store}
}

func (w *SQLWriter) RecordSessionEvent(ctx context.Context, event storage.SessionTelemetryEvent) error {
	if w == nil || w.store == nil {
		return nil
	}
	if _, err := w.store.CreateSessionTelemetryEvent(ctx, event); err != nil {
		return fmt.Errorf("record session telemetry event: %w", err)
	}
	return nil
}

func (w *SQLWriter) RecordQueueEvent(ctx context.Context, event storage.QueueTelemetryEvent) error {
	if w == nil || w.store == nil {
		return nil
	}
	if _, err := w.store.CreateQueueTelemetryEvent(ctx, event); err != nil {
		return fmt.Errorf("record queue telemetry event: %w", err)
	}
	return nil
}

func (w *SQLWriter) RecordSpectatorEvent(ctx context.Context, event storage.SpectatorTelemetryEvent) error {
	if w == nil || w.store == nil {
		return nil
	}
	if _, err := w.store.CreateSpectatorTelemetryEvent(ctx, event); err != nil {
		return fmt.Errorf("record spectator telemetry event: %w", err)
	}
	return nil
}

func (w *SQLWriter) RecordNavigationEvent(ctx context.Context, event storage.NavigationTelemetryEvent) error {
	if w == nil || w.store == nil {
		return nil
	}
	if _, err := w.store.CreateNavigationTelemetryEvent(ctx, event); err != nil {
		return fmt.Errorf("record navigation telemetry event: %w", err)
	}
	return nil
}

func (w *SQLWriter) PersistMatchTurnBatch(ctx context.Context, rows []storage.MatchTurnTelemetry) error {
	if w == nil || w.store == nil {
		return nil
	}
	if err := w.store.InsertTurnTelemetryBatch(ctx, rows); err != nil {
		return fmt.Errorf("persist match turn telemetry: %w", err)
	}
	return nil
}

func (w *SQLWriter) PersistMatchSummary(ctx context.Context, summary storage.MatchSummaryTelemetry) error {
	if w == nil || w.store == nil {
		return nil
	}
	if _, err := w.store.InsertMatchSummaryTelemetry(ctx, summary); err != nil {
		return fmt.Errorf("persist match summary telemetry: %w", err)
	}
	return nil
}

func (w *SQLWriter) RecordAntiBotFlag(ctx context.Context, flag storage.AntiBotFlag) error {
	if w == nil || w.store == nil {
		return nil
	}
	if _, err := w.store.CreateAntiBotFlag(ctx, flag); err != nil {
		return fmt.Errorf("record anti bot flag: %w", err)
	}
	return nil
}
