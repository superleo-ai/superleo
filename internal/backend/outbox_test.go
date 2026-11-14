package backend

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testOutbox is an outbox on a clock the test moves; its timers do not fire
// by themselves, the test calls fire.
func testOutbox(t *testing.T, at time.Time, quiet string) (o *Outbox, clock *time.Time, fire func()) {
	t.Helper()
	now := at
	o = newOutbox(filepath.Join(t.TempDir(), OutboxFile))
	o.now = func() time.Time { return now }
	o.quiet = func() string { return quiet }
	var pending func()
	o.after = func(_ time.Duration, f func()) *time.Timer { pending = f; return time.NewTimer(time.Hour) }
	return o, &now, func() {
		if f := pending; f != nil {
			pending = nil
			f()
		}
	}
}

func noticesWith(o *Outbox) (*Notices, func(string) []Notice) {
	var mu sync.Mutex
	got := map[string][]Notice{}
	p := NewNotices(nil)
	for _, name := range []string{"ui", "inbox", "webhook", "apns"} {
		p.Subscribe(name, func(_ context.Context, n Notice) {
			mu.Lock()
			got[name] = append(got[name], n)
			mu.Unlock()
		})
	}
	p.PaceWith(o)
	return p, func(name string) []Notice { mu.Lock(); defer mu.Unlock(); return append([]Notice(nil), got[name]...) }
}

func at(h, m int) time.Time { return time.Date(2026, 11, 14, h, m, 0, 0, time.Local) }

func TestQuietHoursAreRead(t *testing.T) {
	for v, want := range map[string][3]int{"": {23 * 60, 7 * 60, 1}, "off": {0, 0, 0}, "22:30-06:15": {22*60 + 30, 6*60 + 15, 1}, "08:00-08:00": {480, 480, 0}} {
		from, to, on, err := ParseQuietHours(v)
		if err != nil || from != want[0] || to != want[1] || on != (want[2] == 1) {
			t.Fatalf("%q: %d %d %v %v", v, from, to, on, err)
		}
	}
	for _, bad := range []string{"23-7", "25:00-07:00", "night"} {
		if _, _, _, err := ParseQuietHours(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if e := quietEnd(at(23, 30), 23*60, 7*60); e.Day() != 15 || e.Hour() != 7 {
		t.Fatalf("before midnight ends %v", e)
	}
	if e := quietEnd(at(3, 0), 23*60, 7*60); e.Day() != 14 || e.Hour() != 7 {
		t.Fatalf("after midnight ends %v", e)
	}
	if e := quietEnd(at(12, 0), 23*60, 7*60); !e.IsZero() {
		t.Fatalf("noon is quiet until %v", e)
	}
}

func TestANoticeNobodyTimedWaitsForTheMorningAndGoesOutAsOne(t *testing.T) {
	o, clock, fire := testOutbox(t, at(3, 0), "")
	p, got := noticesWith(o)
	p.Raise(context.Background(), Notice{Message: "step one done", Push: true})
	p.Raise(context.Background(), Notice{Title: "Build", Message: "step two done", Push: true})

	// The surfaces in front of the person have both; the phone has neither.
	if len(got("ui")) != 2 || len(got("inbox")) != 2 || len(got("apns")) != 0 || len(got("webhook")) != 0 {
		t.Fatalf("ui %d inbox %d apns %d webhook %d", len(got("ui")), len(got("inbox")), len(got("apns")), len(got("webhook")))
	}
	// A timer that fires early changes nothing.
	*clock = at(5, 0)
	fire()
	if len(got("apns")) != 0 {
		t.Fatal("sent in the quiet hours")
	}
	*clock = at(7, 0)
	fire()
	a := got("apns")
	if len(a) != 1 || len(got("webhook")) != 1 || len(got("ui")) != 2 {
		t.Fatalf("apns %d webhook %d ui %d", len(a), len(got("webhook")), len(got("ui")))
	}
	if !strings.Contains(a[0].Title, "(+1)") || !strings.Contains(a[0].Message, "step one done") || !strings.Contains(a[0].Message, "Build: step two done") || !a[0].Push {
		t.Fatalf("%+v", a[0])
	}
}

func TestWhatWasTimedOrIsUrgentIsNotHeld(t *testing.T) {
	o, _, _ := testOutbox(t, at(3, 0), "")
	p, got := noticesWith(o)
	for _, n := range []Notice{
		{Message: "wake up", Kind: KindReminder, Push: true},
		{Message: "disk full", Kind: KindAlert, Level: LevelWarn, Push: true},
		{Message: "Run failed: x", Level: LevelError, Push: true},
		{Message: "morning brief", Kind: KindBrief, Push: true},
		{Message: "the 3am job's answer", Source: "every night at 3", Push: true},
		{Message: "reminder run", Event: ChannelEventReminder, Push: true},
	} {
		p.Raise(context.Background(), n)
	}
	if len(got("apns")) != 6 {
		t.Fatalf("%d of 6 reached the phone", len(got("apns")))
	}
	// Not pushed is not the outbox's business either.
	p.Raise(context.Background(), Notice{Message: "saved"})
	if len(got("apns")) != 7 || len(got("ui")) != 7 {
		t.Fatalf("apns %d ui %d", len(got("apns")), len(got("ui")))
	}
}

func TestABurstInTheDayGoesOutTogetherAMinuteLater(t *testing.T) {
	o, clock, fire := testOutbox(t, at(14, 0), "")
	p, got := noticesWith(o)
	p.Raise(context.Background(), Notice{Message: "one", Push: true})
	if len(got("apns")) != 1 {
		t.Fatal("the first of the day waited")
	}
	p.Raise(context.Background(), Notice{Message: "two", Push: true})
	p.Raise(context.Background(), Notice{Message: "three", Key: "k", Push: true})
	p.Raise(context.Background(), Notice{Message: "three again", Key: "k", Push: true})
	if len(got("apns")) != 1 {
		t.Fatalf("%d sent inside the minute", len(got("apns")))
	}
	*clock = clock.Add(outboxGap)
	fire()
	a := got("apns")
	if len(a) != 2 || !strings.Contains(a[1].Message, "two") || !strings.Contains(a[1].Message, "three again") || strings.Count(a[1].Message, "three") != 1 {
		t.Fatalf("%+v", a)
	}
	// A quiet stretch later, the next one goes at once.
	*clock = clock.Add(10 * time.Minute)
	p.Raise(context.Background(), Notice{Message: "four", Push: true})
	if len(got("apns")) != 3 {
		t.Fatal("a notice long after the last one waited")
	}
}

func TestHeldNoticesSurviveARestartAndOffHoldsNothing(t *testing.T) {
	o, _, _ := testOutbox(t, at(3, 0), "")
	p, _ := noticesWith(o)
	p.Raise(context.Background(), Notice{Title: "from outside", Message: "hello", Source: "openclaw", External: true, Push: true})

	// The next process, in the morning: attaching sends what was owed.
	again := newOutbox(o.path)
	again.now = func() time.Time { return at(8, 0) }
	again.quiet = func() string { return "" }
	_, got := noticesWith(again)
	a := got("apns")
	if len(a) != 1 || a[0].Title != "from outside" || !a[0].Push || !a[0].External {
		t.Fatalf("%+v", a)
	}

	off, _, _ := testOutbox(t, at(3, 0), "off")
	p, got = noticesWith(off)
	p.Raise(context.Background(), Notice{Message: "now", Push: true})
	if len(got("apns")) != 1 {
		t.Fatal("held with quiet hours off")
	}
}
