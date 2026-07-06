package chinachu

import "encoding/json"

type Channel struct {
	Type        string `json:"type,omitempty"`
	Channel     string `json:"channel,omitempty"`
	Name        string `json:"name,omitempty"`
	ID          string `json:"id,omitempty"`
	SID         int64  `json:"sid,omitempty"`
	NID         int64  `json:"nid,omitempty"`
	N           int    `json:"n,omitempty"`
	HasLogoData bool   `json:"hasLogoData,omitempty"`
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
	OneSeg           bool                       `json:"1seg,omitempty"`
	PID              int                        `json:"pid,omitempty"`
	Raw              map[string]json.RawMessage `json:"-"`
}

type ChannelSchedule struct {
	Channel
	Programs []Program `json:"programs"`
}

type RangeRule struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type DurationRule struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
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
