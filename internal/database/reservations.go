package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type ReservationDocument struct {
	ProgramID string
	Start     int64
	End       int64
	Document  json.RawMessage
}

func ReadReservations(ctx context.Context, db *sql.DB) ([]json.RawMessage, error) {
	rows, err := db.QueryContext(ctx, "SELECT document_json FROM reservations ORDER BY position")
	if err != nil {
		return nil, fmt.Errorf("read reservations: %w", err)
	}
	defer rows.Close()
	documents := []json.RawMessage{}
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("read reservation: %w", err)
		}
		documents = append(documents, json.RawMessage(document))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read reservations: %w", err)
	}
	return documents, nil
}

func ReplaceReservations(ctx context.Context, db *sql.DB, reservations []ReservationDocument) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace reservations: %w", err)
	}
	defer tx.Rollback()
	if err := replaceReservations(ctx, tx, reservations); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace reservations: %w", err)
	}
	return nil
}

func UpsertReservation(ctx context.Context, db *sql.DB, reservation ReservationDocument) error {
	if reservation.ProgramID == "" || !json.Valid(reservation.Document) {
		return fmt.Errorf("upsert reservation: reservation is invalid")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("upsert reservation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE reservations SET start_at = ?, end_at = ?, document_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE program_id = ?`, reservation.Start, reservation.End, string(reservation.Document), reservation.ProgramID)
	if err != nil {
		return fmt.Errorf("upsert reservation: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("upsert reservation: %w", err)
	} else if n == 0 {
		var position, offset int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) + 1 FROM reservations").Scan(&offset); err != nil {
			return fmt.Errorf("upsert reservation: %w", err)
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM reservations WHERE start_at <= ?", reservation.Start).Scan(&position); err != nil {
			return fmt.Errorf("upsert reservation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE reservations SET position = position + ? WHERE position >= ?", offset, position); err != nil {
			return fmt.Errorf("upsert reservation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE reservations SET position = position - ? + 1 WHERE position >= ?", offset, position+offset); err != nil {
			return fmt.Errorf("upsert reservation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO reservations(program_id, position, start_at, end_at, document_json) VALUES (?, ?, ?, ?, ?)`, reservation.ProgramID, position, reservation.Start, reservation.End, string(reservation.Document)); err != nil {
			return fmt.Errorf("upsert reservation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upsert reservation: %w", err)
	}
	return nil
}

func DeleteReservation(ctx context.Context, db *sql.DB, programID string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	defer tx.Rollback()
	var position int
	if err := tx.QueryRowContext(ctx, "SELECT position FROM reservations WHERE program_id = ?", programID).Scan(&position); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM reservations WHERE program_id = ?", programID); err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	var offset int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) + 1 FROM reservations").Scan(&offset); err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE reservations SET position = position + ? WHERE position > ?", offset, position); err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE reservations SET position = position - ? - 1 WHERE position > ?", offset, position+offset); err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("delete reservation: %w", err)
	}
	return true, nil
}

func replaceReservations(ctx context.Context, tx *sql.Tx, reservations []ReservationDocument) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM reservations"); err != nil {
		return fmt.Errorf("replace reservations: %w", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO reservations(program_id, position, start_at, end_at, document_json) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replace reservations: %w", err)
	}
	defer statement.Close()
	for position, reservation := range reservations {
		if reservation.ProgramID == "" || !json.Valid(reservation.Document) {
			return fmt.Errorf("replace reservations: reservation %d is invalid", position)
		}
		if _, err := statement.ExecContext(ctx, reservation.ProgramID, position, reservation.Start, reservation.End, string(reservation.Document)); err != nil {
			return fmt.Errorf("replace reservation %d: %w", position, err)
		}
	}
	return nil
}
