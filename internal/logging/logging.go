package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func AppendLine(path, format string, args ...any) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	message := fmt.Sprintf(format, args...)
	_, err = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), message)
	return err
}
