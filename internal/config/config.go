package config

import (
	"encoding/json"
	"fmt"
	"os"
)

const StrataSchema = "strata/config"

type Document struct {
	Schema       string               `json:"schema"`
	Version      int                  `json:"version"`
	Mirakurun    MirakurunSettings    `json:"mirakurun"`
	Recording    RecordingSettings    `json:"recording"`
	Web          WebSettings          `json:"web"`
	PreviewCache PreviewCacheSettings `json:"previewCache,omitempty"`
	Services     ServiceSettings      `json:"services"`
	Advanced     AdvancedSettings     `json:"advanced,omitempty"`
}

type PreviewCacheSettings struct {
	MaxAgeDays int `json:"maxAgeDays,omitempty"`
	MaxSizeMB  int `json:"maxSizeMB,omitempty"`
}

type MirakurunSettings struct {
	URL                string `json:"url"`
	RecordingPriority  int    `json:"recordingPriority"`
	ConflictedPriority int    `json:"conflictedPriority"`
}

type RecordingSettings struct {
	Directory      string           `json:"directory"`
	FilenameFormat string           `json:"filenameFormat"`
	LowSpace       LowSpaceSettings `json:"lowSpace"`
}

type LowSpaceSettings struct {
	ThresholdMB int    `json:"thresholdMB"`
	Action      string `json:"action"`
}

type WebSettings struct {
	ListenAddress  string                 `json:"listenAddress"`
	Port           int                    `json:"port"`
	Authentication AuthenticationSettings `json:"authentication"`
}

type AuthenticationSettings struct {
	Enabled bool      `json:"enabled"`
	Users   []WebUser `json:"users,omitempty"`
}

type WebUser struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
}

type ServiceSettings struct {
	Excluded []int64 `json:"excluded,omitempty"`
	Order    []int64 `json:"order,omitempty"`
}

type AdvancedSettings struct {
	NormalizationForm string `json:"normalizationForm,omitempty"`
}

