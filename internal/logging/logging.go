package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

var now = time.Now

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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	line := formatRecord(record)
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
