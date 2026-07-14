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
	WUIWebDir    string               `json:"wuiWebDir,omitempty"`
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
	StartMargin    int              `json:"startMargin"`
	EndMargin      int              `json:"endMargin"`
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
	MirakurunPath              string
	RecordedDir                string
	ExcludeServices            []int64
	ServiceOrder               []int64
	WUIAccounts                []WebUser
	WUIAuthenticationEnabled   bool
	WUIPort                    int
	WUIHost                    string
	WUIWebDir                  string
	NormalizationForm          string
	RecordedFormat             string
	RecordingStartMargin       int
	RecordingStartMarginSet    bool
	RecordingEndMargin         int
	RecordingPriority          int
	ConflictedPriority         int
	StorageLowSpaceThresholdMB int
	StorageLowSpaceAction      string
	PreviewCacheMaxAgeDays     int
	PreviewCacheMaxSizeMB      int
}

type LegacyConfig struct {
	MirakurunPath              string   `json:"mirakurunPath"`
	SchedulerMirakurunPath     string   `json:"schedulerMirakurunPath"`
	RecordedDir                string   `json:"recordedDir"`
	ExcludeServices            []int64  `json:"excludeServices"`
	ServiceOrder               []int64  `json:"serviceOrder"`
	WUIUsers                   []string `json:"wuiUsers"`
	WUIPort                    *int     `json:"wuiPort"`
	WUIHost                    string   `json:"wuiHost"`
	WUIOpenServer              bool     `json:"wuiOpenServer"`
	WUIOpenHost                string   `json:"wuiOpenHost"`
	WUIOpenPort                int      `json:"wuiOpenPort"`
	NormalizationForm          string   `json:"normalizationForm"`
	RecordedFormat             string   `json:"recordedFormat"`
	RecordingStartMargin       int      `json:"recordingStartMargin"`
	RecordingEndMargin         int      `json:"recordingEndMargin"`
	RecordingPriority          int      `json:"recordingPriority"`
	ConflictedPriority         int      `json:"conflictedPriority"`
	StorageLowSpaceThresholdMB int      `json:"storageLowSpaceThresholdMB"`
	StorageLowSpaceAction      string   `json:"storageLowSpaceAction"`
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
	if marker.Schema == "" {
		return nil, fmt.Errorf("legacy config is not a Strata runtime config; run strata-pvr migrate")
	}
	return loadDocument(b)
}

func LoadLegacy(path string) (*LegacyConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseLegacy(b)
}

func ParseLegacy(b []byte) (*LegacyConfig, error) {
	var marker struct {
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(b, &marker); err != nil {
		return nil, err
	}
	if marker.Schema != nil {
		return nil, fmt.Errorf("Strata config cannot be used as legacy migration input")
	}
	cfg := defaultLegacyConfig()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
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
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var recordingRaw map[string]json.RawMessage
	if err := json.Unmarshal(raw["recording"], &recordingRaw); err != nil {
		return nil, fmt.Errorf("recording settings must be an object")
	}
	if _, ok := recordingRaw["startMargin"]; !ok {
		doc.Recording.StartMargin = 15
	}
	if doc.Recording.LowSpace.Action != "remove" && doc.Recording.LowSpace.Action != "stop" {
		return nil, fmt.Errorf("recording.lowSpace.action must be remove or stop")
	}
	cfg := defaultConfig()
	cfg.MirakurunPath = doc.Mirakurun.URL
	cfg.RecordingPriority = doc.Mirakurun.RecordingPriority
	cfg.ConflictedPriority = doc.Mirakurun.ConflictedPriority
	cfg.RecordedDir = doc.Recording.Directory
	cfg.RecordedFormat = doc.Recording.FilenameFormat
	cfg.RecordingStartMargin = doc.Recording.StartMargin
	cfg.RecordingStartMarginSet = recordingRaw != nil && recordingRaw["startMargin"] != nil
	cfg.RecordingEndMargin = doc.Recording.EndMargin
	cfg.StorageLowSpaceThresholdMB = doc.Recording.LowSpace.ThresholdMB
	cfg.StorageLowSpaceAction = doc.Recording.LowSpace.Action
	cfg.WUIHost = doc.Web.ListenAddress
	cfg.WUIPort = doc.Web.Port
	cfg.WUIWebDir = doc.WUIWebDir
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
			StartMargin:    15,
			EndMargin:      0,
			LowSpace:       LowSpaceSettings{ThresholdMB: 3000, Action: "remove"},
		},
		Web:          WebSettings{ListenAddress: "0.0.0.0", Port: 20772},
		PreviewCache: PreviewCacheSettings{MaxAgeDays: 30, MaxSizeMB: 1024},
	}
}

func defaultConfig() *Config {
	return &Config{
		MirakurunPath:              "http://127.0.0.1:40772",
		RecordedDir:                "./recorded/",
		WUIHost:                    "0.0.0.0",
		WUIPort:                    20772,
		RecordedFormat:             "[<date:yymmdd-HHMM>][<type><channel>][<channel-name>]<title>.m2ts",
		RecordingStartMargin:       15,
		RecordingEndMargin:         0,
		RecordingPriority:          2,
		ConflictedPriority:         1,
		StorageLowSpaceThresholdMB: 3000,
		StorageLowSpaceAction:      "remove",
	}
}

func (c *Config) EffectiveMirakurunPath() string {
	return c.MirakurunPath
}

func defaultLegacyConfig() *LegacyConfig {
	return &LegacyConfig{
		MirakurunPath:              "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/",
		RecordedDir:                "./recorded/",
		WUIHost:                    "0.0.0.0",
		WUIOpenPort:                20772,
		RecordedFormat:             "[<date:yymmdd-HHMM>][<type><channel>][<channel-name>]<title>.m2ts",
		RecordingStartMargin:       15,
		RecordingEndMargin:         0,
		RecordingPriority:          2,
		ConflictedPriority:         1,
		StorageLowSpaceThresholdMB: 3000,
		StorageLowSpaceAction:      "remove",
	}
}

func (c *LegacyConfig) EffectiveMirakurunPath() string {
	if c.MirakurunPath != "" {
		return c.MirakurunPath
	}
	if c.SchedulerMirakurunPath != "" {
		return c.SchedulerMirakurunPath
	}
	return "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/"
}
