package app

import "testing"

func TestLoopCallEventsStayOffScreens(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    bool
	}{
		{"chat:event", map[string]any{"type": "tool_call", "tool": "task_complete"}, true},
		{"chat:event", map[string]any{"type": "tool_result", "tool": "task_blocked"}, true},
		{"chat:event", map[string]any{"type": "tool_call", "tool": "web_search"}, false},
		{"chat:event", map[string]any{"type": "partial", "tool": "task_complete"}, false},
		{"hive:event", map[string]any{"type": "tool_call", "tool": "task_complete"}, false},
	}
	for _, c := range cases {
		if got := loopCallEvent(c.name, c.payload); got != c.want {
			t.Errorf("loopCallEvent(%s, %v) = %v, want %v", c.name, c.payload, got, c.want)
		}
	}
}
