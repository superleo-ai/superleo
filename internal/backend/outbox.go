package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The outbox: what leaves the machine for a person who is not at it — the
// webhook, the phones — is paced.
//
// A notice used to go out the moment it was raised, so an agent that worked
// through the night said so through the night, and a burst of five interim
// messages was five buzzes in a pocket. Now a notice nobody timed waits: for
// the morning when it is raised in the quiet hours, and for the last one sent
// to be a minute old otherwise. What waited together goes out as one.
//
// Only what nobody timed waits. A reminder, an alert, a failure, a brief and
// a scheduled run were either set for that minute by the person or are worth
// waking them for, and go out as before. The surfaces in front of the person
// (the window, the notification centre, the desktop banner) are never paced:
// someone looking at the screen is not interrupted by it.
//
// Held notices are kept in a file, so a restart in the night does not lose
// what the morning was owed.

// OutboxFile is where held notices are kept, under the data directory.
const OutboxFile = "outbox.json"

// DefaultQuietHours is when a notice nobody timed waits for the morning.
const DefaultQuietHours = "23:00-07:00"

// QuietHoursOff turns the quiet hours off.
const QuietHoursOff = "off"

// outboxGap is how old the last paced notice must be before the next goes
// out; the ones raised sooner go together when it is.
const outboxGap = time.Minute

// awaySurfaces are the subscribers that leave the machine.
var awaySurfaces = map[string]bool{"webhook": true, "apns": true}

// ParseQuietHours reads "HH:MM-HH:MM" as minutes of the day. on is false for
// "off"; an empty value is the default.
func ParseQuietHours(v string) (from, to int, on bool, err error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		v = DefaultQuietHours
	}
	if v == QuietHoursOff {
		return 0, 0, false, nil
	}
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		return 0, 0, false, fmt.Errorf(`quiet_hours is "HH:MM-HH:MM" or "off", not %q`, v)
	}
	if from, err = clockMinutes(a); err != nil {
		return 0, 0, false, err
	}
	if to, err = clockMinutes(b); err != nil {
		return 0, 0, false, err
	}
	return from, to, from != to, nil
}

func clockMinutes(v string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("quiet_hours: %q is not a time of day (HH:MM)", strings.TrimSpace(v))
	}
	return hh*60 + mm, nil
}

// quietEnd is when the quiet hours t falls in are over, or zero when t is
// outside them.
func quietEnd(t time.Time, from, to int) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	at := func(d time.Time, min int) time.Time { return d.Add(time.Duration(min) * time.Minute) }
	now := t.Hour()*60 + t.Minute()
	switch {
	case from < to && now >= from && now < to:
		return at(day, to)
	case from > to && now >= from: // before midnight, ends tomorrow
		return at(day.AddDate(0, 0, 1), to)
	case from > to && now < to: // after midnight
		return at(day, to)
	}
	return time.Time{}
}

// Paced says whether a notice waits in the outbox: one that is pushed, that
// nobody timed and that is not worth waking somebody for.
func Paced(n Notice) bool {
	if !n.Push || n.Event != "" {
		return false
	}
	if n.Level != LevelInfo && n.Level != LevelSuccess && n.Level != "" {
		return false
	}
	if n.Kind != "" && n.Kind != KindInfo {
		return false
	}
	// A source is the schedule a run was timed by, except on a notice from
	// outside, where it is only who sent it.
	return n.Source == "" || n.External
}

// Outbox holds the paced notices until they may go.
type Outbox struct {
	mu      sync.Mutex
	path    string
	held    []Notice
	last    time.Time // when a paced notice last went out
	timer   *time.Timer
	deliver func(context.Context, Notice)
	quiet   func() string
	now     func() time.Time
	after   func(time.Duration, func()) *time.Timer
}

var (
	outboxMu  sync.Mutex
	outboxes  = map[string]*Outbox{}
	// quietFrom is the quiet hours in force: SUPERLEO_QUIET_HOURS when set
	// (a hive's settings file is rewritten at every start), else the setting.
	quietFrom = func() string {
		if env := strings.TrimSpace(os.Getenv("SUPERLEO_QUIET_HOURS")); env != "" {
			return env
		}
		s, err := LoadSettings()
		if err != nil || s == nil {
			return ""
		}
		return s.QuietHours
	}
)

// OpenOutbox is this data directory's outbox: one for the process, so a
// Service rebuilt by a settings save goes on with what the last one held.
func OpenOutbox() *Outbox {
	path := filepath.Join(DataDir(), "data", OutboxFile)
	outboxMu.Lock()
	defer outboxMu.Unlock()
	if o := outboxes[path]; o != nil {
		return o
	}
	o := newOutbox(path)
	outboxes[path] = o
	return o
}

