package config

import "testing"

func TestLoadValidatedRejectsMalformedBeforeStartup(t *testing.T) {
	t.Setenv("STAGE08_SHARED_ENGINE", "typo")
	if _, err := LoadValidated(); err == nil {
		t.Fatal("unknown mode accepted")
	}
	t.Setenv("STAGE08_SHARED_ENGINE", "off")
	t.Setenv("DB_MAX_OPEN_CONNS", "not-an-int")
	if _, err := LoadValidated(); err == nil {
		t.Fatal("malformed pool env accepted")
	}
}
