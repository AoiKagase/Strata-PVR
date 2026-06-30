package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func ReadJSON(path string, dst any, emptyJSON string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && emptyJSON != "" {
		b = []byte(emptyJSON)
	} else if err != nil {
		return err
	}
	if len(b) == 0 && emptyJSON != "" {
		b = []byte(emptyJSON)
	}
	return json.Unmarshal(b, dst)
}

func WriteJSONAtomic(path string, value any, pretty bool) error {
	var (
		b   []byte
		err error
	)
	if pretty {
		b, err = json.MarshalIndent(value, "", "  ")
	} else {
		b, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, b)
}

func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
