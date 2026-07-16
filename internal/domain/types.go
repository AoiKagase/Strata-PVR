package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

type Channel struct {
	Type        string                     `json:"type,omitempty"`
	Channel     string                     `json:"channel,omitempty"`
	Name        string                     `json:"name,omitempty"`
	ID          string                     `json:"id,omitempty"`
	SID         int64                      `json:"sid,omitempty"`
	NID         int64                      `json:"nid,omitempty"`
	N           int                        `json:"n,omitempty"`
	HasLogoData bool                       `json:"hasLogoData,omitempty"`
	Raw         map[string]json.RawMessage `json:"-"`
}

func (c *Channel) UnmarshalJSON(data []byte) error {
	type channelAlias Channel
	var alias channelAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range channelJSONKeys() {
		delete(raw, key)
	}
	*c = Channel(alias)
	c.Raw = raw
	return nil
}

func (c Channel) MarshalJSON() ([]byte, error) {
	type channelAlias Channel
	knownBytes, err := json.Marshal(channelAlias(c))
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(knownBytes, &known); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(c.Raw)+len(known))
	for key, value := range c.Raw {
		if value != nil {
			out[key] = value
		}
	}
	for _, key := range channelJSONKeys() {
		delete(out, key)
	}
	for key, value := range known {
		out[key] = value
	}
	return marshalOrderedObject(out, channelJSONKeys())
}

func channelJSONKeys() []string {
	return []string{
		"type",
		"channel",
		"name",
		"id",
		"sid",
		"nid",
		"n",
		"hasLogoData",
	}
}

type Program struct {
	ID               string                     `json:"id"`
	Category         string                     `json:"category,omitempty"`
	Title            string                     `json:"title,omitempty"`
	FullTitle        string                     `json:"fullTitle,omitempty"`
	SubTitle         string                     `json:"subTitle,omitempty"`
	Detail           string                     `json:"detail,omitempty"`
	Description      string                     `json:"description,omitempty"`
	Extra            json.RawMessage            `json:"extra,omitempty"`
	Start            int64                      `json:"start"`
	End              int64                      `json:"end"`
	Seconds          int64                      `json:"seconds,omitempty"`
	Flags            []string                   `json:"flags,omitempty"`
	Channel          Channel                    `json:"channel"`
	IsManualReserved bool                       `json:"isManualReserved,omitempty"`
	IsSkip           bool                       `json:"isSkip,omitempty"`
	IsConflict       bool                       `json:"isConflict,omitempty"`
	IsDuplicate      bool                       `json:"isDuplicate,omitempty"`
	RecordedFormat   string                     `json:"recordedFormat,omitempty"`
	Recorded         string                     `json:"recorded,omitempty"`
	Abort            bool                       `json:"abort,omitempty"`
	AbortReason      string                     `json:"abortReason,omitempty"`
	OneSeg           bool                       `json:"1seg,omitempty"`
	PID              int                        `json:"pid,omitempty"`
	Raw              map[string]json.RawMessage `json:"-"`
}

func (p *Program) UnmarshalJSON(data []byte) error {
	type programAlias Program
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	secondsRaw := raw["seconds"]
	delete(raw, "seconds")
	knownData, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var alias programAlias
	if err := json.Unmarshal(knownData, &alias); err != nil {
		return err
	}
	if secondsRaw != nil {
		seconds, err := parseProgramSeconds(secondsRaw, alias.Start, alias.End)
		if err != nil {
			return err
		}
		alias.Seconds = seconds
	}
	for _, key := range programJSONKeys() {
		delete(raw, key)
	}
	*p = Program(alias)
	p.Raw = raw
	return nil
}

func parseProgramSeconds(raw json.RawMessage, start, end int64) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("seconds must be a number: %w", err)
	}
	text := number.String()
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil {
		return seconds, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("invalid seconds %q", text)
	}
	if value != math.Trunc(value) && end >= start {
		return (end - start) / 1000, nil
	}
	if value > float64(math.MaxInt64) {
		return 0, fmt.Errorf("seconds %q overflows int64", text)
	}
	return int64(value), nil
}

func (p Program) MarshalJSON() ([]byte, error) {
	type programAlias Program
	knownBytes, err := json.Marshal(programAlias(p))
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(knownBytes, &known); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(p.Raw)+len(known))
	for key, value := range p.Raw {
		if value != nil {
			out[key] = value
		}
	}
	for _, key := range programJSONKeys() {
		delete(out, key)
	}
	for key, value := range known {
		out[key] = value
	}
	return marshalOrderedObject(out, programJSONKeys())
}

