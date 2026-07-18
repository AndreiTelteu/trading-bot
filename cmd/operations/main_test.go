package main

import "testing"

func TestBackupActionsNeverMigrateSource(t *testing.T) {
	for _, action := range []string{"fingerprint", "record-backup"} {
		if actionMigratesTarget(action) {
			t.Fatalf("%s must be read/open-only", action)
		}
	}
	if !actionMigratesTarget("restore-verify") {
		t.Fatal("restore verification must migrate its explicitly configured target")
	}
}

func TestOperationsPoolSelectionKeepsTrustedReadsOutOfRuntimeValidation(t *testing.T) {
	for _, action := range []string{"fingerprint", "restore-verify"} {
		requirements := operationsPoolRequirements(action)
		if !requirements.TrustedOperator || requirements.ValidateRuntime || requirements.LedgerWriter || requirements.ParityWriter {
			t.Fatalf("%s requirements = %+v", action, requirements)
		}
	}
	record := operationsPoolRequirements("record-backup")
	if !record.ValidateRuntime || record.TrustedOperator || record.Migrate || record.LedgerWriter || record.ParityWriter {
		t.Fatalf("record-backup requirements = %+v", record)
	}
	for _, action := range []string{"verify", "status"} {
		requirements := operationsPoolRequirements(action)
		if !requirements.ValidateRuntime || !requirements.LedgerWriter || requirements.ParityWriter {
			t.Fatalf("%s requirements = %+v", action, requirements)
		}
	}
}

func TestOnlyRestoreBackupActionsUsePersistedAuthority(t *testing.T) {
	for _, action := range []string{"restore-verify", "record-backup"} {
		if !actionUsesPersistedAuthority(action) {
			t.Fatalf("%s must load the restored persisted authority", action)
		}
	}
	for _, action := range []string{"verify", "status", "fingerprint"} {
		if actionUsesPersistedAuthority(action) {
			t.Fatalf("%s must retain local configuration mismatch protection", action)
		}
	}
}

func TestFingerprintSkipsAllStage08ServiceInitialization(t *testing.T) {
	if got := stage08InitializationFor("fingerprint"); got != stage08InitializationNone {
		t.Fatalf("fingerprint initialization = %v, want none", got)
	}
	for _, action := range []string{"verify", "status"} {
		if got := stage08InitializationFor(action); got != stage08InitializationNormal {
			t.Fatalf("%s initialization = %v, want normal", action, got)
		}
	}
	for _, action := range []string{"restore-verify", "record-backup"} {
		if got := stage08InitializationFor(action); got != stage08InitializationPersisted {
			t.Fatalf("%s initialization = %v, want persisted", action, got)
		}
	}
}

func TestFingerprintIgnoresLocalStage08FlagsWithoutUsingPersistedInitialization(t *testing.T) {
	if !actionIgnoresLocalStage08Flags("fingerprint") {
		t.Fatal("fingerprint must not depend on local Stage 08 flags")
	}
	if actionUsesPersistedAuthority("fingerprint") {
		t.Fatal("fingerprint must not load persisted authority")
	}
}
