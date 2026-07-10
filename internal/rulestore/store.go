package rulestore

import (
	"context"
	"encoding/json"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/storage"
)

func Read(ctx context.Context, databasePath, jsonPath string) ([]legacy.Rule, error) {
	if databasePath == "" {
		var rules []legacy.Rule
		if err := storage.ReadJSON(jsonPath, &rules, "[]"); err != nil {
			return nil, err
		}
		return rules, nil
	}
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

func ReadRaw(ctx context.Context, databasePath, jsonPath string) ([]map[string]json.RawMessage, error) {
	if databasePath == "" {
		var rules []map[string]json.RawMessage
		if err := storage.ReadJSON(jsonPath, &rules, "[]"); err != nil {
			return nil, err
		}
		return rules, nil
	}
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

func Write(ctx context.Context, databasePath, jsonPath string, rules []legacy.Rule) error {
	if databasePath == "" {
		return storage.WriteJSONAtomic(jsonPath, rules, true)
	}
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

func WriteRaw(ctx context.Context, databasePath, jsonPath string, rules []map[string]json.RawMessage) error {
	if databasePath == "" {
		return storage.WriteJSONAtomic(jsonPath, rules, true)
	}
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

func Append(ctx context.Context, databasePath, jsonPath string, rule any) error {
	if databasePath == "" {
		rules, err := ReadRaw(ctx, databasePath, jsonPath)
		if err != nil {
			return err
		}
		document, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(document, &raw); err != nil {
			return err
		}
		return WriteRaw(ctx, databasePath, jsonPath, append(rules, raw))
	}
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

func Update(ctx context.Context, databasePath, jsonPath string, position int, rule any) (bool, error) {
	if databasePath == "" {
		rules, err := ReadRaw(ctx, databasePath, jsonPath)
		if err != nil {
			return false, err
		}
		if position < 0 || position >= len(rules) {
			return false, nil
		}
		document, err := json.Marshal(rule)
		if err != nil {
			return false, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(document, &raw); err != nil {
			return false, err
		}
		rules[position] = raw
		return true, WriteRaw(ctx, databasePath, jsonPath, rules)
	}
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

func Delete(ctx context.Context, databasePath, jsonPath string, position int) (bool, error) {
	if databasePath == "" {
		rules, err := ReadRaw(ctx, databasePath, jsonPath)
		if err != nil {
			return false, err
		}
		if position < 0 || position >= len(rules) {
			return false, nil
		}
		rules = append(rules[:position], rules[position+1:]...)
		return true, WriteRaw(ctx, databasePath, jsonPath, rules)
	}
	db, release, err := database.Acquire(ctx, databasePath)
	if err != nil {
		return false, err
	}
	defer release()
	return database.DeleteRule(ctx, db, position)
}
