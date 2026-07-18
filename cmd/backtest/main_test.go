package main

import "testing"

func TestBacktestSelectsRuntimeAndLedgerPools(t *testing.T) {
	requirements := backtestPoolRequirements()
	if !requirements.Migrate || !requirements.ValidateRuntime || !requirements.LedgerWriter || requirements.ParityWriter {
		t.Fatalf("requirements = %+v", requirements)
	}
}
