package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func ReadRules(ctx context.Context, db *sql.DB) ([]json.RawMessage, error) {
	rows, err := db.QueryContext(ctx, "SELECT document_json FROM rules ORDER BY position")
	if err != nil {
		return nil, fmt.Errorf("read rules: %w", err)
	}
	defer rows.Close()
	rules := []json.RawMessage{}
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("read rule: %w", err)
		}
		rules = append(rules, json.RawMessage(document))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read rules: %w", err)
	}
	return rules, nil
}

func ReplaceRules(ctx context.Context, db *sql.DB, rules []json.RawMessage) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace rules: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM rules"); err != nil {
		return fmt.Errorf("replace rules: %w", err)
	}
	statement, err := tx.PrepareContext(ctx, "INSERT INTO rules(position, document_json) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("replace rules: %w", err)
	}
	defer statement.Close()
	for position, rule := range rules {
		if len(rule) == 0 || !json.Valid(rule) {
			return fmt.Errorf("replace rules: rule %d is not valid JSON", position)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(rule, &object); err != nil || object == nil {
			return fmt.Errorf("replace rules: rule %d must be a JSON object", position)
		}
		if _, err := statement.ExecContext(ctx, position, string(rule)); err != nil {
			return fmt.Errorf("replace rule %d: %w", position, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace rules: %w", err)
	}
	return nil
}

func AppendRule(ctx context.Context, db *sql.DB, rule json.RawMessage) error {
	if err := validateRule(rule); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO rules(position, document_json) SELECT COALESCE(MAX(position), -1) + 1, ? FROM rules`, string(rule))
	if err != nil {
		return fmt.Errorf("append rule: %w", err)
	}
	return nil
}

func UpdateRule(ctx context.Context, db *sql.DB, position int, rule json.RawMessage) (bool, error) {
	if err := validateRule(rule); err != nil {
		return false, err
	}
	result, err := db.ExecContext(ctx, `UPDATE rules SET document_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE position = ?`, string(rule), position)
	if err != nil {
		return false, fmt.Errorf("update rule: %w", err)
	}
	n, err := result.RowsAffected()
	return n != 0, err
}

func DeleteRule(ctx context.Context, db *sql.DB, position int) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("delete rule: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM rules WHERE position = ?", position)
	if err != nil {
		return false, fmt.Errorf("delete rule: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	var offset int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) + 1 FROM rules").Scan(&offset); err != nil {
		return false, fmt.Errorf("delete rule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE rules SET position = position + ? WHERE position > ?", offset, position); err != nil {
		return false, fmt.Errorf("delete rule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE rules SET position = position - ? - 1 WHERE position > ?", offset, position+offset); err != nil {
		return false, fmt.Errorf("delete rule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("delete rule: %w", err)
	}
	return true, nil
}

func validateRule(rule json.RawMessage) error {
	var object map[string]json.RawMessage
	if len(rule) == 0 || !json.Valid(rule) || json.Unmarshal(rule, &object) != nil || object == nil {
		return fmt.Errorf("rule must be a valid JSON object")
	}
	return nil
}
