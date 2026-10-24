package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingPathReturnsDefault(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("Load with missing file = %+v, want Default()", got)
	}
}

func TestLoadEmptyPathReturnsDefault(t *testing.T) {
	got, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("Load(\"\") = %+v, want Default()", got)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"listen_port": 9999, "pitch": {"curve": {"dead_zone": 0.1}, "failsafe": "hold"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ListenPort != 9999 {
		t.Errorf("ListenPort = %d, want 9999", got.ListenPort)
	}
	if got.Pitch.Curve.DeadZone != 0.1 {
		t.Errorf("Pitch.Curve.DeadZone = %v, want 0.1", got.Pitch.Curve.DeadZone)
	}
	if got.Pitch.Failsafe != FailsafeHold {
		t.Errorf("Pitch.Failsafe = %v, want hold", got.Pitch.Failsafe)
	}
	if got.Roll != Default().Roll {
		t.Errorf("Roll = %+v, want unchanged Default().Roll = %+v", got.Roll, Default().Roll)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadRejectsInvalidFailsafeValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"pitch": {"failsafe": "netural"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid failsafe value")
	}
}
