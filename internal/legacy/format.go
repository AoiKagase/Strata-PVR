package legacy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var tokenRE = regexp.MustCompile(`<([^>]+)>`)

func FormatRecordedName(program Program, format string) string {
	name := tokenRE.ReplaceAllStringFunc(format, func(token string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(token, "<"), ">")
		if strings.HasPrefix(key, "date:") {
			if key == "date:" {
				return "undefined"
			}
			return jsDateFormat(time.UnixMilli(program.Start), strings.TrimPrefix(key, "date:"))
		}
		switch {
		case key == "id":
			return program.ID
		case key == "type":
			return program.Channel.Type
		case key == "channel":
			return program.Channel.Channel
		case key == "channel-id":
			return program.Channel.ID
		case key == "channel-sid":
			return strconv.FormatInt(program.Channel.SID, 10)
		case key == "channel-name":
			return StripFilename(program.Channel.Name)
		case key == "tuner":
			return rawTunerName(program)
		case key == "title":
			return StripFilename(program.Title)
		case key == "fulltitle":
			return StripFilename(program.FullTitle)
		case key == "subtitle":
			return StripFilename(program.SubTitle)
		case key == "category":
			return program.Category
		default:
			if strings.HasPrefix(key, "episode:") {
				widthText := strings.TrimPrefix(key, "episode:")
				if !digitsOnly(widthText) {
					return "undefined"
				}
				width, _ := strconv.Atoi(widthText)
				episode, ok := rawEpisode(program)
				if !ok {
					return "n"
				}
				return fmt.Sprintf("%0*d", width, episode)
			}
			if key == "episode" {
				episode, ok := rawEpisode(program)
				if !ok || episode == 0 {
					return "n"
				}
				return strconv.FormatInt(episode, 10)
			}
			return "undefined"
		}
	})
	dir, file := filepath.Split(name)
	ext := filepath.Ext(file)
	base := strings.TrimSuffix(file, ext)
	limit := 255 - len([]byte(ext))
	for len([]byte(base)) > limit && base != "" {
		_, size := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-size]
	}
	return filepath.ToSlash(filepath.Join(dir, base+ext))
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func rawTunerName(program Program) string {
	raw, ok := program.Raw["tuner"]
	if !ok {
		return ""
	}
	var tuner struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tuner); err != nil {
		return ""
	}
	return tuner.Name
}

func rawEpisode(program Program) (int64, bool) {
	raw, ok := program.Raw["episode"]
	if !ok || string(raw) == "null" {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int64(f), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func StripFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "／", "\\", "＼", ":", "：", "*", "＊", "?", "？",
		"\"", "”", "<", "＜", ">", "＞", "|", "｜", "≫", "＞＞",
		"\r\n", " ", "\n", " ", "\r", " ",
	)
	return replacer.Replace(s)
}

func jsDateFormat(t time.Time, layout string) string {
	if strings.HasPrefix(layout, "UTC:") {
		return jsDateFormat(t.UTC(), strings.TrimPrefix(layout, "UTC:"))
	}
	if mask, ok := dateFormatMasks[layout]; ok {
		layout = mask
	}
	if layout == "isoUtcDateTime" {
		return formatDateMask(t.UTC(), `yyyy-mm-dd'T'HH:MM:ss'Z'`)
	}
	return formatDateMask(t, layout)
}

var dateFormatMasks = map[string]string{
	"default":       "ddd mmm dd yyyy HH:MM:ss",
	"shortDate":     "m/d/yy",
	"mediumDate":    "mmm d, yyyy",
	"longDate":      "mmmm d, yyyy",
	"fullDate":      "dddd, mmmm d, yyyy",
	"shortTime":     "h:MM TT",
	"mediumTime":    "h:MM:ss TT",
	"longTime":      "h:MM:ss TT Z",
	"isoDate":       "yyyy-mm-dd",
	"isoTime":       "HH:MM:ss",
	"isoDateTime":   "yyyy-mm-dd'T'HH:MM:ss",
	"expiresHeader": "ddd, dd mmm yyyy HH:MM:ss Z",
}

var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var dayNamesShort = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var monthNames = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
var monthNamesShort = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func formatDateMask(t time.Time, mask string) string {
	var b strings.Builder
	for i := 0; i < len(mask); {
		if mask[i] == '\'' || mask[i] == '"' {
			quote := mask[i]
			i++
			for i < len(mask) && mask[i] != quote {
				b.WriteByte(mask[i])
				i++
			}
			if i < len(mask) {
				i++
			}
			continue
		}
		if token, value, ok := dateFormatToken(t, mask[i:]); ok {
			b.WriteString(value)
			i += len(token)
			continue
		}
		b.WriteByte(mask[i])
		i++
	}
	return b.String()
}

func dateFormatToken(t time.Time, s string) (token, value string, ok bool) {
	hour12 := t.Hour() % 12
	if hour12 == 0 {
		hour12 = 12
	}
	tokens := []struct {
		token string
		value string
	}{
		{"yyyy", fmt.Sprintf("%04d", t.Year())},
		{"yy", fmt.Sprintf("%02d", t.Year()%100)},
		{"mmmm", monthNames[int(t.Month())-1]},
		{"mmm", monthNamesShort[int(t.Month())-1]},
		{"mm", fmt.Sprintf("%02d", int(t.Month()))},
		{"m", strconv.Itoa(int(t.Month()))},
		{"dddd", dayNames[int(t.Weekday())]},
		{"ddd", dayNamesShort[int(t.Weekday())]},
		{"dd", fmt.Sprintf("%02d", t.Day())},
		{"d", strconv.Itoa(t.Day())},
		{"S", ordinalSuffix(t.Day())},
		{"HH", fmt.Sprintf("%02d", t.Hour())},
		{"H", strconv.Itoa(t.Hour())},
		{"hh", fmt.Sprintf("%02d", hour12)},
		{"h", strconv.Itoa(hour12)},
		{"MM", fmt.Sprintf("%02d", t.Minute())},
		{"M", strconv.Itoa(t.Minute())},
		{"ss", fmt.Sprintf("%02d", t.Second())},
		{"s", strconv.Itoa(t.Second())},
		{"l", fmt.Sprintf("%03d", t.Nanosecond()/int(time.Millisecond))},
		{"L", fmt.Sprintf("%02d", (t.Nanosecond()/int(time.Millisecond)+5)/10)},
		{"TT", meridiem(t, true)},
		{"tt", meridiem(t, false)},
		{"T", meridiem(t, true)[:1]},
		{"t", meridiem(t, false)[:1]},
		{"Z", t.Format("MST")},
		{"o", timezoneOffset(t)},
	}
	for _, item := range tokens {
		if strings.HasPrefix(s, item.token) {
			return item.token, item.value, true
		}
	}
	return "", "", false
}

func ordinalSuffix(day int) string {
	if day%100 >= 11 && day%100 <= 13 {
		return "th"
	}
	switch day % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
	}
}

func timezoneOffset(t time.Time) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s%02d%02d", sign, offset/3600, (offset%3600)/60)
}

func meridiem(t time.Time, upper bool) string {
	if t.Hour() < 12 {
		if upper {
			return "AM"
		}
		return "am"
	}
	if upper {
		return "PM"
	}
	return "pm"
}
