package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func ReplaceSchedulerState(ctx context.Context, db *sql.DB, schedule []ScheduleChannelDocument, reservations []ReservationDocument) error {
	return ReplaceSchedulerStateWithRecordingUpdates(ctx, db, schedule, reservations, nil)
}

// RecordingTimingUpdate changes an active recording's schedule-owned timing
// fields while preserving fields owned by the recorder and other components.
type RecordingTimingUpdate struct {
	ProgramID string
	Start     int64
	End       int64
	Update    func(json.RawMessage) (json.RawMessage, error)
}

// ReplaceSchedulerStateWithRecordingUpdates atomically publishes a new guide,
// reservations, and timing updates for active recordings.
func ReplaceSchedulerStateWithRecordingUpdates(ctx context.Context, db *sql.DB, schedule []ScheduleChannelDocument, reservations []ReservationDocument, recordings []RecordingTimingUpdate) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace scheduler state: %w", err)
	}
	defer tx.Rollback()
	if err := replaceSchedule(ctx, tx, schedule); err != nil {
		return fmt.Errorf("replace scheduler state: %w", err)
	}
	if err := replaceReservations(ctx, tx, reservations); err != nil {
		return fmt.Errorf("replace scheduler state: %w", err)
	}
	for _, recording := range recordings {
		if err := updateRecordingTiming(ctx, tx, recording); err != nil {
			return fmt.Errorf("replace scheduler state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace scheduler state: %w", err)
	}
	return nil
}

func updateRecordingTiming(ctx context.Context, tx *sql.Tx, update RecordingTimingUpdate) error {
	if update.ProgramID == "" || update.Update == nil {
		return fmt.Errorf("update recording timing: invalid program")
	}
	var document string
	err := tx.QueryRowContext(ctx, `SELECT document_json FROM program_collections WHERE collection = 'recording' AND program_id = ?`, update.ProgramID).Scan(&document)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("update recording timing %q: %w", update.ProgramID, err)
	}
	updated, err := update.Update(json.RawMessage(document))
	if err != nil || !json.Valid(updated) {
		if err == nil {
			err = fmt.Errorf("invalid document")
		}
		return fmt.Errorf("update recording timing %q: %w", update.ProgramID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE program_collections SET start_at = ?, end_at = ?, document_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE collection = 'recording' AND program_id = ?`, update.Start, update.End, string(updated), update.ProgramID); err != nil {
		return fmt.Errorf("update recording timing %q: %w", update.ProgramID, err)
	}
	return nil
}
