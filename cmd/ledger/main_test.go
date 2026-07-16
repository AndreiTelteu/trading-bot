package main

import "testing"

func TestReconciliationExitCodeFailsUnbalancedGate(t *testing.T) {
	if reconciliationExitCode(true) != 0 || reconciliationExitCode(false) == 0 {
		t.Fatal("unsafe reconciliation exit code")
	}
}
