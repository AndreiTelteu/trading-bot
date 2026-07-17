package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var activeState struct {
	sync.RWMutex
	flags       Flags
	snapshotID  string
	authority   string
	initialized bool
}

// Activate is retained for isolated unit tests. Production binaries must call
// ActivateVerified only after the persisted cutover state is locked and checked.
func Activate(flags Flags) error {
	return ActivateVerified(flags, "", "unverified")
}
func ActivateVerified(flags Flags, snapshotID, authority string) error {
	if err := flags.Validate(); err != nil {
		return err
	}
	if snapshotID != "" {
		_, digest, err := flags.Canonical()
		if err != nil || digest != snapshotID {
			return fmt.Errorf("verified flag snapshot digest mismatch")
		}
	}
	activeState.Lock()
	activeState.flags, activeState.snapshotID, activeState.authority, activeState.initialized = flags, snapshotID, authority, true
	activeState.Unlock()
	return nil
}
func ActiveEvidence() (Flags, string, string, bool) {
	activeState.RLock()
	defer activeState.RUnlock()
	return activeState.flags, activeState.snapshotID, activeState.authority, activeState.initialized
}
func Active() (Flags, bool) {
	activeState.RLock()
	defer activeState.RUnlock()
	return activeState.flags, activeState.initialized
}
func ResetForTest() {
	activeState.Lock()
	activeState.flags, activeState.snapshotID, activeState.authority, activeState.initialized = Flags{}, "", "", false
	activeState.Unlock()
}

const FlagSchemaVersion = "stage08-flags-v1"

type Flags struct {
	SchemaVersion     string `json:"schema_version"`
	LedgerAuthority   string `json:"ledger_authority"`
	SharedEngine      string `json:"shared_engine"`
	NewBacktest       string `json:"new_backtest"`
	PointInTime       string `json:"point_in_time_universe"`
	CandidateStrategy string `json:"candidate_strategy"`
	DualRun           string `json:"dual_run"`
	Stage07Context    string `json:"stage07_context,omitempty"`
}

func SafeFlags() Flags {
	return Flags{SchemaVersion: FlagSchemaVersion, LedgerAuthority: "legacy", SharedEngine: "off", NewBacktest: "off", PointInTime: "off", CandidateStrategy: "off", DualRun: "off"}
}

func Parse(values map[string]string) (Flags, error) {
	f := SafeFlags()
	assign := func(key string, target *string) {
		if value, ok := values[key]; ok {
			*target = strings.TrimSpace(strings.ToLower(value))
		}
	}
	assign("STAGE08_FLAG_SCHEMA_VERSION", &f.SchemaVersion)
	assign("STAGE08_LEDGER_AUTHORITY", &f.LedgerAuthority)
	assign("STAGE08_SHARED_ENGINE", &f.SharedEngine)
	assign("STAGE08_NEW_BACKTEST", &f.NewBacktest)
	assign("STAGE08_POINT_IN_TIME_UNIVERSE", &f.PointInTime)
	assign("STAGE08_CANDIDATE_STRATEGY", &f.CandidateStrategy)
	assign("STAGE08_DUAL_RUN", &f.DualRun)
	if value, ok := values["STAGE08_STAGE07_CONTEXT"]; ok {
		f.Stage07Context = strings.TrimSpace(value)
	}
	return f, f.Validate()
}

