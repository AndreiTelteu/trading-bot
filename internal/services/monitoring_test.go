package services

import (
	"testing"
	"time"
)

func TestDecisionLabelsAreFixedHorizonCohortEvidence(t *testing.T) {
	if defaultDecisionLabelHorizon != 24*time.Hour {
		t.Fatalf("default label horizon = %s", defaultDecisionLabelHorizon)
	}
}
