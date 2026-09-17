package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseProgressWithPrefixedTerminalNoise(t *testing.T) {
	line := `time="now" backtest_progress {"phase":"engine","lane":"baseline","bar_index":524,"bar_total":26208,"fraction":0.01999,"message":"engine_bar","elapsed_ms":2593}`
	update, ok := parseProgress(line)
	if !ok {
		t.Fatal("expected progress update")
	}
	if update.Phase != "engine" || update.Lane != "baseline" || update.BarIndex != 524 || update.ElapsedMS != 2593 {
		t.Fatalf("unexpected update: %+v", update)
	}
}

func TestRenderLineIncludesProgressAndETA(t *testing.T) {
	state := monitorState{
		last: &progressSample{Update: progressUpdate{Phase: "engine", Lane: "baseline", BarIndex: 50, BarTotal: 100, Fraction: .5}, At: time.Unix(100, 0)},
		rate: .01,
	}
	line := renderLine(state, 10, time.Unix(101, 0))
	for _, expected := range []string{"[#####.....]", "50.00%", "engine/baseline", "bars 50/100", "ETA 0m49s", "running"} {
		if !strings.Contains(line, expected) {
			t.Fatalf("rendered line %q does not contain %q", line, expected)
		}
	}
}

func TestETARequiresLiveProgressDelta(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "progress-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _ = file.WriteString("backtest_progress {\"phase\":\"engine\",\"lane\":\"baseline\",\"fraction\":0.04}\n")
	_, _ = file.Seek(0, 0)

	state := monitorState{}
	if err := consume(file, &state, time.Unix(100, 0), false); err != nil {
		t.Fatal(err)
	}
	if state.rate != 0 || !strings.Contains(renderLine(state, 10, time.Unix(101, 0)), "ETA n/a") {
		t.Fatalf("historical telemetry must not establish ETA: %+v", state)
	}

	offset, _ := file.Seek(0, 1)
	_, _ = file.WriteString("backtest_progress {\"phase\":\"engine\",\"lane\":\"baseline\",\"fraction\":0.05}\n")
	_, _ = file.Seek(offset, 0)
	if err := consume(file, &state, time.Unix(220, 0), true); err != nil {
		t.Fatal(err)
	}
	if state.rate < 0.0000833 || state.rate > 0.0000834 {
		t.Fatalf("rate = %f, want progress delta 0.01 / 120 seconds", state.rate)
	}
	if line := renderLine(state, 10, time.Unix(220, 0)); !strings.Contains(line, "ETA 3h09m59s") && !strings.Contains(line, "ETA 3h10m00s") {
		t.Fatalf("unexpected line: %s", line)
	}
}

func TestTerminalState(t *testing.T) {
	if got := terminalState("2026 Backtest failed: coverage"); got != "failed" {
		t.Fatalf("terminalState failure = %q", got)
	}
	if got := terminalState("SUCCESS: manifest-backed backtest initialized"); got != "completed" {
		t.Fatalf("terminalState success = %q", got)
	}
}
