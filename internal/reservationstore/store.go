package reservationstore

import (
	"context"
	"encoding/json"

	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
)

func Read(ctx context.Context, databasePath string) ([]legacy.Program, error) {
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

func ReadDue(ctx context.Context, databasePath string, startBefore, endAfter int64) ([]legacy.Program, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	documents, err := database.ReadReservationsDue(ctx, db, startBefore, endAfter)
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

func Write(ctx context.Context, databasePath string, reservations []legacy.Program) error {
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

func Upsert(ctx context.Context, databasePath string, reservation legacy.Program) error {
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

func Delete(ctx context.Context, databasePath, programID string) (bool, error) {
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
