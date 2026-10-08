package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultAddr(t *testing.T) {
	cfg, err := Load(strings.NewReader("# nothing set\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != DefaultAddr || cfg.Retries != DefaultRetries {
		t.Fatalf("cfg = %+v, want default addr and retries", cfg)
	}
}

func TestLoadParsesKeys(t *testing.T) {
	cfg, err := Load(strings.NewReader("addr = a:1\ntimeout = 2m\nretries = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "a:1" || cfg.Timeout != 2*time.Minute || cfg.Retries != 0 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	if _, err := Load(strings.NewReader("tmieout = 1s\n")); err == nil {
		t.Fatal("expected an error for an unknown key")
	}
}