func newOutbox(path string) *Outbox {
	o := &Outbox{path: path, quiet: quietFrom, now: time.Now, after: time.AfterFunc}
	if raw, err := os.ReadFile(path); err == nil {
		var in []heldNotice
		if json.Unmarshal(raw, &in) == nil {
			for _, h := range in {
				h.Notice.Push, h.Notice.External = h.Push, h.External
				o.held = append(o.held, h.Notice)
			}
		}
	}
	return o
}

// Attach says how a notice that waited goes out, and sends what a start
// found already due. The latest caller wins: it is the Notices of the Service
// that is running now.
func (o *Outbox) Attach(deliver func(context.Context, Notice)) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.deliver = deliver
	pending := len(o.held) > 0
	o.mu.Unlock()
	if pending {
		o.flush()
	}
}

// Take is asked about every notice bound for an away surface. True means the
// outbox has it and it goes out later; false means send it now.
func (o *Outbox) Take(n Notice) bool {
	if o == nil || !Paced(n) {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	due := o.dueLocked(now)
	if len(o.held) == 0 && !due.After(now) {
		o.last = now
		return false
	}
	// The same thing said again replaces what was waiting.
	if n.Key != "" {
		for i := range o.held {
			if o.held[i].Key == n.Key {
				o.held[i] = n
				o.saveLocked()
				return true
			}
		}
	}
	o.held = append(o.held, n)
	o.saveLocked()
	o.armLocked(due.Sub(now))
	return true
}

// dueLocked is when a paced notice may next go out.
func (o *Outbox) dueLocked(now time.Time) time.Time {
	due := now
	if from, to, on, err := ParseQuietHours(o.quiet()); err == nil && on {
		if end := quietEnd(now, from, to); !end.IsZero() {
			due = end
		}
	}
	if next := o.last.Add(outboxGap); !o.last.IsZero() && next.After(due) {
		due = next
	}
	return due
}

func (o *Outbox) armLocked(wait time.Duration) {
	if o.timer != nil {
		return
	}
	if wait < 0 {
		wait = 0
	}
	o.timer = o.after(wait, o.flush)
}

// flush sends what waited, as one notice when there were several, unless the
// quiet hours began meanwhile or nobody is attached to send it.
func (o *Outbox) flush() {
	o.mu.Lock()
	o.timer = nil
	now := o.now()
	if len(o.held) == 0 || o.deliver == nil {
		o.mu.Unlock()
		return
	}
	if due := o.dueLocked(now); due.After(now) {
		o.armLocked(due.Sub(now))
		o.mu.Unlock()
		return
	}
	out, deliver := digest(o.held), o.deliver
	o.held, o.last = nil, now
	o.saveLocked()
	o.mu.Unlock()
	deliver(context.Background(), out)
}

func (o *Outbox) saveLocked() {
	if o.path == "" {
		return
	}
	if len(o.held) == 0 {
		_ = os.Remove(o.path)
		return
	}
	out := make([]heldNotice, len(o.held))
	for i, n := range o.held {
		out[i] = heldNotice{Notice: n, Push: n.Push, External: n.External}
	}
	raw, err := json.Marshal(out)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(o.path), 0o700); err == nil {
			err = os.WriteFile(o.path, raw, 0o600)
		}
	}
	if err != nil {
		slog.Warn("outbox: held notices not saved", "error", err)
	}
}

// heldNotice is a Notice as the outbox file keeps it: with the fields a
// Notice leaves out of its own JSON, which decide where it goes when sent.
type heldNotice struct {
	Notice
	Push     bool `json:"push"`
	External bool `json:"external"`
}

// digest is the notices that waited together as one: the first one's title
// with how many more there are, and each one's words on a line of its own.
func digest(held []Notice) Notice {
	if len(held) == 1 {
		return held[0]
	}
	first := held[0]
	head := first.Title
	if head == "" {
		head = firstLine(first.Message, 60)
	}
	var b strings.Builder
	external := true
	for _, n := range held {
		line := n.Message
		if n.Title != "" && n.Message != "" {
			line = n.Title + ": " + n.Message
		} else if n.Title != "" {
			line = n.Title
		}
		b.WriteString("• ")
		b.WriteString(firstLine(line, 300))
		b.WriteString("\n")
		external = external && n.External
	}
	return Notice{
		Level:   LevelInfo,
		Title:   fmt.Sprintf("%s (+%d)", head, len(held)-1),
		Message: strings.TrimSpace(b.String()),
		Push:    true,
		// To the webhook unless every one of them came from outside, whose
		// sender has its own way there.
		External: external,
		At:       held[len(held)-1].At,
	}
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
