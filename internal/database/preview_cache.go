package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type PreviewCacheEntry struct {
	CacheKey    string
	ProgramID   string
	SourcePath  string
	SourceSize  int64
	SourceMTime int64
	FileName    string
	AccessedAt  string
}

func ListPreviewCacheEntries(ctx context.Context, db *sql.DB) ([]PreviewCacheEntry, error) {
	rows, err := db.QueryContext(ctx, `SELECT cache_key, file_name, accessed_at FROM preview_cache ORDER BY accessed_at ASC, cache_key ASC`)
	if err != nil {
		return nil, fmt.Errorf("list preview cache entries: %w", err)
	}
	defer rows.Close()
	entries := []PreviewCacheEntry{}
	for rows.Next() {
		var entry PreviewCacheEntry
		if err := rows.Scan(&entry.CacheKey, &entry.FileName, &entry.AccessedAt); err != nil {
			return nil, fmt.Errorf("scan preview cache entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preview cache entries: %w", err)
	}
	return entries, nil
}

func DeletePreviewCacheEntries(ctx context.Context, db *sql.DB, cacheKeys []string) error {
	if len(cacheKeys) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete preview cache entries: %w", err)
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `DELETE FROM preview_cache WHERE cache_key = ?`)
	if err != nil {
		return fmt.Errorf("delete preview cache entries: %w", err)
	}
	defer statement.Close()
	for _, cacheKey := range cacheKeys {
		if _, err := statement.ExecContext(ctx, cacheKey); err != nil {
			return fmt.Errorf("delete preview cache entry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete preview cache entries: %w", err)
	}
	return nil
}

func ClearPreviewCache(ctx context.Context, db *sql.DB) ([]string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("clear preview cache: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT file_name FROM preview_cache`)
	if err != nil {
		return nil, fmt.Errorf("list preview cache files to clear: %w", err)
	}
	files := []string{}
	for rows.Next() {
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan preview cache file to clear: %w", err)
		}
		files = append(files, fileName)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate preview cache files to clear: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close preview cache files to clear: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM preview_cache`); err != nil {
		return nil, fmt.Errorf("clear preview cache entries: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("clear preview cache: %w", err)
	}
	return files, nil
}

func FindPreviewCache(ctx context.Context, db *sql.DB, cacheKey string) (PreviewCacheEntry, bool, error) {
	var entry PreviewCacheEntry
	err := db.QueryRowContext(ctx, `SELECT cache_key, program_id, source_path, source_size, source_mtime, file_name FROM preview_cache WHERE cache_key = ?`, cacheKey).Scan(
		&entry.CacheKey, &entry.ProgramID, &entry.SourcePath, &entry.SourceSize, &entry.SourceMTime, &entry.FileName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PreviewCacheEntry{}, false, nil
	}
	if err != nil {
		return PreviewCacheEntry{}, false, fmt.Errorf("find preview cache: %w", err)
	}
	_, _ = db.ExecContext(ctx, `UPDATE preview_cache SET accessed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE cache_key = ?`, cacheKey)
	return entry, true, nil
}

func StorePreviewCache(ctx context.Context, db *sql.DB, entry PreviewCacheEntry) (string, error) {
	var previous string
	_ = db.QueryRowContext(ctx, `SELECT file_name FROM preview_cache WHERE cache_key = ?`, entry.CacheKey).Scan(&previous)
	_, err := db.ExecContext(ctx, `INSERT INTO preview_cache(cache_key, program_id, source_path, source_size, source_mtime, file_name)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
		program_id=excluded.program_id, source_path=excluded.source_path, source_size=excluded.source_size,
		source_mtime=excluded.source_mtime, file_name=excluded.file_name,
		created_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), accessed_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		entry.CacheKey, entry.ProgramID, entry.SourcePath, entry.SourceSize, entry.SourceMTime, entry.FileName)
	if err != nil {
		return "", fmt.Errorf("store preview cache: %w", err)
	}
	return previous, nil
}

func DeletePreviewCache(ctx context.Context, db *sql.DB, cacheKey string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM preview_cache WHERE cache_key = ?`, cacheKey)
	return err
}

func ListPreviewCacheFiles(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT file_name FROM preview_cache`)
	if err != nil {
		return nil, fmt.Errorf("list preview cache files: %w", err)
	}
	defer rows.Close()
	files := make(map[string]struct{})
	for rows.Next() {
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			return nil, fmt.Errorf("scan preview cache file: %w", err)
		}
		files[fileName] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preview cache files: %w", err)
	}
	return files, nil
}

func RemoveMissingPreviewCacheFiles(ctx context.Context, db *sql.DB, existing map[string]struct{}) (int64, error) {
	files, err := ListPreviewCacheFiles(ctx, db)
	if err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("remove missing preview cache files: %w", err)
	}
	defer tx.Rollback()
	var removed int64
	for fileName := range files {
		if _, ok := existing[fileName]; ok {
			continue
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM preview_cache WHERE file_name = ?`, fileName)
		if err != nil {
			return 0, fmt.Errorf("remove missing preview cache file: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("remove missing preview cache file: %w", err)
		}
		removed += count
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("remove missing preview cache files: %w", err)
	}
	return removed, nil
}

func RemovePreviewCacheForProgram(ctx context.Context, db *sql.DB, programID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT file_name FROM preview_cache WHERE program_id = ?`, programID)
	if err != nil {
		return nil, err
	}
	files := []string{}
	for rows.Next() {
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			rows.Close()
			return nil, err
		}
		files = append(files, fileName)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM preview_cache WHERE program_id = ?`, programID); err != nil {
		return nil, err
	}
	return files, nil
}
