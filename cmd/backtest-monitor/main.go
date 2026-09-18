// Command backtest-monitor discovers file-backed backtest runs and renders live
// progress without influencing the deterministic backtest process.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const progressPrefix = "backtest_progress "

const (
	backtestProgressLog = "backtest.json.raw"
	initializationLog   = "backtest_init.log"
)

type progressUpdate struct {
	Phase       string  `json:"phase"`
	Lane        string  `json:"lane,omitempty"`
	BarIndex    int     `json:"bar_index,omitempty"`
	BarTotal    int     `json:"bar_total,omitempty"`
	WindowIndex int     `json:"window_index,omitempty"`
	WindowTotal int     `json:"window_total,omitempty"`
	Fraction    float64 `json:"fraction,omitempty"`
	Message     string  `json:"message,omitempty"`
	ElapsedMS   int64   `json:"elapsed_ms,omitempty"`
}

type progressSample struct {
	Update progressUpdate
	At     time.Time
}

type runInfo struct {
	Dir       string
	Log       string
	Modified  time.Time
	State     string
	Last      *progressSample
	StartedAt time.Time
}

type monitorState struct {
	last       *progressSample
	previous   *progressSample
	rate       float64
	terminal   string
	terminalAt time.Time
}

func main() {
	rootDefault := os.Getenv("BACKTEST_MONITOR_ROOT")
	if rootDefault == "" {
		rootDefault = "instance/backtest-init"
	}
	fs := flag.NewFlagSet("backtest-monitor", flag.ExitOnError)
	root := fs.String("root", rootDefault, "directory containing backtest-init run directories")
	width := fs.Int("width", 36, "ASCII progress bar width")
	staleAfter := fs.Duration("stale-after", 30*time.Minute, "age after which a non-terminal run is shown as stale")
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()
	command := "latest"
	if len(args) > 0 {
		command = strings.ToLower(args[0])
	}

	runs, err := discoverRuns(*root, *staleAfter, time.Now())
	if err != nil {
		fatal(err)
	}
	switch command {
	case "list":
		printRuns(runs)
	case "latest", "monitor":
		run, ok := selectLatest(runs)
		if !ok {
			fatal(fmt.Errorf("no backtest progress files found under %s", *root))
		}
		fmt.Printf("Monitoring %s (%s)\n", run.Dir, run.State)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := monitor(ctx, run, max(10, *width), os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unknown command %q (use latest or list)", command))
	}
}

func discoverRuns(root string, staleAfter time.Duration, now time.Time) ([]runInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	runs := make([]runInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		progressLogPath := filepath.Join(dir, backtestProgressLog)
		initLogPath := filepath.Join(dir, initializationLog)
		resultPath := filepath.Join(dir, "backtest.json")
		progressInfo, progressErr := os.Stat(progressLogPath)
		initInfo, initErr := os.Stat(initLogPath)
		resultInfo, resultErr := os.Stat(resultPath)
		if progressErr != nil && !errors.Is(progressErr, os.ErrNotExist) {
			return nil, progressErr
		}
		if initErr != nil && !errors.Is(initErr, os.ErrNotExist) {
			return nil, initErr
		}
		if resultErr != nil && !errors.Is(resultErr, os.ErrNotExist) {
			return nil, resultErr
		}
		if errors.Is(progressErr, os.ErrNotExist) && errors.Is(initErr, os.ErrNotExist) && errors.Is(resultErr, os.ErrNotExist) {
			continue
		}
		// backtest_init.log exists from the first ingestion step and continues to
		// receive the later engine telemetry. Prefer it so `latest` can attach
		// before backtest.json.raw is created, without needing to switch files.
		logPath := progressLogPath
		logInfo := progressInfo
		logErr := progressErr
		if initErr == nil {
			logPath = initLogPath
			logInfo = initInfo
			logErr = nil
		}
		state := "completed"
		var last *progressSample
		modified := time.Time{}
		if resultErr == nil {
			modified = resultInfo.ModTime()
		}
		if logErr == nil {
			modified = logInfo.ModTime()
			state, last, err = inspectLog(logPath, modified)
			if err != nil {
				return nil, err
			}
		}
		if resultErr == nil && resultInfo.Size() > 0 {
			state = "completed"
			if resultInfo.ModTime().After(modified) {
				modified = resultInfo.ModTime()
			}
		} else if state == "running" && now.Sub(modified) > staleAfter {
			state = "stale"
		}
		runs = append(runs, runInfo{Dir: dir, Log: logPath, Modified: modified, State: state, Last: last, StartedAt: runStart(entry.Name(), modified)})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Modified.After(runs[j].Modified) })
	return runs, nil
}

