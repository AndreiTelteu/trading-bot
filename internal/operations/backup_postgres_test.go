package operations

import (
	"context"
	"fmt"
	"testing"

	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestDatabaseFingerprintCoversRowsAndSecuritySchemaInventory(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	baseline, err := FingerprintDatabase(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Tables) == 0 || baseline.SchemaObjects["triggers"].Count == 0 || baseline.SchemaObjects["functions"].Count == 0 {
		t.Fatalf("incomplete fingerprint inventory: %+v", baseline.SchemaObjects)
	}
	for _, kind := range []string{"schemas", "relations", "privileges", "default_privileges", "rls_policies", "views", "sequences"} {
		if _, ok := baseline.SchemaObjects[kind]; !ok {
			t.Fatalf("fingerprint omits %s", kind)
		}
	}
	var setting database.Setting
	if err := db.First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&setting).UpdateColumn("value", setting.Value+"-changed").Error; err != nil {
		t.Fatal(err)
	}
	changedRow, err := FingerprintDatabase(context.Background(), db)
	if err != nil || changedRow.Digest == baseline.Digest {
		t.Fatalf("row change in an automatically inventoried table was not detected: %v", err)
	}
	if err := db.Model(&setting).UpdateColumn("value", setting.Value).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`CREATE TABLE unexpected_restore_object(id bigint primary key)`).Error; err != nil {
		t.Fatal(err)
	}
	extra, err := FingerprintDatabase(context.Background(), db)
	if err != nil || extra.Digest == baseline.Digest {
		t.Fatalf("extra table was not detected: %v", err)
	}
	if err := db.Exec(`DROP TABLE unexpected_restore_object`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE settings ADD CONSTRAINT fingerprint_fixture_check CHECK (length(key)>0)`).Error; err != nil {
		t.Fatal(err)
	}
	changedConstraint, err := FingerprintDatabase(context.Background(), db)
	if err != nil || changedConstraint.Digest == baseline.Digest {
		t.Fatalf("changed constraint was not detected: %v", err)
	}
	if err := db.Exec(`ALTER TABLE settings DROP CONSTRAINT fingerprint_fixture_check`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE FUNCTION fingerprint_acl_fixture() RETURNS integer LANGUAGE sql AS 'SELECT 1'`).Error; err != nil {
		t.Fatal(err)
	}
	functionDefaultACL, err := FingerprintDatabase(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	// PostgreSQL represents this as an explicit ACL even though it is its normal
	// NULL-proacl default. The effective authority must remain identical.
	if err := db.Exec(`GRANT EXECUTE ON FUNCTION fingerprint_acl_fixture() TO PUBLIC`).Error; err != nil {
		t.Fatal(err)
	}
	functionExplicitDefaultACL, err := FingerprintDatabase(context.Background(), db)
	if err != nil || functionExplicitDefaultACL.Digest != functionDefaultACL.Digest {
		t.Fatalf("explicit default function ACL changed fingerprint: %v", err)
	}
	if err := db.Exec(`REVOKE EXECUTE ON FUNCTION fingerprint_acl_fixture() FROM PUBLIC`).Error; err != nil {
		t.Fatal(err)
	}
	functionRevokedACL, err := FingerprintDatabase(context.Background(), db)
	if err != nil || functionRevokedACL.Digest == functionDefaultACL.Digest {
		t.Fatalf("meaningful function execute privilege change was not detected: %v", err)
	}
	if err := db.Exec(`ALTER FUNCTION fingerprint_acl_fixture() SECURITY DEFINER`).Error; err != nil {
		t.Fatal(err)
	}
	functionSecurityDefiner, err := FingerprintDatabase(context.Background(), db)
	if err != nil || functionSecurityDefiner.Digest == functionRevokedACL.Digest {
		t.Fatalf("function security-definer change was not detected: %v", err)
	}
	if err := db.Exec(`ALTER FUNCTION fingerprint_acl_fixture() VOLATILE; ALTER FUNCTION fingerprint_acl_fixture() STRICT; ALTER FUNCTION fingerprint_acl_fixture() SET search_path TO public`).Error; err != nil {
		t.Fatal(err)
	}
	functionExecutionProperties, err := FingerprintDatabase(context.Background(), db)
	if err != nil || functionExecutionProperties.Digest == functionSecurityDefiner.Digest {
		t.Fatalf("function volatility, strictness, or config change was not detected: %v", err)
	}
	if err := db.Exec(`CREATE OR REPLACE FUNCTION fingerprint_acl_fixture() RETURNS integer LANGUAGE sql AS 'SELECT 2'`).Error; err != nil {
		t.Fatal(err)
	}
	functionBody, err := FingerprintDatabase(context.Background(), db)
	if err != nil || functionBody.Digest == functionExecutionProperties.Digest {
		t.Fatalf("function body change was not detected: %v", err)
	}
	if err := db.Exec(`ALTER FUNCTION fingerprint_acl_fixture() OWNER TO trading_bot_ledger_owner`).Error; err != nil {
		t.Fatal(err)
	}
	functionOwner, err := FingerprintDatabase(context.Background(), db)
	if err != nil || functionOwner.Digest == functionBody.Digest {
		t.Fatalf("function owner change was not detected: %v", err)
	}

	if err := db.Exec(`DROP TRIGGER positions_economic_guard ON positions`).Error; err != nil {
		t.Fatal(err)
	}
	missingTrigger, err := FingerprintDatabase(context.Background(), db)
	if err != nil || missingTrigger.Digest == baseline.Digest {
		t.Fatalf("missing security trigger was not detected: %v", err)
	}

	// Authority-bearing metadata must affect the digest independently of rows.
	if err := db.Exec(`CREATE VIEW fingerprint_authority_view AS SELECT key FROM settings`).Error; err != nil {
		t.Fatal(err)
	}
	withView, err := FingerprintDatabase(context.Background(), db)
	if err != nil || withView.Digest == missingTrigger.Digest {
		t.Fatalf("view definition was not detected: %v", err)
	}
	if err := db.Exec(`CREATE SEQUENCE fingerprint_authority_sequence`).Error; err != nil {
		t.Fatal(err)
	}
	withSequence, err := FingerprintDatabase(context.Background(), db)
	if err != nil || withSequence.Digest == withView.Digest {
		t.Fatalf("sequence was not detected: %v", err)
	}
	if err := db.Exec(`ALTER TABLE settings ENABLE ROW LEVEL SECURITY; CREATE POLICY fingerprint_settings_policy ON settings USING (true)`).Error; err != nil {
		t.Fatal(err)
	}
	withRLS, err := FingerprintDatabase(context.Background(), db)
	if err != nil || withRLS.Digest == withSequence.Digest {
		t.Fatalf("RLS policy was not detected: %v", err)
	}
	if err := db.Exec(`GRANT SELECT ON settings TO trading_bot_ledger_owner`).Error; err != nil {
		t.Fatal(err)
	}
	withACL, err := FingerprintDatabase(context.Background(), db)
	if err != nil || withACL.Digest == withRLS.Digest {
		t.Fatalf("ACL was not detected: %v", err)
	}
	var owner string
	if err := db.Raw(`SELECT tableowner FROM pg_tables WHERE schemaname='public' AND tablename='settings'`).Scan(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE settings OWNER TO trading_bot_ledger_owner`).Error; err != nil {
		t.Fatal(err)
	}
	withOwner, err := FingerprintDatabase(context.Background(), db)
	if err != nil || withOwner.Digest == withACL.Digest {
		t.Fatalf("ownership was not detected: %v", err)
	}
	_ = db.Exec(fmt.Sprintf(`ALTER TABLE settings OWNER TO "%s"`, owner)).Error
}

func TestDatabaseFingerprintIncludesPhysicalColumnModifiers(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := db.Exec(`CREATE TABLE fingerprint_column_modifiers (name varchar(64) NOT NULL, amount numeric(20,8))`).Error; err != nil {
		t.Fatal(err)
	}
	baseline, err := FingerprintDatabase(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE fingerprint_column_modifiers ALTER COLUMN name TYPE varchar(4096)`).Error; err != nil {
		t.Fatal(err)
	}
	varcharDrift, err := FingerprintDatabase(context.Background(), db)
	if err != nil || varcharDrift.Digest == baseline.Digest {
		t.Fatalf("varchar modifier drift was not detected: %v", err)
	}
	if err := db.Exec(`ALTER TABLE fingerprint_column_modifiers ALTER COLUMN name TYPE varchar(64); ALTER TABLE fingerprint_column_modifiers ALTER COLUMN amount TYPE numeric(30,12)`).Error; err != nil {
		t.Fatal(err)
	}
	numericDrift, err := FingerprintDatabase(context.Background(), db)
	if err != nil || numericDrift.Digest == baseline.Digest {
		t.Fatalf("numeric precision/scale drift was not detected: %v", err)
	}
	if err := db.Exec(`ALTER TABLE fingerprint_column_modifiers ALTER COLUMN amount TYPE numeric(20,8)`).Error; err != nil {
		t.Fatal(err)
	}
	restored, err := FingerprintDatabase(context.Background(), db)
	if err != nil || restored.Digest != baseline.Digest {
		t.Fatalf("restored physical column definition did not recover its fingerprint: %v", err)
	}
}