func programJSONKeys() []string {
	return []string{
		"id",
		"category",
		"title",
		"fullTitle",
		"subTitle",
		"detail",
		"description",
		"extra",
		"start",
		"end",
		"seconds",
		"flags",
		"channel",
		"isManualReserved",
		"isSkip",
		"isConflict",
		"isDuplicate",
		"recordedFormat",
		"recorded",
		"abort",
		"abortReason",
		"1seg",
		"pid",
	}
}

type ChannelSchedule struct {
	Channel
	Programs []Program `json:"programs"`
}

func (s *ChannelSchedule) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	channelData := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if key != "programs" {
			channelData[key] = value
		}
	}
	channelBytes, err := json.Marshal(channelData)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(channelBytes, &s.Channel); err != nil {
		return err
	}
	if value, ok := raw["programs"]; ok {
		if err := json.Unmarshal(value, &s.Programs); err != nil {
			return err
		}
	}
	return nil
}

func (s ChannelSchedule) MarshalJSON() ([]byte, error) {
	channelBytes, err := json.Marshal(s.Channel)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(channelBytes, &out); err != nil {
		return nil, err
	}
	programsBytes, err := json.Marshal(s.Programs)
	if err != nil {
		return nil, err
	}
	out["programs"] = programsBytes
	return marshalOrderedObject(out, append(channelJSONKeys(), "programs"))
}

func marshalOrderedObject(values map[string]json.RawMessage, order []string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	wrote := false
	writeField := func(key string, value json.RawMessage) {
		if value == nil {
			return
		}
		if wrote {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(key)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		buf.Write(value)
		wrote = true
	}
	seen := make(map[string]bool, len(order))
	for _, key := range order {
		seen[key] = true
		if value, ok := values[key]; ok {
			writeField(key, value)
		}
	}
	extras := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		writeField(key, values[key])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type RangeRule struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type DurationRule struct {
	Min    int64 `json:"min"`
	Max    int64 `json:"max"`
	HasMin bool  `json:"-"`
	HasMax bool  `json:"-"`
}

func (r *DurationRule) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if value, ok := raw["min"]; ok {
		if err := json.Unmarshal(value, &r.Min); err != nil {
			return err
		}
		r.HasMin = true
	}
	if value, ok := raw["max"]; ok {
		if err := json.Unmarshal(value, &r.Max); err != nil {
			return err
		}
		r.HasMax = true
	}
	return nil
}

func (r DurationRule) MarshalJSON() ([]byte, error) {
	type wire struct {
		Min *int64 `json:"min,omitempty"`
		Max *int64 `json:"max,omitempty"`
	}
	out := wire{}
	if r.HasMin || (!r.HasMin && !r.HasMax) {
		out.Min = &r.Min
	}
	if r.HasMax || (!r.HasMin && !r.HasMax) {
		out.Max = &r.Max
	}
	return json.Marshal(out)
}

type Rule struct {
	IsDisabled          bool          `json:"isDisabled,omitempty"`
	SID                 int64         `json:"sid,omitempty"`
	Types               []string      `json:"types,omitempty"`
	Channels            []string      `json:"channels,omitempty"`
	IgnoreChannels      []string      `json:"ignore_channels,omitempty"`
	Category            string        `json:"category,omitempty"`
	Categories          []string      `json:"categories,omitempty"`
	Hour                *RangeRule    `json:"hour,omitempty"`
	Duration            *DurationRule `json:"duration,omitempty"`
	ReserveTitles       []string      `json:"reserve_titles,omitempty"`
	IgnoreTitles        []string      `json:"ignore_titles,omitempty"`
	ReserveDescriptions []string      `json:"reserve_descriptions,omitempty"`
	IgnoreDescriptions  []string      `json:"ignore_descriptions,omitempty"`
	ReserveFlags        []string      `json:"reserve_flags,omitempty"`
	IgnoreFlags         []string      `json:"ignore_flags,omitempty"`
	RecordedFormat      string        `json:"recorded_format,omitempty"`
}

func (r Rule) MarshalJSON() ([]byte, error) {
	type ruleAlias Rule
	knownBytes, err := json.Marshal(ruleAlias(r))
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(knownBytes, &known); err != nil {
		return nil, err
	}
	return marshalOrderedObject(known, []string{
		"sid",
		"types",
		"channels",
		"ignore_channels",
		"category",
		"categories",
		"hour",
		"duration",
		"reserve_titles",
		"ignore_titles",
		"reserve_descriptions",
		"ignore_descriptions",
		"reserve_flags",
		"ignore_flags",
		"recorded_format",
		"isDisabled",
	})
}

func GetProgramByID(id string, schedules []ChannelSchedule, programs []Program) *Program {
	for i := range schedules {
		for j := range schedules[i].Programs {
			if schedules[i].Programs[j].ID == id {
				return &schedules[i].Programs[j]
			}
		}
	}
	for i := range programs {
		if programs[i].ID == id {
			return &programs[i]
		}
	}
	return nil
}
