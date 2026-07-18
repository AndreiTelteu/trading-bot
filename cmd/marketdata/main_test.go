package main

import "testing"

func TestMarketdataReadOnlyActionsDoNotRequireWriterPools(t *testing.T) {
	for _, input := range []struct {
		action string
		dryRun bool
	}{{"coverage", false}, {"ingest", true}} {
		requirements := marketdataPoolRequirements(input.action, input.dryRun)
		if requirements.Migrate || !requirements.ValidateRuntime || requirements.LedgerWriter || requirements.ParityWriter {
			t.Fatalf("%+v requirements = %+v", input, requirements)
		}
	}
}

func TestMarketdataMutationsSelectLedgerForFreshInstallSeed(t *testing.T) {
	for _, input := range []struct {
		action string
		dryRun bool
	}{{"ingest", false}, {"import-metadata", true}, {"build-manifest", true}, {"build-universe", true}, {"build-universe-range", true}} {
		requirements := marketdataPoolRequirements(input.action, input.dryRun)
		if !requirements.Migrate || !requirements.ValidateRuntime || !requirements.LedgerWriter || requirements.ParityWriter {
			t.Fatalf("%+v requirements = %+v", input, requirements)
		}
	}
}
