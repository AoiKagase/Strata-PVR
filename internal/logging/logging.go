package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Level is the severity of a log record.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Field is a key/value pair in a structured log record.
type Field struct {
	Key   string
	Value any
}

// Record is the in-memory representation of the custom line format.
type Record struct {
	Time   time.Time
	Level  Level
	Event  string
	Fields []Field
}

// RotationConfig controls size-based log rotation. MaxBytes <= 0 disables
// rotation. When rotation is enabled, MaxFiles is the number of rotated
// copies to retain (path.1 is the newest copy).
type RotationConfig struct {
	MaxBytes int64
	MaxFiles int
}

const (
	DefaultRotationMaxBytes = 10 * 1024 * 1024
	DefaultRotationMaxFiles = 5
)

var now = time.Now

var (
	rotationMu     sync.RWMutex
	rotationConfig = RotationConfig{
		MaxBytes: DefaultRotationMaxBytes,
		MaxFiles: DefaultRotationMaxFiles,
	}
	pathLocks sync.Map // map[string]*sync.Mutex
)

// SetRotationConfig changes the process-wide log rotation policy. A zero
// MaxBytes value disables rotation. It is intended for applications and
// tests that need a policy other than the defaults.
func SetRotationConfig(config RotationConfig) error {
	if config.MaxBytes < 0 {
		return fmt.Errorf("log rotation max bytes must not be negative")
	}
	if config.MaxBytes > 0 && config.MaxFiles < 1 {
		return fmt.Errorf("log rotation max files must be at least 1 when rotation is enabled")
	}
	rotationMu.Lock()
	rotationConfig = config
	rotationMu.Unlock()
	return nil
}

func currentRotationConfig() RotationConfig {
	rotationMu.RLock()
	defer rotationMu.RUnlock()
	return rotationConfig
}

// AppendEvent writes one structured record. The record is deliberately kept
// line-oriented so the WUI can continue to stream log files efficiently.
// Values are quoted when necessary and escaped using Go string escapes, which
// keeps tabs, newlines, quotes, and separators inside one physical line.
func AppendEvent(path string, level Level, event string, fields ...Field) error {
	return appendRecord(path, Record{
		Time:   now(),
		Level:  level,
		Event:  event,
		Fields: fields,
	})
}

func Info(path, event string, fields ...Field) error {
	return AppendEvent(path, LevelInfo, event, fields...)
}

func Warn(path, event string, fields ...Field) error {
	return AppendEvent(path, LevelWarn, event, fields...)
}

func Error(path, event string, fields ...Field) error {
	return AppendEvent(path, LevelError, event, fields...)
}

// AppendLine is kept as the compatibility entry point used by existing
// callers. Its original message is preserved as a field, while a stable event
// name is inferred for the common "EVENT: message" lines.
func AppendLine(path, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	return AppendEvent(path, legacyLevel(message), legacyEvent(message), Field{
		Key:   "message",
		Value: message,
	})
}

func appendRecord(path string, record Record) error {
	if path == "" {
		return nil
	}

	line := formatRecord(record)
	lock := pathLock(path)
	lock.Lock()
	defer lock.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := rotateIfNeeded(path, int64(len(line)), currentRotationConfig()); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func pathLock(path string) *sync.Mutex {
	lock := &sync.Mutex{}
	actual, _ := pathLocks.LoadOrStore(path, lock)
	return actual.(*sync.Mutex)
}

func rotateIfNeeded(path string, incomingBytes int64, config RotationConfig) error {
	if config.MaxBytes <= 0 || config.MaxFiles < 1 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() == 0 || (info.Size() <= config.MaxBytes && incomingBytes <= config.MaxBytes-info.Size()) {
		return nil
	}

	for index := config.MaxFiles - 1; index >= 1; index-- {
		if err := renameReplacing(rotationPath(path, index), rotationPath(path, index+1)); err != nil {
			return err
		}
	}
	return renameReplacing(path, rotationPath(path, 1))
}

func rotationPath(path string, index int) string {
	return path + "." + strconv.Itoa(index)
}

func renameReplacing(source, destination string) error {
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}

func formatRecord(record Record) string {
	level := record.Level
	if level == "" {
		level = LevelInfo
	}
	event := record.Event
	if event == "" {
		event = "message"
	}

	var b strings.Builder
	// time.Now returns the OS local time. Keep that location so installations
	// in JST show JST while other installations follow their OS timezone.
	b.WriteString(record.Time.Format(time.RFC3339Nano))
	b.WriteString("|level=")
	b.WriteString(escapeToken(string(level)))
	b.WriteString("|event=")
	b.WriteString(escapeToken(event))
	for _, field := range record.Fields {
		if !validKey(field.Key) {
			continue
		}
		b.WriteByte('|')
		b.WriteString(field.Key)
		b.WriteByte('=')
		b.WriteString(formatFieldValue(field))
	}
	b.WriteByte('\n')
	return b.String()
}

func formatValue(value any) string {
	switch value := value.(type) {
	case string:
		return escapeQuotedValue(value)
	case error:
		return escapeQuotedValue(value.Error())
	default:
		return escapeToken(fmt.Sprint(value))
	}
}

func formatFieldValue(field Field) string {
	// Keep the compatibility message human-readable and searchable in the WUI.
	// Other values use quoted encoding so delimiters cannot corrupt a record.
	if field.Key == "message" {
		if value, ok := field.Value.(string); ok {
			return escapeToken(value)
		}
	}
	return formatValue(field.Value)
}

func escapeQuotedValue(value string) string {
	return strings.ReplaceAll(strconv.Quote(value), "|", "\\u007c")
}

func escapeToken(value string) string {
	return strings.NewReplacer("|", "\\u007c", "\n", "\\n", "\r", "\\r", "\t", "\\t").Replace(value)
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

func legacyLevel(message string) Level {
	lower := strings.ToLower(strings.TrimSpace(message))
	if strings.HasPrefix(lower, "error") || strings.HasPrefix(lower, "#ffmpeg") {
		return LevelError
	}
	if strings.HasPrefix(lower, "**warning**") || strings.HasPrefix(lower, "!conflict") || strings.HasPrefix(lower, "alert:") {
		return LevelWarn
	}
	return LevelInfo
}

func legacyEvent(message string) string {
	trimmed := strings.TrimSpace(message)
	if i := strings.IndexByte(trimmed, ':'); i > 0 && i <= 48 {
		if event := normalizeEvent(trimmed[:i]); event != "" {
			return event
		}
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "**warning**") {
		return "warning"
	}
	return "message"
}

func normalizeEvent(value string) string {
	var b strings.Builder
	lastDot := true
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDot = false
			continue
		}
		if !lastDot {
			b.WriteByte('.')
			lastDot = true
		}
	}
	return strings.Trim(b.String(), ".")
}
