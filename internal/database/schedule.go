package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type ScheduleProgramDocument struct {
	ProgramID string
	Start     int64
	End       int64
	Document  json.RawMessage
}

type ScheduleChannelDocument struct {
	ChannelKey string
	Document   json.RawMessage
	Programs   []ScheduleProgramDocument
}

func ReadSchedule(ctx context.Context, db *sql.DB) ([]ScheduleChannelDocument, error) {
	rows, err := db.QueryContext(ctx, "SELECT channel_key, document_json FROM schedule_channels ORDER BY position")
	if err != nil {
		return nil, fmt.Errorf("read schedule channels: %w", err)
	}
	channels := []ScheduleChannelDocument{}
	for rows.Next() {
		var channel ScheduleChannelDocument
		var document string
		if err := rows.Scan(&channel.ChannelKey, &document); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read schedule channel: %w", err)
		}
		channel.Document = json.RawMessage(document)
		channel.Programs = []ScheduleProgramDocument{}
		channels = append(channels, channel)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read schedule channels: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schedule channels: %w", err)
	}
	programRows, err := db.QueryContext(ctx, `SELECT channel_key, program_id, start_at, end_at, document_json FROM schedule_programs ORDER BY channel_key, position`)
	if err != nil {
		return nil, fmt.Errorf("read schedule programs: %w", err)
	}
	defer programRows.Close()
	byKey := make(map[string]int, len(channels))
	for index := range channels {
		byKey[channels[index].ChannelKey] = index
	}
	for programRows.Next() {
		var key, document string
		var program ScheduleProgramDocument
		if err := programRows.Scan(&key, &program.ProgramID, &program.Start, &program.End, &document); err != nil {
			return nil, fmt.Errorf("read schedule program: %w", err)
		}
		program.Document = json.RawMessage(document)
		index, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("read schedule program: missing channel %q", key)
		}
		channels[index].Programs = append(channels[index].Programs, program)
	}
	if err := programRows.Err(); err != nil {
		return nil, fmt.Errorf("read schedule programs: %w", err)
	}
	return channels, nil
}

func ReplaceSchedule(ctx context.Context, db *sql.DB, channels []ScheduleChannelDocument) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace schedule: %w", err)
	}
	defer tx.Rollback()
	if err := replaceSchedule(ctx, tx, channels); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace schedule: %w", err)
	}
	return nil
}

func replaceSchedule(ctx context.Context, tx *sql.Tx, channels []ScheduleChannelDocument) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM schedule_programs"); err != nil {
		return fmt.Errorf("replace schedule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM schedule_channels"); err != nil {
		return fmt.Errorf("replace schedule: %w", err)
	}
	channelStatement, err := tx.PrepareContext(ctx, `INSERT INTO schedule_channels(channel_key, position, document_json) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replace schedule: %w", err)
	}
	defer channelStatement.Close()
	programStatement, err := tx.PrepareContext(ctx, `INSERT INTO schedule_programs(program_id, channel_key, position, start_at, end_at, document_json) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replace schedule: %w", err)
	}
	defer programStatement.Close()
	for channelPosition, channel := range channels {
		if channel.ChannelKey == "" || !json.Valid(channel.Document) {
			return fmt.Errorf("replace schedule: channel %d is invalid", channelPosition)
		}
		if _, err := channelStatement.ExecContext(ctx, channel.ChannelKey, channelPosition, string(channel.Document)); err != nil {
			return fmt.Errorf("replace schedule channel %d: %w", channelPosition, err)
		}
		for programPosition, program := range channel.Programs {
			if program.ProgramID == "" || !json.Valid(program.Document) {
				return fmt.Errorf("replace schedule: program %d/%d is invalid", channelPosition, programPosition)
			}
			if _, err := programStatement.ExecContext(ctx, program.ProgramID, channel.ChannelKey, programPosition, program.Start, program.End, string(program.Document)); err != nil {
				return fmt.Errorf("replace schedule program %d/%d: %w", channelPosition, programPosition, err)
			}
		}
	}
	return nil
}
