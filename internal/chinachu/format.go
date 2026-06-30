package chinachu

import (
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
				return "n"
			}
			if key == "episode" {
				return "n"
			}
			return token
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

func StripFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "／", "\\", "＼", ":", "：", "*", "＊", "?", "？",
		"\"", "”", "<", "＜", ">", "＞", "|", "｜", "≫", "＞＞",
		"\r\n", " ", "\n", " ", "\r", " ",
	)
	return replacer.Replace(s)
}

func jsDateFormat(t time.Time, layout string) string {
	replacements := []struct{ from, to string }{
		{"yyyy", "2006"},
		{"yy", "06"},
		{"mm", "01"},
		{"m", "1"},
		{"dd", "02"},
		{"d", "2"},
		{"HH", "15"},
		{"H", "15"},
		{"MM", "04"},
		{"M", "4"},
		{"ss", "05"},
		{"s", "5"},
	}
	goLayout := layout
	for _, r := range replacements {
		goLayout = strings.ReplaceAll(goLayout, r.from, r.to)
	}
	return t.Format(goLayout)
}
