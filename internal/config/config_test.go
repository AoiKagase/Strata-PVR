package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreservesUnknownAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"recordedDir":"./rec/","vaapiEnabled":true,"vaapiDevice":"/dev/dri/renderD128","wuiAllowCountries":["JP","US"],"wuiMdnsAdvertisement":true,"operTweeter":true,"operTweeterAuth":{"consumerKey":"ck","consumerSecret":"cs","accessToken":"at","accessTokenSecret":"ats"},"operTweeterFormat":{"start":"start <title>","end":"end <title>"},"custom":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordedDir != "./rec/" {
		t.Fatalf("RecordedDir = %q", cfg.RecordedDir)
	}
	if cfg.EffectiveMirakurunPath() != "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/" {
		t.Fatalf("unexpected mirakurun path: %s", cfg.EffectiveMirakurunPath())
	}
	if !cfg.VAAPIEnabled || cfg.VAAPIDevice != "/dev/dri/renderD128" {
		t.Fatalf("VAAPI fields were not loaded: enabled=%v device=%q", cfg.VAAPIEnabled, cfg.VAAPIDevice)
	}
	if !cfg.WUIMdnsAdvertisement {
		t.Fatal("wuiMdnsAdvertisement was not loaded")
	}
	if len(cfg.WUIAllowCountries) != 2 || cfg.WUIAllowCountries[0] != "JP" || cfg.WUIAllowCountries[1] != "US" {
		t.Fatalf("wuiAllowCountries was not loaded: %#v", cfg.WUIAllowCountries)
	}
	if !cfg.OperTweeter || cfg.OperTweeterAuth == nil || cfg.OperTweeterAuth.ConsumerKey != "ck" || cfg.OperTweeterFormat == nil || cfg.OperTweeterFormat.End != "end <title>" {
		t.Fatalf("operTweeter fields were not loaded: %#v", cfg)
	}
	if _, ok := cfg.Raw["custom"]; !ok {
		t.Fatal("unknown field was not preserved in Raw")
	}
}
