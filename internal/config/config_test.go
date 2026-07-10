package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"strata-pvr/internal/storage"
)

func TestLoadLegacyPreservesUnknownAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"recordedDir":"./rec/","vaapiEnabled":true,"vaapiDevice":"/dev/dri/renderD128","wuiAllowCountries":["JP","US"],"wuiMdnsAdvertisement":true,"operTweeter":true,"operTweeterAuth":{"consumerKey":"ck","consumerSecret":"cs","accessToken":"at","accessTokenSecret":"ats"},"operTweeterFormat":{"start":"start <title>","end":"end <title>"},"custom":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLegacy(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordedDir != "./rec/" {
		t.Fatalf("RecordedDir = %q", cfg.RecordedDir)
	}
	if cfg.EffectiveMirakurunPath() != "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/" {
		t.Fatalf("unexpected mirakurun path: %s", cfg.EffectiveMirakurunPath())
	}
	for _, key := range []string{"wuiAllowCountries", "wuiMdnsAdvertisement", "operTweeter", "operTweeterAuth", "operTweeterFormat"} {
		if _, ok := cfg.Raw[key]; !ok {
			t.Fatalf("retired legacy field %q was not retained for migration diagnostics", key)
		}
	}
	if _, ok := cfg.Raw["custom"]; !ok {
		t.Fatal("unknown field was not preserved in Raw")
	}
}

func TestSampleConfigLoads(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveMirakurunPath() == "" {
		t.Fatal("sample config has no Mirakurun path")
	}
	if cfg.RecordedDir == "" {
		t.Fatal("sample config has no recordedDir")
	}
	if cfg.RecordedFormat == "" {
		t.Fatal("sample config has no recordedFormat")
	}
	if cfg.WUIAuthenticationEnabled {
		t.Fatalf("sample config should be native Strata with authentication disabled: %#v", cfg)
	}
}

func TestLoadStrataDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := DefaultDocument()
	doc.Web.Authentication = AuthenticationSettings{
		Enabled: true,
		Users:   []WebUser{{Username: "admin", PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA"}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MirakurunPath != doc.Mirakurun.URL || cfg.WUIPort != doc.Web.Port {
		t.Fatalf("Strata document was not mapped: %#v", cfg)
	}
	if len(cfg.WUIAccounts) != 1 || cfg.WUIAccounts[0].Username != "admin" {
		t.Fatalf("Strata accounts were not mapped: %#v", cfg.WUIAccounts)
	}
}

func TestLoadStrataDocumentUsesOpenListenerWhenAuthenticationDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := storage.WriteJSONAtomic(path, DefaultDocument(), true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WUIPort != 20772 || cfg.WUIAuthenticationEnabled {
		t.Fatalf("unexpected unauthenticated listener mapping: %#v", cfg)
	}
}

func TestParseRejectsLegacyRuntimeConfig(t *testing.T) {
	if _, err := Parse([]byte(`{"mirakurunPath":"http://127.0.0.1:40772"}`)); err == nil {
		t.Fatal("legacy config should not load as a runtime config")
	}
}
