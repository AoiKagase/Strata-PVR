package rulestore

import (
	"context"
	"encoding/json"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
)

func Read(ctx context.Context, databasePath string) ([]legacy.Rule, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	documents, err := database.ReadRules(ctx, db)
	if err != nil {
		return nil, err
	}
	rules := make([]legacy.Rule, 0, len(documents))
	for _, document := range documents {
		var rule legacy.Rule
		if err := json.Unmarshal(document, &rule); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func ReadRaw(ctx context.Context, databasePath string) ([]map[string]json.RawMessage, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	defer release()
	documents, err := database.ReadRules(ctx, db)
	if err != nil {
		return nil, err
	}
	rules := make([]map[string]json.RawMessage, 0, len(documents))
	for _, document := range documents {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(document, &rule); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func Write(ctx context.Context, databasePath string, rules []legacy.Rule) error {
	documents := make([]json.RawMessage, 0, len(rules))
	for _, rule := range rules {
		document, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		documents = append(documents, document)
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.ReplaceRules(ctx, db, documents)
}

func WriteRaw(ctx context.Context, databasePath string, rules []map[string]json.RawMessage) error {
	documents := make([]json.RawMessage, 0, len(rules))
	for _, rule := range rules {
		document, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		documents = append(documents, document)
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.ReplaceRules(ctx, db, documents)
}

func Append(ctx context.Context, databasePath string, rule any) error {
	document, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return err
	}
	defer release()
	return database.AppendRule(ctx, db, document)
}

func Update(ctx context.Context, databasePath string, position int, rule any) (bool, error) {
	document, err := json.Marshal(rule)
	if err != nil {
		return false, err
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return false, err
	}
	defer release()
	return database.UpdateRule(ctx, db, position, document)
}

func Delete(ctx context.Context, databasePath string, position int) (bool, error) {
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return false, err
	}
	defer release()
	return database.DeleteRule(ctx, db, position)
}