func inspectLog(path string, modified time.Time) (string, *progressSample, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	state := "running"
	var last *progressSample
	scanner := bufio.NewScanner(bytes.NewReader(content))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if update, ok := parseProgress(line); ok {
			last = &progressSample{Update: update, At: modified}
		} else if update, ok := parseInitializationStatus(line); ok {
			last = &progressSample{Update: update, At: modified}
		}
		if terminalState(line) != "" {
			state = terminalState(line)
		}
	}
	return state, last, scanner.Err()
}

func selectLatest(runs []runInfo) (runInfo, bool) {
	for _, run := range runs {
		if run.State == "running" {
			return run, true
		}
	}
	if len(runs) == 0 {
		return runInfo{}, false
	}
	return runs[0], true
}

func printRuns(runs []runInfo) {
	if len(runs) == 0 {
		fmt.Println("No backtest runs found.")
		return
	}
	fmt.Printf("%-10s %-8s %-20s %s\n", "STATE", "PROGRESS", "UPDATED", "RUN")
	for _, run := range runs {
		progress := "-"
		if run.Last != nil {
			progress = fmt.Sprintf("%6.2f%%", clamp(run.Last.Update.Fraction)*100)
		}
		fmt.Printf("%-10s %-8s %-20s %s\n", run.State, progress, run.Modified.Local().Format("2006-01-02 15:04:05"), run.Dir)
	}
}

func monitor(ctx context.Context, run runInfo, width int, out io.Writer) error {
	if run.State == "completed" {
		fmt.Fprintf(out, "%s: completed (%s)\n", run.Dir, filepath.Join(run.Dir, "backtest.json"))
		return nil
	}
	file, err := os.Open(run.Log)
	if err != nil {
		return err
	}
	defer file.Close()

	state := monitorState{}
	// Existing telemetry establishes only the baseline. ETA intentionally stays
	// unavailable until this monitor observes a newer update in real time.
	if err := consume(file, &state, run.Modified, false); err != nil {
		return err
	}
	if state.last == nil && run.Last != nil {
		state.last = run.Last
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	render := func(now time.Time) {
		fmt.Fprintf(out, "\r\033[2K%s", renderLine(state, width, now))
	}
	render(time.Now())
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out)
			return ctx.Err()
		case now := <-ticker.C:
			if err := consume(file, &state, now, true); err != nil {
				fmt.Fprintln(out)
				return err
			}
			if result, err := os.Stat(filepath.Join(run.Dir, "backtest.json")); err == nil && result.Size() > 0 {
				state.terminal = "completed"
			}
			render(now)
			if state.terminal != "" {
				fmt.Fprintln(out)
				return nil
			}
		}
	}
}

func consume(file *os.File, state *monitorState, observedAt time.Time, estimate bool) error {
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		update, ok := parseProgress(line)
		if !ok {
			update, ok = parseInitializationStatus(line)
		}
		if ok {
			sample := &progressSample{Update: update, At: observedAt}
			if estimate && state.last != nil && sameTrack(state.last.Update, update) && update.Fraction > state.last.Update.Fraction {
				delta := observedAt.Sub(state.last.At).Seconds()
				if delta > 0 {
					// Use only progress observed by this monitor. Do not infer a rate
					// from elapsed_ms or historical lines read at startup.
					state.rate = (update.Fraction - state.last.Update.Fraction) / delta
				}
			} else if state.last != nil && !sameTrack(state.last.Update, update) {
				state.rate = 0
			}
			state.previous = state.last
			state.last = sample
		}
		if terminal := terminalState(line); terminal != "" {
			state.terminal = terminal
			state.terminalAt = observedAt
		}
	}
	return scanner.Err()
}

