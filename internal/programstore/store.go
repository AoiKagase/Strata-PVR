package programstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
)

const (
	Recording = "recording"
	Recorded  = "recorded"
)

var ErrProgramNotFound = errors.New("program not found")

func Read(ctx context.Context, databasePath, collection string) ([]legacy.Program, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	documents, err := database.ReadProgramCollection(ctx, db, collection)
	if err != nil {
		return nil, err
	}
	programs := make([]legacy.Program, 0, len(documents))
	for _, document := range documents {
		var program legacy.Program
		if err := json.Unmarshal(document, &program); err != nil {
			return nil, err
		}
		programs = append(programs, program)
	}
	return programs, nil
}

func ReadIDs(ctx context.Context, databasePath, collection string) ([]string, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	return database.ReadProgramIDs(ctx, db, collection)
}

func ReadByID(ctx context.Context, databasePath, collection, programID string) (legacy.Program, bool, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return legacy.Program{}, false, err
	}
	defer release()
	document, found, err := database.ReadProgramByID(ctx, db, collection, programID)
	if err != nil || !found {
		return legacy.Program{}, found, err
	}
	var program legacy.Program
	if err := json.Unmarshal(document, &program); err != nil {
		return legacy.Program{}, false, err
	}
	return program, true, nil
}

func Write(ctx context.Context, databasePath, collection string, programs []legacy.Program) error {
	documents := make([]database.ProgramDocument, 0, len(programs))
	for _, program := range programs {
		document, err := json.Marshal(program)
		if err != nil {
			return err
		}
		documents = append(documents, database.ProgramDocument{ProgramID: program.ID, Start: program.Start, End: program.End, Document: document})
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.ReplaceProgramCollection(ctx, db, collection, documents)
}

func Upsert(ctx context.Context, databasePath, collection string, program legacy.Program) error {
	document, err := encodeProgram(program)
	if err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.UpsertProgram(ctx, db, collection, document)
}

func Update(ctx context.Context, databasePath, collection, programID string, update func(legacy.Program) (legacy.Program, error)) error {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	_, err = database.UpdateProgramDocument(ctx, db, collection, programID, func(document json.RawMessage) (json.RawMessage, error) {
		var current legacy.Program
		if err := json.Unmarshal(document, &current); err != nil {
			return nil, err
		}
		updated, err := update(current)
		if err != nil {
			return nil, err
		}
		return json.Marshal(updated)
	})
	return err
}

func SetAbort(ctx context.Context, databasePath, collection, programID string, abort bool) error {
	return Update(ctx, databasePath, collection, programID, func(program legacy.Program) (legacy.Program, error) {
		program.Abort = abort
		return program, nil
	})
}

func Remove(ctx context.Context, databasePath, collection, programID string) error {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.DeleteProgram(ctx, db, collection, programID)
}

func Complete(ctx context.Context, databasePath string, program legacy.Program) error {
	document, err := encodeProgram(program)
	if err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	found, err := database.CompleteProgramFromRecording(ctx, db, document, mergeCompletedProgramDocuments)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s: %w", ErrProgramNotFound, program.ID, sql.ErrNoRows)
	}
	return nil
}

func mergeCompletedProgramDocuments(currentDocument, completedDocument json.RawMessage) (json.RawMessage, error) {
	var current, completed legacy.Program
	if err := json.Unmarshal(currentDocument, &current); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(completedDocument, &completed); err != nil {
		return nil, err
	}
	current.Recorded = completed.Recorded
	current.PID = completed.PID
	if len(completed.Raw) > 0 {
		if current.Raw == nil {
			current.Raw = make(map[string]json.RawMessage)
		}
		for _, key := range []string{"priority", "tuner", "command"} {
			if value, ok := completed.Raw[key]; ok {
				current.Raw[key] = value
			}
		}
	}
	return json.Marshal(current)
}

func encodeProgram(program legacy.Program) (database.ProgramDocument, error) {
	document, err := json.Marshal(program)
	if err != nil {
		return database.ProgramDocument{}, err
	}
	return database.ProgramDocument{ProgramID: program.ID, Start: program.Start, End: program.End, Document: document}, nil
}
