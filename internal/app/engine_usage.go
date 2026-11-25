package app

// What the queen's engine costs the person, read from the place that knows.
//
// With the person's own API key, SuperLeo makes the model calls itself and its
// meter counts the tokens. With a subscription CLI (Codex, Claude Code) the
// CLI makes them and the meter sees nothing, so the panel shows the plan's own
// figures instead: Codex writes its usage limits into every session log;
// Claude Code logs token counts only (its plan limits are not on disk).
// Only this machine's logs are read.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// UsageWindow is one usage limit of a plan.
type UsageWindow struct {
	Label   string  `json:"label"` // "5h", "week"
	Percent float64 `json:"percent"`
	// ResetsAt is unix seconds; 0 when the window has already rolled over.
	ResetsAt int64 `json:"resetsAt,omitempty"`
}

// EngineUsage is what the panel shows for the queen's engine.
type EngineUsage struct {
	// Mode is "key" (SuperLeo's own model calls, counted by the meter) or
	// "subscription" (a CLI on the person's plan).
	Mode string `json:"mode"`
	CLI  string `json:"cli,omitempty"` // "codex", "claude"
	// Remote: the engine runs on another machine, whose logs are not read here.
	Remote  bool          `json:"remote,omitempty"`
	Windows []UsageWindow `json:"windows,omitempty"`
	// Tokens is today's count from the CLI's own logs, where it keeps no limits.
	Tokens int `json:"tokens,omitempty"`
}

// EngineUsage is the usage figures for whatever thinks for the queen now.
func (a *App) EngineUsage() EngineUsage {
	a.mu.Lock()
	var h backend.HiveSettings
	if a.settings != nil {
		h = a.settings.Hive
	}
	a.mu.Unlock()
	return engineUsageFor(h, a.queenEngineNow(), time.Now())
}

func engineUsageFor(h backend.HiveSettings, now string, at time.Time) EngineUsage {
	engine := now
	if engine == "" {
		if on := h.QueenEnginesOn(); len(on) > 0 {
			engine = on[0]
		}
	}
	if engine == "" || engine == backend.QueenEngineSuperLeo || engine == queenNowSuperLeo {
		return EngineUsage{Mode: "key"}
	}
	cli, _, remote := splitRemoteCLI(engine)
	if !remote {
		cli = engine
	}
	if cli != "codex" && cli != "claude" {
		return EngineUsage{Mode: "key"}
	}
	out := EngineUsage{Mode: "subscription", CLI: cli, Remote: remote}
	if remote {
		return out
	}
	switch cli {
	case "codex":
		out.Windows = codexLimits(at)
	case "claude":
		out.Tokens = claudeTokensToday(at)
	}
	return out
}

// usageCache keeps a read for a short while: the panel asks every few seconds
// and the logs can be large.
var usageCache struct {
	sync.Mutex
	at   time.Time
	key  string
	data any
}

func cachedUsage[T any](key string, fn func() T) T {
	c := &usageCache
	c.Lock()
	defer c.Unlock()
	if v, ok := c.data.(T); ok && c.key == key && time.Since(c.at) < 15*time.Second {
		return v
	}
	v := fn()
	c.at, c.key, c.data = time.Now(), key, v
	return v
}

// tailLines calls fn for each whole line in the last part of a file.
func tailLines(path string, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	partial := false
	if st, err := f.Stat(); err == nil && st.Size() > sessionLogTail {
		if _, err := f.Seek(st.Size()-sessionLogTail, io.SeekStart); err == nil {
			partial = true
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		if partial {
			partial = false
			continue
		}
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			fn(line)
		}
	}
}

// newestFirst lists files matching a glob, most recently written first.
func newestFirst(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	sort.Slice(m, func(i, j int) bool { return modTime(m[i]).After(modTime(m[j])) })
	return m
}

