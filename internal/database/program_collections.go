package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type ProgramDocument struct {
	ProgramID string
	Start     int64
	End       int64
	Document  json.RawMessage
}

func ReadProgramCollection(ctx context.Context, db *sql.DB, collection string) ([]json.RawMessage, error) {
	if !validProgramCollection(collection) {
		return nil, fmt.Errorf("invalid program collection %q", collection)
	}
	rows, err := db.QueryContext(ctx, `SELECT document_json FROM program_collections WHERE collection = ? ORDER BY position`, collection)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", collection, err)
	}
	defer rows.Close()
	documents := []json.RawMessage{}
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("read %s: %w", collection, err)
		}
		documents = append(documents, json.RawMessage(document))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", collection, err)
	}
	return documents, nil
}

func ReadProgramIDs(ctx context.Context, db *sql.DB, collection string) ([]string, error) {
	if !validProgramCollection(collection) {
		return nil, fmt.Errorf("invalid program collection %q", collection)
	}
	rows, err := db.QueryContext(ctx, `SELECT program_id FROM program_collections WHERE collection = ? ORDER BY position`, collection)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", collection, err)
	}
	defer rows.Close()
	programIDs := []string{}
	for rows.Next() {
		var programID string
		if err := rows.Scan(&programID); err != nil {
			return nil, fmt.Errorf("read %s: %w", collection, err)
		}
		programIDs = append(programIDs, programID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", collection, err)
	}
	return programIDs, nil
}

func ReadProgramByID(ctx context.Context, db *sql.DB, collection, programID string) (json.RawMessage, bool, error) {
	if !validProgramCollection(collection) {
		return nil, false, fmt.Errorf("invalid program collection %q", collection)
	}
	var document string
	if err := db.QueryRowContext(ctx, `SELECT document_json FROM program_collections WHERE collection = ? AND program_id = ?`, collection, programID).Scan(&document); err == sql.ErrNoRows {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("read %s program %q: %w", collection, programID, err)
	}
	return json.RawMessage(document), true, nil
}

func ReplaceProgramCollection(ctx context.Context, db *sql.DB, collection string, programs []ProgramDocument) error {
	if !validProgramCollection(collection) {
		return fmt.Errorf("invalid program collection %q", collection)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace %s: %w", collection, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM program_collections WHERE collection = ?`, collection); err != nil {
		return fmt.Errorf("replace %s: %w", collection, err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO program_collections(collection, program_id, position, start_at, end_at, document_json) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replace %s: %w", collection, err)
	}
	defer statement.Close()
	for position, program := range programs {
		if program.ProgramID == "" || !json.Valid(program.Document) {
			return fmt.Errorf("replace %s: program %d is invalid", collection, position)
		}
		if _, err := statement.ExecContext(ctx, collection, program.ProgramID, position, program.Start, program.End, string(program.Document)); err != nil {
			return fmt.Errorf("replace %s program %d: %w", collection, position, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace %s: %w", collection, err)
	}
	return nil
}

func UpsertProgram(ctx context.Context, db *sql.DB, collection string, program ProgramDocument) error {
	if !validProgramCollection(collection) || program.ProgramID == "" || !json.Valid(program.Document) {
		return fmt.Errorf("upsert %s: invalid program", collection)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO program_collections(collection, program_id, position, start_at, end_at, document_json)
		VALUES (?, ?, COALESCE((SELECT MAX(position) + 1 FROM program_collections WHERE collection = ?), 0), ?, ?, ?)
		ON CONFLICT(collection, program_id) DO UPDATE SET
		start_at=excluded.start_at, end_at=excluded.end_at, document_json=excluded.document_json,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		collection, program.ProgramID, collection, program.Start, program.End, string(program.Document))
	if err != nil {
		return fmt.Errorf("upsert %s program: %w", collection, err)
	}
	return nil
}

// UpdateProgramDocument atomically applies update to the latest JSON document
// for a program. The callback runs inside the transaction so callers can merge
// recorder-owned fields without replacing changes made by another component.
func UpdateProgramDocument(ctx context.Context, db *sql.DB, collection, programID string, update func(json.RawMessage) (json.RawMessage, error)) (bool, error) {
	if !validProgramCollection(collection) || programID == "" || update == nil {
		return false, fmt.Errorf("update %s: invalid program", collection)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("update %s program %q: %w", collection, programID, err)
	}
	defer tx.Rollback()
	var document string
	if err := tx.QueryRowContext(ctx, `SELECT document_json FROM program_collections WHERE collection = ? AND program_id = ?`, collection, programID).Scan(&document); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("update %s program %q: %w", collection, programID, err)
	}
	updated, err := update(json.RawMessage(document))
	if err != nil {
		return false, fmt.Errorf("update %s program %q: %w", collection, programID, err)
	}
	if !json.Valid(updated) {
		return false, fmt.Errorf("update %s program %q: invalid document", collection, programID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE program_collections SET document_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE collection = ? AND program_id = ?`, string(updated), collection, programID); err != nil {
		return false, fmt.Errorf("update %s program %q: %w", collection, programID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("update %s program %q: %w", collection, programID, err)
	}
	return true, nil
}

func DeleteProgram(ctx context.Context, db *sql.DB, collection, programID string) error {
	if !validProgramCollection(collection) || programID == "" {
		return fmt.Errorf("delete %s: invalid program", collection)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM program_collections WHERE collection = ? AND program_id = ?`, collection, programID); err != nil {
		return fmt.Errorf("delete %s program: %w", collection, err)
	}
	return nil
}

func CompleteProgram(ctx context.Context, db *sql.DB, program ProgramDocument) error {
	if program.ProgramID == "" || !json.Valid(program.Document) {
		return fmt.Errorf("complete program: invalid program")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete program: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM program_collections WHERE collection = 'recording' AND program_id = ?`, program.ProgramID); err != nil {
		return fmt.Errorf("complete program: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM program_collections WHERE collection = 'recorded' AND program_id = ?`, program.ProgramID); err != nil {
		return fmt.Errorf("complete program: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO program_collections(collection, program_id, position, start_at, end_at, document_json)
		VALUES ('recorded', ?, COALESCE((SELECT MAX(position) + 1 FROM program_collections WHERE collection = 'recorded'), 0), ?, ?, ?)`,
		program.ProgramID, program.Start, program.End, string(program.Document)); err != nil {
		return fmt.Errorf("complete program: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("complete program: %w", err)
	}
	return nil
}

func validProgramCollection(collection string) bool {
	return collection == "recording" || collection == "recorded"
}
