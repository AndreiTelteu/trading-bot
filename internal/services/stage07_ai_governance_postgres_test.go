package services

import (
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestStage07AIApprovalRecordsDispositionWithoutRuntimeMutation(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	previous := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previous })
	if err := db.Create(&database.Setting{Key: "risk_per_trade", Value: "1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.IndicatorWeight{Indicator: "rsi", Weight: 1}).Error; err != nil {
		t.Fatal(err)
	}
	key, value := "risk_per_trade", "99"
	proposal := database.AIProposal{ProposalType: "parameter_adjustment", ParameterKey: &key, NewValue: &value, Reasoning: "adversarial", Status: "pending", CreatedAt: time.Now().UTC()}
	if err := db.Create(&proposal).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ApproveProposal(proposal.ID); err != nil {
		t.Fatal(err)
	}
	var setting database.Setting
	db.First(&setting, "key=?", "risk_per_trade")
	if setting.Value != "1" {
		t.Fatalf("AI approval mutated risk setting to %s", setting.Value)
	}
	var weight database.IndicatorWeight
	db.First(&weight, "indicator=?", "rsi")
	if weight.Weight != 1 {
		t.Fatalf("AI approval mutated weight to %v", weight.Weight)
	}
	db.First(&proposal, proposal.ID)
	if proposal.Status != "approved" || proposal.ResolvedAt == nil {
		t.Fatalf("proposal disposition missing: %+v", proposal)
	}
}
