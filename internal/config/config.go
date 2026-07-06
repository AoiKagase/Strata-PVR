package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	UID                        any                        `json:"uid"`
	GID                        any                        `json:"gid"`
	MirakurunPath              string                     `json:"mirakurunPath"`
	SchedulerMirakurunPath     string                     `json:"schedulerMirakurunPath"`
	RecordedDir                string                     `json:"recordedDir"`
	ExcludeServices            []int64                    `json:"excludeServices"`
	ServiceOrder               []int64                    `json:"serviceOrder"`
	WUIUsers                   []string                   `json:"wuiUsers"`
	WUIAllowCountries          []string                   `json:"wuiAllowCountries"`
	WUIPort                    *int                       `json:"wuiPort"`
	WUIHost                    string                     `json:"wuiHost"`
	WUITlsKeyPath              string                     `json:"wuiTlsKeyPath"`
	WUITlsCertPath             string                     `json:"wuiTlsCertPath"`
	WUITlsPassphrase           string                     `json:"wuiTlsPassphrase"`
	WUITlsRequestCert          bool                       `json:"wuiTlsRequestCert"`
	WUITlsRejectUnauthorized   bool                       `json:"wuiTlsRejectUnauthorized"`
	WUITlsCaPath               string                     `json:"wuiTlsCaPath"`
	WUIOpenServer              bool                       `json:"wuiOpenServer"`
	WUIOpenHost                string                     `json:"wuiOpenHost"`
	WUIOpenPort                int                        `json:"wuiOpenPort"`
	WUIXFF                     bool                       `json:"wuiXFF"`
	WUIMdnsAdvertisement       bool                       `json:"wuiMdnsAdvertisement"`
	NormalizationForm          string                     `json:"normalizationForm"`
	RecordedFormat             string                     `json:"recordedFormat"`
	RecordingPriority          int                        `json:"recordingPriority"`
	ConflictedPriority         int                        `json:"conflictedPriority"`
	StorageLowSpaceThresholdMB int                        `json:"storageLowSpaceThresholdMB"`
	StorageLowSpaceAction      string                     `json:"storageLowSpaceAction"`
	StorageLowSpaceNotifyTo    string                     `json:"storageLowSpaceNotifyTo"`
	StorageLowSpaceCommand     string                     `json:"storageLowSpaceCommand"`
	SchedulerStartCommand      string                     `json:"schedulerStartCommand"`
	SchedulerEndCommand        string                     `json:"schedulerEndCommand"`
	EPGStartCommand            string                     `json:"epgStartCommand"`
	EPGEndCommand              string                     `json:"epgEndCommand"`
	ConflictCommand            string                     `json:"conflictCommand"`
	RecordedCommand            string                     `json:"recordedCommand"`
	Raw                        map[string]json.RawMessage `json:"-"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
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
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		MirakurunPath:              "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/",
		RecordedDir:                "./recorded/",
		WUIHost:                    "0.0.0.0",
		WUIOpenPort:                20772,
		WUITlsRejectUnauthorized:   true,
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
