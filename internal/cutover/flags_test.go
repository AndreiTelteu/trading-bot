package cutover

import "testing"

func TestSafeFlagsAndDependencies(t *testing.T) {
	if err := SafeFlags().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []Flags{
		{SchemaVersion: "unknown", LedgerAuthority: "legacy", SharedEngine: "off", NewBacktest: "off", PointInTime: "off", CandidateStrategy: "off", DualRun: "off"},
		{SchemaVersion: FlagSchemaVersion, LedgerAuthority: "legacy", SharedEngine: "off", NewBacktest: "off", PointInTime: "off", CandidateStrategy: "off", DualRun: "observe"},
		{SchemaVersion: FlagSchemaVersion, LedgerAuthority: "legacy", SharedEngine: "paper", NewBacktest: "off", PointInTime: "research", CandidateStrategy: "off", DualRun: "off"},
		{SchemaVersion: FlagSchemaVersion, LedgerAuthority: "authoritative", SharedEngine: "paper", NewBacktest: "off", PointInTime: "off", CandidateStrategy: "paper", DualRun: "off"},
		{SchemaVersion: FlagSchemaVersion, LedgerAuthority: "authoritative", SharedEngine: "limited_live", NewBacktest: "off", PointInTime: "authoritative", CandidateStrategy: "limited_live", DualRun: "off"},
	}
	for i, flags := range cases {
		if err := flags.Validate(); err == nil {
			t.Errorf("case %d unexpectedly valid: %+v", i, flags)
		}
	}
	valid := Flags{SchemaVersion: FlagSchemaVersion, LedgerAuthority: "authoritative", SharedEngine: "paper", NewBacktest: "research", PointInTime: "research", CandidateStrategy: "paper", DualRun: "observe"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSettingsCannotExceedEnvelope(t *testing.T) {
	flags := SafeFlags()
	if err := flags.AuthorizeRuntimeSetting("shared"); err == nil {
		t.Fatal("safe default authorized shared capital path")
	}
	if err := flags.AuthorizeRuntimeSetting("legacy"); err != nil {
		t.Fatal(err)
	}
	flags.SharedEngine = "shadow"
	flags.DualRun = "observe"
	if err := flags.AuthorizeRuntimeSetting("shadow_compare"); err != nil {
		t.Fatal(err)
	}
	if err := flags.AuthorizeRuntimeSetting("shared"); err == nil {
		t.Fatal("shadow authorized paper")
	}
}