type Config struct {
	Strata                     bool                       `json:"-"`
	MirakurunPath              string                     `json:"mirakurunPath"`
	SchedulerMirakurunPath     string                     `json:"schedulerMirakurunPath"`
	RecordedDir                string                     `json:"recordedDir"`
	ExcludeServices            []int64                    `json:"excludeServices"`
	ServiceOrder               []int64                    `json:"serviceOrder"`
	WUIUsers                   []string                   `json:"wuiUsers"`
	WUIAccounts                []WebUser                  `json:"-"`
	WUIAuthenticationEnabled   bool                       `json:"-"`
	WUIPort                    *int                       `json:"wuiPort"`
	WUIHost                    string                     `json:"wuiHost"`
	WUIOpenServer              bool                       `json:"wuiOpenServer"`
	WUIOpenHost                string                     `json:"wuiOpenHost"`
	WUIOpenPort                int                        `json:"wuiOpenPort"`
	NormalizationForm          string                     `json:"normalizationForm"`
	RecordedFormat             string                     `json:"recordedFormat"`
	RecordingPriority          int                        `json:"recordingPriority"`
	ConflictedPriority         int                        `json:"conflictedPriority"`
	StorageLowSpaceThresholdMB int                        `json:"storageLowSpaceThresholdMB"`
	StorageLowSpaceAction      string                     `json:"storageLowSpaceAction"`
	PreviewCacheMaxAgeDays     int                        `json:"-"`
	PreviewCacheMaxSizeMB      int                        `json:"-"`
	Raw                        map[string]json.RawMessage `json:"-"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	var marker struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(b, &marker); err != nil {
		return nil, err
	}
	if marker.Schema != "" {
		return loadDocument(b)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	cfg.Raw = raw
	if cfg.WUIPort != nil {
		cfg.WUIAuthenticationEnabled = len(cfg.WUIUsers) > 0
	} else if cfg.WUIOpenServer {
		port := cfg.WUIOpenPort
		if port == 0 {
			port = 20772
		}
		cfg.WUIHost = cfg.WUIOpenHost
		cfg.WUIPort = &port
	}
	return cfg, nil
}

func ParseDocument(b []byte) (Document, error) {
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return Document{}, err
	}
	if _, err := loadDocument(b); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func loadDocument(b []byte) (*Config, error) {
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Schema != StrataSchema {
		return nil, fmt.Errorf("unsupported config schema %q", doc.Schema)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("unsupported %s version %d", StrataSchema, doc.Version)
	}
	if doc.Mirakurun.URL == "" {
		return nil, fmt.Errorf("mirakurun.url is required")
	}
	if doc.Web.Port < 1 || doc.Web.Port > 65535 {
		return nil, fmt.Errorf("web.port must be between 1 and 65535")
	}
	if doc.PreviewCache.MaxAgeDays < 0 || doc.PreviewCache.MaxSizeMB < 0 {
		return nil, fmt.Errorf("previewCache limits must not be negative")
	}
	if doc.Recording.LowSpace.Action != "remove" && doc.Recording.LowSpace.Action != "stop" {
		return nil, fmt.Errorf("recording.lowSpace.action must be remove or stop")
	}
	cfg := defaultConfig()
	cfg.Strata = true
	cfg.MirakurunPath = doc.Mirakurun.URL
	cfg.RecordingPriority = doc.Mirakurun.RecordingPriority
	cfg.ConflictedPriority = doc.Mirakurun.ConflictedPriority
	cfg.RecordedDir = doc.Recording.Directory
	cfg.RecordedFormat = doc.Recording.FilenameFormat
	cfg.StorageLowSpaceThresholdMB = doc.Recording.LowSpace.ThresholdMB
	cfg.StorageLowSpaceAction = doc.Recording.LowSpace.Action
	cfg.WUIHost = doc.Web.ListenAddress
	cfg.WUIPort = &doc.Web.Port
	cfg.WUIAuthenticationEnabled = doc.Web.Authentication.Enabled
	if doc.Web.Authentication.Enabled {
		if len(doc.Web.Authentication.Users) == 0 {
			return nil, fmt.Errorf("web.authentication.users requires at least one user when authentication is enabled")
		}
		for _, user := range doc.Web.Authentication.Users {
			if user.Username == "" || user.PasswordHash == "" {
				return nil, fmt.Errorf("web authentication users require username and passwordHash")
			}
		}
		cfg.WUIAccounts = doc.Web.Authentication.Users
	}
	cfg.ExcludeServices = doc.Services.Excluded
	cfg.ServiceOrder = doc.Services.Order
	cfg.NormalizationForm = doc.Advanced.NormalizationForm
	cfg.PreviewCacheMaxAgeDays = doc.PreviewCache.MaxAgeDays
	cfg.PreviewCacheMaxSizeMB = doc.PreviewCache.MaxSizeMB
	return cfg, nil
}

func DefaultDocument() Document {
	return Document{
		Schema:  StrataSchema,
		Version: 1,
		Mirakurun: MirakurunSettings{
			URL:                "http://127.0.0.1:40772",
			RecordingPriority:  2,
			ConflictedPriority: 1,
		},
		Recording: RecordingSettings{
			Directory:      "./recorded/",
			FilenameFormat: "[<date:yymmdd-HHMM>][<type><channel>][<channel-name>]<title>.m2ts",
			LowSpace:       LowSpaceSettings{ThresholdMB: 3000, Action: "remove"},
		},
		Web:          WebSettings{ListenAddress: "0.0.0.0", Port: 20772},
		PreviewCache: PreviewCacheSettings{MaxAgeDays: 30, MaxSizeMB: 1024},
	}
}

func defaultConfig() *Config {
	return &Config{
		MirakurunPath:              "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/",
		RecordedDir:                "./recorded/",
		WUIHost:                    "0.0.0.0",
		WUIOpenPort:                20772,
		RecordedFormat:             "[<date:yymmdd-HHMM>][<type><channel>][<channel-name>]<title>.m2ts",
		RecordingPriority:          2,
		ConflictedPriority:         1,
		StorageLowSpaceThresholdMB: 3000,
		StorageLowSpaceAction:      "remove",
	}
}

func (c *Config) EffectiveMirakurunPath() string {
	if c.MirakurunPath != "" {
		return c.MirakurunPath
	}
	if c.SchedulerMirakurunPath != "" {
		return c.SchedulerMirakurunPath
	}
	return "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/"
}
