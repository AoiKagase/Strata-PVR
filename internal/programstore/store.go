package programstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
)

const (
	Recording = "recording"
	Recorded  = "recorded"
)

const completionMarker = "_strataFinalizing"

var completionClaimSequence uint64

var (
	ErrProgramNotFound   = errors.New("program not found")
	ErrProgramAborted    = errors.New("program was aborted")
	ErrProgramFinalizing = errors.New("program is finalizing")
)

func CompletionClaimed(program legacy.Program) bool {
	_, claimed := program.Raw[completionMarker]
	return claimed
}

func CompletionClaimToken(program legacy.Program) string {
	value, ok := program.Raw[completionMarker]
	if !ok {
		return ""
	}
	var token string
	if json.Unmarshal(value, &token) != nil {
		return ""
	}
	return token
}

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
	apply := func(document json.RawMessage) (json.RawMessage, error) {
		var current legacy.Program
		if err := json.Unmarshal(document, &current); err != nil {
			return nil, err
		}
		updated, err := update(current)
		if err != nil {
			return nil, err
		}
		return json.Marshal(updated)
	}
	for attempt := 0; ; attempt++ {
		found, err := database.UpdateProgramDocument(ctx, db, collection, programID, apply)
		if err == nil || !isDatabaseBusy(err) || attempt == 4 {
			return found, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		}
	}
}

// SQLite can reject a read transaction's later write with SQLITE_BUSY_SNAPSHOT
// when a different process (for example WUI) commits in between. Retrying the
// complete read-modify-write transaction obtains a fresh snapshot.
func isDatabaseBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func SetAbort(ctx context.Context, databasePath, collection, programID string, abort bool) error {
	return Update(ctx, databasePath, collection, programID, func(program legacy.Program) (legacy.Program, error) {
		if CompletionClaimed(program) {
			return program, ErrProgramFinalizing
		}
		program.Abort = abort
		return program, nil
	})
}

func ClaimCompletion(ctx context.Context, databasePath string, program legacy.Program) (bool, error) {
	claimed, _, err := ClaimCompletionWithToken(ctx, databasePath, program)
	return claimed, err
}

func ClaimCompletionWithToken(ctx context.Context, databasePath string, program legacy.Program) (bool, string, error) {
	claimToken := strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(atomic.AddUint64(&completionClaimSequence, 1), 10)
	claimed, err := UpdateFound(ctx, databasePath, Recording, program.ID, func(current legacy.Program) (legacy.Program, error) {
		if current.Abort {
			return current, ErrProgramAborted
		}
		if CompletionClaimed(current) {
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
		encodedToken, err := json.Marshal(claimToken)
		if err != nil {
			return current, err
		}
		current.Raw[completionMarker] = encodedToken
		return current, nil
	})
	return claimed, claimToken, err
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
