package backend

import "testing"

// Trace lines are on unless AGENTGO_TRACE says no; unset or anything else
// leaves them on.
func TestTraceIsOnUnlessTurnedOff(t *testing.T) {
	for v, off := range map[string]bool{"": false, "1": false, "on": false, "0": true, "false": true, " OFF ": true, "no": true} {
		if traceOff(v) != off {
			t.Errorf("AGENTGO_TRACE=%q: off=%v", v, traceOff(v))
		}
	}
}