func parseInitializationStatus(line string) (progressUpdate, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "[") {
		return progressUpdate{}, false
	}
	separator := strings.Index(line, "] ")
	if separator <= 1 || separator+2 >= len(line) {
		return progressUpdate{}, false
	}
	if _, err := time.Parse(time.RFC3339, line[1:separator]); err != nil {
		return progressUpdate{}, false
	}
	message := strings.TrimSpace(line[separator+2:])
	if message == "" {
		return progressUpdate{}, false
	}
	return progressUpdate{Phase: "initialization", Message: message}, true
}

func parseProgress(line string) (progressUpdate, bool) {
	index := strings.Index(line, progressPrefix)
	if index < 0 {
		return progressUpdate{}, false
	}
	payload := strings.TrimSpace(line[index+len(progressPrefix):])
	var update progressUpdate
	if json.Unmarshal([]byte(payload), &update) != nil || strings.TrimSpace(update.Phase) == "" {
		return progressUpdate{}, false
	}
	update.Fraction = clamp(update.Fraction)
	return update, true
}

func terminalState(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "success: manifest-backed backtest initialized"):
		return "completed"
	case strings.Contains(lower, "backtest failed:"), strings.Contains(lower, "failed (exit="):
		return "failed"
	default:
		return ""
	}
}

func renderLine(state monitorState, width int, now time.Time) string {
	if state.last == nil {
		return "[" + strings.Repeat(".", width) + "] waiting for progress telemetry | ETA n/a"
	}
	update := state.last.Update
	fraction := clamp(update.Fraction)
	filled := int(fraction * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat(".", width-filled)
	track := update.Phase
	if update.Lane != "" {
		track += "/" + update.Lane
	}
	position := update.Message
	if update.BarTotal > 0 {
		position = fmt.Sprintf("bars %d/%d", update.BarIndex, update.BarTotal)
	} else if update.WindowTotal > 0 {
		position = fmt.Sprintf("windows %d/%d", update.WindowIndex, update.WindowTotal)
	}
	eta := "n/a"
	if state.rate > 0 && fraction < 1 {
		remaining := time.Duration((1-fraction)/state.rate)*time.Second - now.Sub(state.last.At)
		if remaining > 0 {
			eta = formatDuration(remaining)
		} else {
			eta = "recalculating"
		}
	} else if fraction >= 1 {
		eta = "0s"
	}
	status := state.terminal
	if status == "" {
		status = "running"
	}
	return fmt.Sprintf("[%s] %6.2f%% | %-20s | %-18s | ETA %-10s | %s", bar, fraction*100, track, position, eta, status)
}

func sameTrack(a, b progressUpdate) bool {
	return a.Phase == b.Phase && a.Lane == b.Lane
}

func runStart(name string, fallback time.Time) time.Time {
	for _, prefix := range []string{"balanced-full-", "balanced-"} {
		if strings.HasPrefix(name, prefix) {
			if parsed, err := time.Parse("20060102T150405Z", strings.TrimPrefix(name, prefix)); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func formatDuration(value time.Duration) string {
	value = value.Round(time.Second)
	if value < 0 {
		value = 0
	}
	days := value / (24 * time.Hour)
	value %= 24 * time.Hour
	hours := value / time.Hour
	value %= time.Hour
	minutes := value / time.Minute
	seconds := (value % time.Minute) / time.Second
	if days > 0 {
		return fmt.Sprintf("%dd%02dh%02dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "backtest-monitor:", err)
	os.Exit(1)
}
