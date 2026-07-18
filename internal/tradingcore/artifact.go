package tradingcore

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

// The governance digest is derived from the checked-in source artifact that is
// compiled into this package, not from a human-maintained version label. Shared
// contracts/risk/execution sources are included because they affect observable
// strategy behavior just as the selected strategy implementation does.
//
//go:embed brokers.go contracts.go determinism.go exact.go exits.go orchestrator.go risk.go rollout.go snapshots.go values.go
var sharedStrategyArtifacts embed.FS

//go:embed strategy.go
var legacyStrategyArtifact []byte

//go:embed target_strategy.go
var targetStrategyArtifact []byte

func StrategyArtifactDigest(strategy string) string {
	hash := sha256.New()
	hash.Write([]byte("tradingcore-strategy-artifact-v1\x00"))
	for _, name := range []string{"brokers.go", "contracts.go", "determinism.go", "exact.go", "exits.go", "orchestrator.go", "risk.go", "rollout.go", "snapshots.go", "values.go"} {
		payload, err := sharedStrategyArtifacts.ReadFile(name)
		if err != nil {
			return ""
		}
		hash.Write([]byte(name + "\x00"))
		hash.Write(payload)
	}
	switch strategy {
	case "legacy":
		hash.Write(legacyStrategyArtifact)
	case "target":
		hash.Write(targetStrategyArtifact)
	default:
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}
