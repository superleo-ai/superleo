package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Every call into the App — from the desktop window (through Call), a browser
// tab (/api/rpc) or core over the agent link — goes through callMethod, and
// callMethod writes what it did here. Before, each feature logged for itself
// or not at all, so a dashboard could be deleted, a setting changed or a run
// cancelled and desktop.log had nothing to say about it.
//
// What is logged:
//   - a call that changes something (its name has one of changeWords) at Info,
//     with its first argument when that is a short id or name;
//   - any call that fails, at Warn;
//   - any call slower than slowCall, at Info;
//   - everything else (the window's polling) at Debug.

const slowCall = 3 * time.Second

// changeWords are the words a method's name uses when it changes state:
// SetDashboardCron, HiveTaskCancel, DeleteDashboard, HiveSpawn.
var changeWords = map[string]bool{
	"Set": true, "Save": true, "Delete": true, "Remove": true, "Add": true, "Create": true,
	"Rename": true, "Update": true, "Cancel": true, "Install": true, "Uninstall": true,
	"Import": true, "Start": true, "Stop": true, "Restart": true, "Send": true, "Run": true,
	"Reply": true, "Resolve": true, "Retire": true, "Spawn": true, "Join": true, "Leave": true,
	"Pair": true, "Unpair": true, "Revoke": true, "Clear": true, "Reset": true, "Enable": true,
	"Disable": true, "Pause": true, "Resume": true, "Apply": true, "Move": true, "Approve": true,
	"Deny": true, "Restore": true, "Refresh": true, "Upload": true, "Write": true, "Accept": true,
	"Dismiss": true, "Kill": true, "Migrate": true, "Reconnect": true, "Take": true, "Forget": true,
	"Archive": true, "Pin": true, "Unpin": true, "Mark": true, "Complete": true, "Edit": true,
	"Link": true, "Unlink": true, "Wake": true, "Trigger": true, "Promote": true, "Drop": true,
}

// camelWords splits HiveTaskCancel into Hive, Task, Cancel.
func camelWords(name string) []string {
	var out []string
	start := 0
	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			out = append(out, name[start:i])
			start = i
		}
	}
	return append(out, name[start:])
}

// readWords end or start a name that only reads: LongRunList, GetSettings,
// HiveStatus — whatever other words they hold.
var readWords = map[string]bool{"List": true, "Get": true, "Status": true, "History": true, "Info": true}

func changesState(name string) bool {
	ws := camelWords(name)
	if readWords[ws[0]] || readWords[ws[len(ws)-1]] {
		return false
	}
	for _, w := range ws {
		if changeWords[w] {
			return true
		}
	}
	return false
}

// secretish is a method whose arguments may carry a key or token: none of its
// arguments are written down.
func secretish(name string) bool {
	n := strings.ToLower(name)
	for _, s := range []string{"key", "token", "secret", "password", "auth", "credential", "login", "settings", "env"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// idLike is what a first argument looks like when it names the thing a call
// acts on: an id, a short name, a number. Anything longer or odder is left out.
var idLike = regexp.MustCompile(`^[\p{L}\p{N} ._:/@#-]{1,64}$`)

var keyShape = regexp.MustCompile(`(?i)(sk[-]|ghp[_]|cfut[_]|akia|bearer|[-]{5}begin)`)

// callSubject is the first argument when it is a short id or name, for the
// log line; "" otherwise.
func callSubject(name string, raw []json.RawMessage) string {
	if len(raw) == 0 || secretish(name) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw[0], &s); err != nil {
		var n json.Number
		if err := json.Unmarshal(raw[0], &n); err != nil {
			return ""
		}
		return n.String()
	}
	if !idLike.MatchString(s) || keyShape.MatchString(s) {
		return ""
	}
	return s
}

func auditCall(via, name string, raw []json.RawMessage, took time.Duration, err error) {
	attrs := []any{"method", name, "via", via, "took", took.Round(time.Millisecond)}
	if s := callSubject(name, raw); s != "" {
		attrs = append(attrs, "subject", s)
	}
	attrs = append(attrs, "args", len(raw))
	switch {
	case err != nil:
		var ce *callError
		if errors.As(err, &ce) {
			attrs = append(attrs, "refused", ce.status)
		}
		msg := err.Error()
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		slog.Warn("call: failed", append(attrs, "error", msg)...)
	case changesState(name):
		slog.Info("call", attrs...)
	case took >= slowCall:
		slog.Info("call: slow", attrs...)
	default:
		slog.Debug("call", attrs...)
	}
}
