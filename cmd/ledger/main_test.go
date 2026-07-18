package main

import "testing"

func TestReconciliationExitCodeFailsUnbalancedGate(t *testing.T) {
	if reconciliationExitCode(true) != 0 || reconciliationExitCode(false) == 0 {
		t.Fatal("unsafe reconciliation exit code")
	}
}

func TestLedgerActionsSelectLedgerWriterWithoutParity(t *testing.T) {
	for _, action := range []string{"reconcile", "backfill", "asset-correction", "reverse"} {
		requirements := ledgerPoolRequirements(action)
		if !requirements.Migrate || !requirements.ValidateRuntime || !requirements.LedgerWriter || requirements.ParityWriter {
			t.Fatalf("%s requirements = %+v", action, requirements)
		}
	}
}
