package system

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrProcessAlreadyRunning = errors.New("process already running")

type ProcessLock struct {
	file   *os.File
	unlock func() error
}

func AcquireProcessLock(path string) (*ProcessLock, error) {
	if path == "" {
		return &ProcessLock{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(file)
	if err != nil {
		_ = file.Close()
		if isLockUnavailable(err) {
			return nil, ErrProcessAlreadyRunning
		}
		return nil, err
	}
	return &ProcessLock{file: file, unlock: unlock}, nil
}

func (lock *ProcessLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	var result error
	if lock.unlock != nil {
		result = lock.unlock()
	}
	if err := lock.file.Close(); result == nil {
		result = err
	}
	lock.file = nil
	lock.unlock = nil
	return result
}
