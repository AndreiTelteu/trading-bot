package decisionmodel

import (
	"context"
	"errors"
	"testing"

	"trading-go/internal/database"
	"trading-go/internal/testutil"

	"gorm.io/gorm"
)

func TestPostgresStoreMigrationAndAuthority(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct {
		role, privilege string
		want            bool
	}{
		{"trading_bot_runtime", "SELECT", true}, {"trading_bot_runtime", "INSERT", true},
		{"trading_bot_runtime", "UPDATE", false}, {"trading_bot_runtime", "DELETE", false}, {"trading_bot_runtime", "TRUNCATE", false},
		{"trading_bot_ledger_writer", "SELECT", false}, {"trading_bot_ledger_writer", "INSERT", false},
		{"trading_bot_ledger_writer", "UPDATE", false}, {"trading_bot_ledger_writer", "DELETE", false}, {"trading_bot_ledger_writer", "TRUNCATE", false},
		{"trading_bot_parity_writer", "SELECT", false}, {"trading_bot_parity_writer", "INSERT", false},
		{"trading_bot_parity_writer", "UPDATE", false}, {"trading_bot_parity_writer", "DELETE", false}, {"trading_bot_parity_writer", "TRUNCATE", false},
	} {
		var got bool
		if err := db.Raw("SELECT has_table_privilege(?, 'decision_model_responses', ?)", check.role, check.privilege).Scan(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Errorf("%s %s=%v want %v", check.role, check.privilege, got, check.want)
		}
	}

	request := sampleRequest()
	response, err := parseSystemOneResponse([]byte(validWireResponse), request)
	if err != nil {
		t.Fatal(err)
	}
	response.ModelIdentity = "test/model"
	response.Driver = SystemOneDriverName
	response.RequestDigest = RequestDigest(response.ModelIdentity, response.Driver, request)
	ctx := context.Background()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_runtime").Error; err != nil {
			return err
		}
		store := NewPostgresStore(tx)
		if err := store.Save(ctx, request, response); err != nil {
			return err
		}
		if err := store.Save(ctx, request, response); err != nil {
			return err
		}
		loaded, found, err := store.Load(ctx, response.RequestDigest)
		if err != nil || !found || !equalResponses(loaded, response) {
			t.Fatalf("round trip found=%v err=%v", found, err)
		}
		changed := response
		changed.ResolvedModel = "other"
		if err := store.Save(ctx, request, changed); !errors.Is(err, ErrCacheConflict) {
			t.Fatalf("conflict: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, statement := range []string{
		"UPDATE decision_model_responses SET resolved_model='other'",
		"DELETE FROM decision_model_responses",
	} {
		if err := db.Exec(statement).Error; err == nil {
			t.Errorf("owner mutation succeeded: %s", statement)
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL ROLE trading_bot_runtime").Error; err != nil {
				return err
			}
			return tx.Exec(statement).Error
		}); err == nil {
			t.Errorf("runtime mutation succeeded: %s", statement)
		}
	}
	// The administrative test reset needs TRUNCATE; it is rolled back here so
	// the stored response remains available for the corruption check below.
	rollback := errors.New("rollback administrative truncate")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("TRUNCATE decision_model_responses").Error; err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("administrative truncate failed: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_runtime").Error; err != nil {
			return err
		}
		return tx.Exec("TRUNCATE decision_model_responses").Error
	}); err == nil {
		t.Fatal("runtime truncate succeeded")
	}
	for _, statement := range []string{
		`INSERT INTO decision_model_responses(request_digest,model_identity,driver,resolved_model,request_json,response_json) VALUES ('bad','x','x','x','{}','{}')`,
		`INSERT INTO decision_model_responses(request_digest,model_identity,driver,resolved_model,request_json,response_json,input_tokens) VALUES (repeat('a',64),'x','x','x','{}','{}',-1)`,
		`INSERT INTO decision_model_responses(request_digest,model_identity,driver,resolved_model,request_json,response_json) VALUES (repeat('b',64),NULL,'x','x','{}','{}')`,
	} {
		if err := db.Exec(statement).Error; err == nil {
			t.Errorf("invalid row accepted: %s", statement)
		}
	}

	// Administrative corruption is confined to setup; production triggers stay enabled.
	testutil.WithCorruptedLedgerStorage(t, db, func(tx *gorm.DB) error {
		return tx.Exec(`UPDATE decision_model_responses SET request_json = '{"schema":"decision-model-request-v1"}'::jsonb WHERE request_digest = ?`, response.RequestDigest).Error
	})
	_, _, err = NewPostgresStore(db).Load(ctx, response.RequestDigest)
	if !errors.Is(err, ErrCacheCorrupt) || errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("tampered request: %v", err)
	}
}
