package programstore

import (
	"context"
	"encoding/json"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
)

const (
	Recording = "recording"
	Recorded  = "recorded"
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
	return database.CompleteProgram(ctx, db, document)
}

func encodeProgram(program legacy.Program) (database.ProgramDocument, error) {
	document, err := json.Marshal(program)
	if err != nil {
		return database.ProgramDocument{}, err
	}
	return database.ProgramDocument{ProgramID: program.ID, Start: program.Start, End: program.End, Document: document}, nil
}
