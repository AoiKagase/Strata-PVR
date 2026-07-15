package wui

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	legacy "strata-pvr/internal/domain"
)

type programSearchQuery struct {
	Query       string
	Title       string
	Description string
	Category    string
	Type        string
	ProgramID   string
	ChannelID   string
	StartHour   *int
	EndHour     *int
}

type programSearchPage struct {
	Items      []legacy.Program `json:"items"`
	Total      int              `json:"total"`
	Categories []string         `json:"categories"`
	Channels   []legacy.Channel `json:"channels"`
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	query, err := parseProgramSearchQuery(r)
	if err != nil {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	now := time.Now()
	programs := searchPrograms(schedules, query, now)
	limit, offset, paged, err := parseSearchPagination(r)
	if err != nil {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	if !paged {
		writePrettyJSON(w, http.StatusOK, programs)
		return
	}
	start := min(offset, len(programs))
	end := min(start+limit, len(programs))
	categories, channels := searchFacets(schedules, now)
	writeCompactJSON(w, http.StatusOK, programSearchPage{
		Items: programs[start:end], Total: len(programs), Categories: categories, Channels: channels,
	})
}

func parseProgramSearchQuery(r *http.Request) (programSearchQuery, error) {
	values := r.URL.Query()
	query := programSearchQuery{
		Query:       values.Get("query"),
		Title:       values.Get("title"),
		Description: firstQueryValue(values, "description", "desc"),
		Category:    firstQueryValue(values, "category", "cat"),
		Type:        values.Get("type"),
		ProgramID:   firstQueryValue(values, "programID", "pgid"),
		ChannelID:   firstQueryValue(values, "channelID", "chid"),
	}
	var err error
	if query.StartHour, err = parseSearchHour(values, "startHour", "start", 0, 23); err != nil {
		return programSearchQuery{}, err
	}
	if query.EndHour, err = parseSearchHour(values, "endHour", "end", 1, 24); err != nil {
		return programSearchQuery{}, err
	}
	return query, nil
}

func parseSearchPagination(r *http.Request) (limit int, offset int, paged bool, err error) {
	values := r.URL.Query()
	limitValue := values.Get("limit")
	if limitValue == "" {
		return 0, 0, false, nil
	}
	limit, err = strconv.Atoi(limitValue)
	if err != nil || limit < 1 || limit > 200 {
		return 0, 0, false, strconv.ErrSyntax
	}
	offsetValue := values.Get("offset")
	if offsetValue == "" {
		return limit, 0, true, nil
	}
	offset, err = strconv.Atoi(offsetValue)
	if err != nil || offset < 0 {
		return 0, 0, false, strconv.ErrSyntax
	}
	return limit, offset, true, nil
}

func firstQueryValue(values url.Values, names ...string) string {
	for _, name := range names {
		if value := values.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func parseSearchHour(values url.Values, names string, alias string, min int, max int) (*int, error) {
	value := firstQueryValue(values, names, alias)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return nil, strconv.ErrSyntax
	}
	return &n, nil
}

func searchPrograms(schedules []legacy.ChannelSchedule, query programSearchQuery, now time.Time) []legacy.Program {
	nowMS := now.UnixMilli()
	programs := make([]legacy.Program, 0)
	for _, schedule := range schedules {
		for _, program := range schedule.Programs {
			if program.End < nowMS || !matchesProgramSearch(program, schedule.Channel, query) {
				continue
			}
			if program.Channel.ID == "" {
				program.Channel = schedule.Channel
			}
			programs = append(programs, program)
		}
	}
	sort.SliceStable(programs, func(i, j int) bool {
		if programs[i].Start == programs[j].Start {
			return programs[i].ID < programs[j].ID
		}
		return programs[i].Start < programs[j].Start
	})
	return programs
}

func matchesProgramSearch(program legacy.Program, channel legacy.Channel, query programSearchQuery) bool {
	if query.ProgramID != "" && program.ID != query.ProgramID {
		return false
	}
	programChannel := program.Channel
	if programChannel.ID == "" {
		programChannel = channel
	}
	if query.ChannelID != "" && programChannel.ID != query.ChannelID {
		return false
	}
	if query.Type != "" && programChannel.Type != query.Type {
		return false
	}
	if query.Category != "" && program.Category != query.Category {
		return false
	}
	if query.Query != "" && !searchContains(strings.Join([]string{
		program.ID, programTitle(program), programDescription(program), program.Category, programChannel.Name,
	}, " "), query.Query) {
		return false
	}
	if query.Title != "" && !searchContains(programTitle(program), query.Title) {
		return false
	}
	if query.Description != "" && !searchContains(programDescription(program), query.Description) {
		return false
	}
	return matchesProgramSearchHours(program, query.StartHour, query.EndHour)
}

func searchFacets(schedules []legacy.ChannelSchedule, now time.Time) ([]string, []legacy.Channel) {
	nowMS := now.UnixMilli()
	categories := map[string]struct{}{}
	channels := make([]legacy.Channel, 0, len(schedules))
	for _, schedule := range schedules {
		hasFutureProgram := false
		for _, program := range schedule.Programs {
			if program.End < nowMS {
				continue
			}
			hasFutureProgram = true
			if program.Category != "" {
				categories[program.Category] = struct{}{}
			}
		}
		if hasFutureProgram {
			channels = append(channels, schedule.Channel)
		}
	}
	categoryList := make([]string, 0, len(categories))
	for category := range categories {
		categoryList = append(categoryList, category)
	}
	sort.Strings(categoryList)
	sort.SliceStable(channels, func(i, j int) bool {
		if channels[i].N == channels[j].N {
			return channels[i].ID < channels[j].ID
		}
		return channels[i].N < channels[j].N
	})
	return categoryList, channels
}

func programTitle(program legacy.Program) string {
	if program.FullTitle != "" {
		return program.FullTitle
	}
	return program.Title
}

func programDescription(program legacy.Program) string {
	if program.Detail != "" {
		return program.Detail
	}
	return program.Description
}

func searchContains(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func matchesProgramSearchHours(program legacy.Program, startHour, endHour *int) bool {
	if startHour == nil && endHour == nil {
		return true
	}
	ruleStart, ruleEnd := 0, 24
	if startHour != nil {
		ruleStart = *startHour
	}
	if endHour != nil {
		ruleEnd = *endHour
	}
	start := time.UnixMilli(program.Start).In(time.Local).Hour()
	end := time.UnixMilli(program.End).In(time.Local).Hour()
	if start > end {
		end += 24
	}
	if ruleStart > ruleEnd {
		return !((ruleStart > start) && (ruleEnd < end))
	}
	return !(ruleStart > start || ruleEnd < end)
}
