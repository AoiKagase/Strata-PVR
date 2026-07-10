package schedulestore

import (
	"context"
	"encoding/json"
	"fmt"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
)

func Read(ctx context.Context, databasePath string) ([]legacy.ChannelSchedule, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	documents, err := database.ReadSchedule(ctx, db)
	if err != nil {
		return nil, err
	}
	schedule := make([]legacy.ChannelSchedule, 0, len(documents))
	for _, channelDocument := range documents {
		var channel legacy.Channel
		if err := json.Unmarshal(channelDocument.Document, &channel); err != nil {
			return nil, err
		}
		entry := legacy.ChannelSchedule{Channel: channel, Programs: make([]legacy.Program, 0, len(channelDocument.Programs))}
		for _, programDocument := range channelDocument.Programs {
			var program legacy.Program
			if err := json.Unmarshal(programDocument.Document, &program); err != nil {
				return nil, err
			}
			entry.Programs = append(entry.Programs, program)
		}
		schedule = append(schedule, entry)
	}
	return schedule, nil
}

func Write(ctx context.Context, databasePath string, schedule []legacy.ChannelSchedule) error {
	documents, err := Documents(schedule)
	if err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.ReplaceSchedule(ctx, db, documents)
}

func Documents(schedule []legacy.ChannelSchedule) ([]database.ScheduleChannelDocument, error) {
	documents := make([]database.ScheduleChannelDocument, 0, len(schedule))
	for _, entry := range schedule {
		channelDocument, err := json.Marshal(entry.Channel)
		if err != nil {
			return nil, err
		}
		key := entry.ID
		if key == "" {
			key = fmt.Sprintf("%s:%s:%d", entry.Type, entry.Channel.Channel, entry.SID)
		}
		document := database.ScheduleChannelDocument{ChannelKey: key, Document: channelDocument}
		for _, program := range entry.Programs {
			programDocument, err := json.Marshal(program)
			if err != nil {
				return nil, err
			}
			document.Programs = append(document.Programs, database.ScheduleProgramDocument{
				ProgramID: program.ID, Start: program.Start, End: program.End, Document: programDocument,
			})
		}
		documents = append(documents, document)
	}
	return documents, nil
}
