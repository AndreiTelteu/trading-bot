package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestRestoreScriptPinsAllWritesToExplicitTargetOnly(t *testing.T) {
	payload, err := os.ReadFile("stage08_backup_restore.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	for _, required := range []string{
		`DATABASE_URL="$target_conn" MIGRATION_DATABASE_URL="$target_conn"`,
		`DATABASE_URL="$target_conn" MIGRATION_DATABASE_URL=`,
		`DATABASE_URL="$1" MIGRATION_DATABASE_URL=`,
		`STAGE08_RESTORE_SERVICE`,
		`STAGE08_RECORD_SERVICE`,
		`record service must resolve to the restore target database identity`,
		`record service must use a distinct constrained runtime principal`,
		`record_runtime_conn="$record_conn user=$(libpq_quote "$record_user")"`,
		`DATABASE_URL="$record_runtime_conn" MIGRATION_DATABASE_URL=`,
		`value="${value//\\/\\\\}"`,
		`value="${value//\'/\\\'}"`,
		`restore target '$target_db' is not empty`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("restore script lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		`source_db" == "trading_bot_test`,
		`DATABASE_URL="$source_conn" MIGRATION_DATABASE_URL= GOCACHE=`,
		`dropdb`,
		`createdb`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("restore script contains forbidden source/destructive flow %q", forbidden)
		}
	}
}

func TestRestoreScriptRequiresSeparateRuntimeRecordServiceOutsideTestMode(t *testing.T) {
	payload, err := os.ReadFile("stage08_backup_restore.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	for _, required := range []string{
		`if [[ "$test_mode" == 0 ]]; then`,
		`[[ -n "$record_service" && -n "$principal" ]] || usage`,
		`[[ "$record_service" != "$source_service" && "$record_service" != "$restore_service" ]]`,
		`[[ "$record_identity" == "$target_identity" ]]`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("non-test record-service guard lacks %q", required)
		}
	}
}
