package database

import (
	"context"
	"database/sql"
	"fmt"
)

func ReplaceSchedulerState(ctx context.Context, db *sql.DB, schedule []ScheduleChannelDocument, reservations []ReservationDocument) error {
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace scheduler state: %w", err)
	}
	return nil
}