// codexLimits reads the plan's usage limits from the newest codex session log
// that carries them. A window whose reset time has passed reads 0%: the log's
// figure is from before it rolled over.
func codexLimits(now time.Time) []UsageWindow {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return cachedUsage("codex"+home, func() []UsageWindow {
		files := newestFirst(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl"))
		if len(files) > 8 {
			files = files[:8]
		}
		for _, f := range files {
			var last []UsageWindow
			tailLines(f, func(line []byte) {
				if !bytes.Contains(line, []byte(`"rate_limits"`)) {
					return
				}
				if w := codexWindows(line, now); len(w) > 0 {
					last = w
				}
			})
			if len(last) > 0 {
				return last
			}
		}
		return nil
	})
}

// codexWindows reads the limits out of one token_count line.
func codexWindows(line []byte, now time.Time) []UsageWindow {
	var m struct {
		Payload struct {
			Type       string `json:"type"`
			RateLimits struct {
				Primary   *codexWindow `json:"primary"`
				Secondary *codexWindow `json:"secondary"`
			} `json:"rate_limits"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &m) != nil || m.Payload.Type != "token_count" {
		return nil
	}
	var out []UsageWindow
	for _, w := range []*codexWindow{m.Payload.RateLimits.Primary, m.Payload.RateLimits.Secondary} {
		if w == nil {
			continue
		}
		u := UsageWindow{Label: windowLabel(w.Minutes), Percent: w.Used, ResetsAt: w.ResetsAt}
		if w.ResetsAt > 0 && w.ResetsAt <= now.Unix() {
			u.Percent, u.ResetsAt = 0, 0
		}
		out = append(out, u)
	}
	return out
}

type codexWindow struct {
	Used     float64 `json:"used_percent"`
	Minutes  int     `json:"window_minutes"`
	ResetsAt int64   `json:"resets_at"`
}

func windowLabel(minutes int) string {
	switch {
	case minutes >= 10080:
		return "week"
	case minutes >= 60 && minutes%60 == 0:
		return strconv.Itoa(minutes/60) + "h"
	default:
		return strconv.Itoa(minutes) + "m"
	}
}

// claudeTokensToday adds up what Claude Code's logs say today's turns used
// (input, output and cache writes; cache reads are re-reads, not new work).
// A message is logged once per content block with a growing count, so the
// last line of each message id wins. A day's logs run to tens of megabytes,
// so each file is read once and then only from where the last read stopped.
func claudeTokensToday(now time.Time) int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	y, mo, d := now.Date()
	start := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
	c := &claudeDay
	c.Lock()
	defer c.Unlock()
	if key := home + "|" + start.Format("2006-01-02"); c.key != key {
		c.key, c.files = key, map[string]*claudeFile{}
	}
	total := 0
	for _, path := range newestFirst(filepath.Join(home, ".claude", "projects", "*", "*.jsonl")) {
		if modTime(path).Before(start) {
			break
		}
		cf := c.files[path]
		if cf == nil {
			cf = &claudeFile{msgs: map[string]int{}}
			c.files[path] = cf
		}
		cf.readFrom(path, start)
		for _, n := range cf.msgs {
			total += n
		}
	}
	return total
}

// claudeDay is what has been read of today's Claude Code logs.
var claudeDay struct {
	sync.Mutex
	key   string
	files map[string]*claudeFile
}

type claudeFile struct {
	off  int64
	msgs map[string]int
}

// readFrom reads the lines added since the last read. A line still being
// written (no newline yet) is left for next time.
func (cf *claudeFile) readFrom(path string, start time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() < cf.off {
		// Rewritten or truncated: start over.
		cf.off, cf.msgs = 0, map[string]int{}
	}
	if _, err := f.Seek(cf.off, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		cf.off += int64(len(line))
		if !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}
		var m struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				ID    string `json:"id"`
				Usage struct {
					In     int `json:"input_tokens"`
					Out    int `json:"output_tokens"`
					Create int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &m) != nil || m.Type != "assistant" || m.Message.ID == "" {
			continue
		}
		if lineTime(m.Timestamp).Before(start) {
			continue
		}
		cf.msgs[m.Message.ID] = m.Message.Usage.In + m.Message.Usage.Out + m.Message.Usage.Create
	}
}
