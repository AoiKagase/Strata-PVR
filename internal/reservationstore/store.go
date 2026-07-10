package reservationstore

import (
	"context"
	"encoding/json"
	"sort"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/storage"
)

func Read(ctx context.Context, databasePath, jsonPath string) ([]legacy.Program, error) {
	if databasePath == "" {
		var reservations []legacy.Program
		if err := storage.ReadJSON(jsonPath, &reservations, "[]"); err != nil {
			return nil, err
		}
		return reservations, nil
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	documents, err := database.ReadReservations(ctx, db)
	if err != nil {
		return nil, err
	}
	reservations := make([]legacy.Program, 0, len(documents))
	for _, document := range documents {
		var reservation legacy.Program
		if err := json.Unmarshal(document, &reservation); err != nil {
			return nil, err
		}
		reservations = append(reservations, reservation)
	}
	return reservations, nil
}

func Write(ctx context.Context, databasePath, jsonPath string, reservations []legacy.Program) error {
	if databasePath == "" {
		return storage.WriteJSONAtomic(jsonPath, reservations, false)
	}
	documents, err := Documents(reservations)
	if err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.ReplaceReservations(ctx, db, documents)
}

func Upsert(ctx context.Context, databasePath, jsonPath string, reservation legacy.Program) error {
	if databasePath == "" {
		reservations, err := Read(ctx, databasePath, jsonPath)
		if err != nil {
			return err
		}
		updated := false
		for i := range reservations {
			if reservations[i].ID == reservation.ID {
				reservations[i] = reservation
				updated = true
				break
			}
		}
		if !updated {
			reservations = append(reservations, reservation)
		}
		sort.SliceStable(reservations, func(i, j int) bool { return reservations[i].Start < reservations[j].Start })
		return Write(ctx, databasePath, jsonPath, reservations)
	}
	document, err := json.Marshal(reservation)
	if err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.UpsertReservation(ctx, db, database.ReservationDocument{ProgramID: reservation.ID, Start: reservation.Start, End: reservation.End, Document: document})
}

func Delete(ctx context.Context, databasePath, jsonPath, programID string) (bool, error) {
	if databasePath == "" {
		reservations, err := Read(ctx, databasePath, jsonPath)
		if err != nil {
			return false, err
		}
		for i := range reservations {
			if reservations[i].ID == programID {
				reservations = append(reservations[:i], reservations[i+1:]...)
				return true, Write(ctx, databasePath, jsonPath, reservations)
			}
		}
		return false, nil
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return false, err
	}
	defer release()
	return database.DeleteReservation(ctx, db, programID)
}

func Documents(reservations []legacy.Program) ([]database.ReservationDocument, error) {
	documents := make([]database.ReservationDocument, 0, len(reservations))
	for _, reservation := range reservations {
		document, err := json.Marshal(reservation)
		if err != nil {
			return nil, err
		}
		documents = append(documents, database.ReservationDocument{
			ProgramID: reservation.ID, Start: reservation.Start, End: reservation.End, Document: document,
		})
	}
	return documents, nil
}
