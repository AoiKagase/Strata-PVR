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

const completionMarker = "_strataFinalizing"

var (
	ErrProgramNotFound   = errors.New("program not found")
	ErrProgramAborted    = errors.New("program was aborted")
	ErrProgramFinalizing = errors.New("program is finalizing")
)

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
	_, err := UpdateFound(ctx, databasePath, collection, programID, update)
	return err
}

func UpdateFound(ctx context.Context, databasePath, collection, programID string, update func(legacy.Program) (legacy.Program, error)) (bool, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return false, err
	}
	defer release()
	return database.UpdateProgramDocument(ctx, db, collection, programID, func(document json.RawMessage) (json.RawMessage, error) {
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
}

func SetAbort(ctx context.Context, databasePath, collection, programID string, abort bool) error {
	return Update(ctx, databasePath, collection, programID, func(program legacy.Program) (legacy.Program, error) {
		if _, finalizing := program.Raw[completionMarker]; finalizing {
			return program, ErrProgramFinalizing
		}
		program.Abort = abort
		return program, nil
	})
}

func ClaimCompletion(ctx context.Context, databasePath string, program legacy.Program) (bool, error) {
	return UpdateFound(ctx, databasePath, Recording, program.ID, func(current legacy.Program) (legacy.Program, error) {
		if current.Abort {
			return current, ErrProgramAborted
		}
		if _, finalizing := current.Raw[completionMarker]; finalizing {
			return current, ErrProgramFinalizing
		}
		current.Recorded = program.Recorded
		current.PID = 0
		if current.Raw == nil {
			current.Raw = make(map[string]json.RawMessage)
		}
		for _, key := range []string{"priority", "tuner", "command"} {
			if value, ok := program.Raw[key]; ok {
				current.Raw[key] = value
			}
		}
		current.Raw[completionMarker] = json.RawMessage(`true`)
		return current, nil
	})
}

func ClearCompletionClaim(ctx context.Context, databasePath, programID string) error {
	return Update(ctx, databasePath, Recording, programID, func(program legacy.Program) (legacy.Program, error) {
		delete(program.Raw, completionMarker)
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
	delete(current.Raw, completionMarker)
	return json.Marshal(current)
}

func encodeProgram(program legacy.Program) (database.ProgramDocument, error) {
	document, err := json.Marshal(program)
	if err != nil {
		return database.ProgramDocument{}, err
	}
	return database.ProgramDocument{ProgramID: program.ID, Start: program.Start, End: program.End, Document: document}, nil
}
