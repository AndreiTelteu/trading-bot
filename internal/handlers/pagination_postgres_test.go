package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/testutil"

	"github.com/gofiber/fiber/v2"
)

func TestListCursorsTraverseEqualTimestampsWithoutGaps(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	at := time.Now().UTC().Truncate(time.Microsecond)
	for index := 0; index < 3; index++ {
		if err := db.Create(&database.Order{OrderType: "BUY", Symbol: "BTCUSDT", ExecutedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.AIProposal{ProposalType: "parameter_adjustment", CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.BacktestJob{Status: "complete", CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	app.Get("/orders", GetOrders)
	app.Get("/proposals", GetAIProposals)
	app.Get("/backtests", ListBacktestJobs)
	for _, path := range []string{"/orders", "/proposals", "/backtests"} {
		first, err := app.Test(httptest.NewRequest("GET", path+"?limit=2", nil))
		if err != nil {
			t.Fatal(err)
		}
		var firstRows []struct{ ID uint }
		if err := json.NewDecoder(first.Body).Decode(&firstRows); err != nil {
			t.Fatal(err)
		}
		cursor := first.Header.Get("X-Next-Cursor")
		if len(firstRows) != 2 || cursor == "" || first.Header.Get("X-Result-Truncated") != "true" {
			t.Fatalf("%s first page is not explicitly bounded: rows=%d cursor=%q", path, len(firstRows), cursor)
		}
		second, err := app.Test(httptest.NewRequest("GET", path+"?limit=2&cursor="+cursor, nil))
		if err != nil {
			t.Fatal(err)
		}
		var secondRows []struct{ ID uint }
		if err := json.NewDecoder(second.Body).Decode(&secondRows); err != nil {
			t.Fatal(err)
		}
		if len(secondRows) != 1 || secondRows[0].ID == firstRows[0].ID || secondRows[0].ID == firstRows[1].ID || second.Header.Get("X-Result-Truncated") != "false" {
			t.Fatalf("%s cursor traversal duplicated, skipped, or hid truncation: first=%+v second=%+v", path, firstRows, secondRows)
		}
	}
}