func (f Flags) Validate() error {
	if f.SchemaVersion != FlagSchemaVersion {
		return fmt.Errorf("unsupported STAGE08_FLAG_SCHEMA_VERSION %q (required %q)", f.SchemaVersion, FlagSchemaVersion)
	}
	checks := []struct {
		name, value string
		allowed     []string
	}{
		{"STAGE08_LEDGER_AUTHORITY", f.LedgerAuthority, []string{"legacy", "compare", "authoritative"}},
		{"STAGE08_SHARED_ENGINE", f.SharedEngine, []string{"off", "shadow", "paper", "limited_live", "full_live"}},
		{"STAGE08_NEW_BACKTEST", f.NewBacktest, []string{"off", "research"}},
		{"STAGE08_POINT_IN_TIME_UNIVERSE", f.PointInTime, []string{"off", "research", "authoritative"}},
		{"STAGE08_CANDIDATE_STRATEGY", f.CandidateStrategy, []string{"off", "research", "shadow", "paper", "limited_live", "full_live"}},
		{"STAGE08_DUAL_RUN", f.DualRun, []string{"off", "observe"}},
	}
	for _, check := range checks {
		valid := false
		for _, allowed := range check.allowed {
			valid = valid || check.value == allowed
		}
		if !valid {
			return fmt.Errorf("unknown or malformed %s=%q (allowed: %s)", check.name, check.value, strings.Join(check.allowed, ","))
		}
	}
	if f.DualRun == "observe" && f.SharedEngine == "off" {
		return fmt.Errorf("STAGE08_DUAL_RUN=observe requires STAGE08_SHARED_ENGINE=shadow or stronger")
	}
	if f.CandidateStrategy != "off" && f.CandidateStrategy != "research" && (f.SharedEngine == "off" || f.PointInTime == "off") {
		return fmt.Errorf("candidate strategy shadow/capital modes require shared engine and point-in-time universe")
	}
	if f.CandidateStrategy == "paper" && f.SharedEngine != "paper" {
		return fmt.Errorf("candidate paper requires STAGE08_SHARED_ENGINE=paper")
	}
	if f.SharedEngine == "paper" && f.LedgerAuthority != "authoritative" {
		return fmt.Errorf("new paper requires authoritative ledger")
	}
	if f.PointInTime == "authoritative" && f.SharedEngine == "off" {
		return fmt.Errorf("authoritative point-in-time universe requires shared engine")
	}
	if f.IsLive() {
		if f.LedgerAuthority != "authoritative" || f.PointInTime != "authoritative" || f.Stage07Context == "" {
			return fmt.Errorf("live modes require authoritative ledger/PIT universe and exact STAGE08_STAGE07_CONTEXT")
		}
		if f.SharedEngine != f.CandidateStrategy {
			return fmt.Errorf("live shared-engine and candidate modes must match exactly")
		}
	}
	return nil
}

func (f Flags) IsLive() bool {
	return f.SharedEngine == "limited_live" || f.SharedEngine == "full_live" || f.CandidateStrategy == "limited_live" || f.CandidateStrategy == "full_live"
}
func (f Flags) CapitalEnabled() bool { return f.SharedEngine == "paper" || f.IsLive() }
func (f Flags) Canonical() ([]byte, string, error) {
	data, err := json.Marshal(f)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func (f Flags) ObservationContext(activePath string, versions map[string]string) string {
	_, flagID, _ := f.Canonical()
	if _, verifiedID, _, ok := ActiveEvidence(); ok && verifiedID != "" {
		flagID = verifiedID
	}
	copyVersions := map[string]string{}
	for key, value := range versions {
		copyVersions[key] = value
	}
	base := struct {
		SchemaVersion  string            `json:"schema_version"`
		FlagSnapshotID string            `json:"flag_snapshot_id"`
		ActivePath     string            `json:"active_path"`
		Flags          Flags             `json:"flags"`
		Versions       map[string]string `json:"versions"`
	}{"stage08-observation-context-v2", flagID, activePath, f, copyVersions}
	canonical, _ := json.Marshal(base)
	sum := sha256.Sum256(canonical)
	payload, _ := json.Marshal(struct {
		SchemaVersion  string            `json:"schema_version"`
		FlagSnapshotID string            `json:"flag_snapshot_id"`
		ActivePath     string            `json:"active_path"`
		Flags          Flags             `json:"flags"`
		Versions       map[string]string `json:"versions"`
		ContentDigest  string            `json:"content_digest"`
	}{base.SchemaVersion, base.FlagSnapshotID, base.ActivePath, base.Flags, base.Versions, hex.EncodeToString(sum[:])})
	return string(payload)
}

// AuthorizeRuntimeSetting prevents persisted settings and AI proposals from
// selecting authority beyond the validated startup envelope.
func (f Flags) AuthorizeRuntimeSetting(engineMode string) error {
	switch strings.ToLower(strings.TrimSpace(engineMode)) {
	case "legacy":
		return nil
	case "shadow_compare":
		if f.DualRun == "observe" && f.SharedEngine != "off" {
			return nil
		}
	case "shared":
		if f.SharedEngine == "paper" && f.LedgerAuthority == "authoritative" {
			return nil
		}
	default:
		return fmt.Errorf("unknown trading_engine_mode %q", engineMode)
	}
	return fmt.Errorf("trading_engine_mode %q exceeds validated Stage 08 authority", engineMode)
}
