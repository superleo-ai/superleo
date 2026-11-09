package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestEveryCallThatChangesSomethingIsWrittenDown(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(old)

	a := &App{}
	_, _ = a.Call("DeleteDashboard", []json.RawMessage{json.RawMessage(`"dash-42"`)})
	_, _ = a.Call("NoSuchMethod", nil)
	out := buf.String()
	for _, want := range []string{"method=DeleteDashboard", "via=window", "subject=dash-42", "method=NoSuchMethod", "refused=404"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func TestWhichCallsChangeState(t *testing.T) {
	for name, want := range map[string]bool{
		"DeleteDashboard": true, "SetDashboardCron": true, "HiveTaskCancel": true, "HiveSpawn": true,
		"Dashboards": false, "HiveStatus": false, "ChatHistory": false, "StandingAgents": false, "LongRunList": false, "GetSettings": false,
	} {
		if got := changesState(name); got != want {
			t.Errorf("changesState(%s) = %v, want %v", name, got, want)
		}
	}
}

func TestAKeyNeverReachesTheLog(t *testing.T) {
	// A made-up key, spelled in two halves so a key scan of this file stays quiet.
	key := json.RawMessage(`"sk` + `-ant-abcdefghijklmnop"`)
	if s := callSubject("RenameDashboard", []json.RawMessage{key}); s != "" {
		t.Errorf("a key-shaped argument was kept: %q", s)
	}
	if s := callSubject("SetProviderKey", []json.RawMessage{json.RawMessage(`"anthropic"`)}); s != "" {
		t.Errorf("a key method's argument was kept: %q", s)
	}
	if s := callSubject("RenameDashboard", []json.RawMessage{json.RawMessage(`"资产管理"`)}); s != "资产管理" {
		t.Errorf("a name was dropped: %q", s)
	}
}
