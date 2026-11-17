package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

func phoneTestSettings() *backend.Settings {
	return &backend.Settings{
		LLMBaseURL:           "https://api.example.com/v1",
		LLMKey:               "llm-secret-value",
		LLMModel:             "m1",
		EmbedKey:             "",
		SharedMemoryToken:    "mem-secret-value",
		SharedMemoryEndpoint: "10.0.0.2:47821",
		MaxRounds:            30,
		Proxy:                backend.ProxySettings{Mode: "manual", URL: "http://bob:proxy-pass@127.0.0.1:7890"},
		Hive:                 backend.HiveSettings{JoinToken: "join-secret-value", PeerToken: "peer-secret-value"},
		RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: map[string]backend.RemoteAgent{
			"mini": {About: "the mini", Hosts: []string{"10.0.0.9"}, URL: "https://x:agent-pass@10.0.0.9", Token: "agent-secret-value"},
		}},
	}
}

// What the phone reads names no secret, only whether each is set.
func TestPhoneSettingsCarryNoSecret(t *testing.T) {
	v := phoneSettingsOf(*phoneTestSettings())
	raw, _ := json.Marshal(v)
	for _, secret := range []string{"llm-secret-value", "mem-secret-value", "proxy-pass", "join-secret-value", "peer-secret-value", "agent-secret-value", "agent-pass"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s is in what the phone reads: %s", secret, raw)
		}
	}
	if !v.Secrets["llm_key"] || v.Secrets["embed_key"] || !v.Secrets["shared_memory_token"] {
		t.Fatalf("secrets set: %v", v.Secrets)
	}
	if v.Proxy.Mode != "manual" || !strings.Contains(v.Proxy.URL, "127.0.0.1:7890") {
		t.Fatalf("proxy: %+v", v.Proxy)
	}
	if len(v.RemoteAgents.Agents) != 1 || v.RemoteAgents.Agents[0].Name != "mini" || v.RemoteAgents.Agents[0].Hosts[0] != "10.0.0.9" {
		t.Fatalf("agents: %+v", v.RemoteAgents)
	}
}

func TestPhonePatchTakesOnlyTheScreensFields(t *testing.T) {
	cur := *phoneTestSettings()
	for _, bad := range []map[string]any{
		{"hive": map[string]any{"join_token": "x"}},
		{"telegram_bot_token": "x"},
		{"apns": map[string]any{}},
		{"remote_agents": map[string]any{"agents": map[string]any{"evil": map[string]any{"url": "https://evil"}}}},
		{"remote_agents": map[string]any{"agents": map[string]any{"nobody": nil}}},
		{"proxy": map[string]any{"bypass": "x"}},
		{"hive": map[string]any{"role": "worker"}},
		{"llm_key": nil},
	} {
		if _, err := phonePatch(cur, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}

	got, err := phonePatch(cur, map[string]any{
		"max_rounds": 40.0,
		"llm_key":    "new-key",
		// Sent back as it was shown: the real address with its password stays.
		"proxy":         map[string]any{"mode": "manual", "url": redactURL(cur.Proxy.URL)},
		"remote_agents": map[string]any{"enabled": false, "agents": map[string]any{"mini": nil}},
		"hive":          map[string]any{"role": "queen", "standalone": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["max_rounds"] != 40.0 || got["llm_key"] != "new-key" {
		t.Fatalf("patch: %v", got)
	}
	if p := got["proxy"].(map[string]any); p["url"] != nil || p["mode"] != "manual" {
		t.Fatalf("proxy patch: %v", p)
	}
	next, err := mergeSettingsPatch(cur, got)
	if err != nil {
		t.Fatal(err)
	}
	if next.Proxy.URL != cur.Proxy.URL || next.MaxRounds != 40 || next.LLMKey != "new-key" || next.Hive.JoinToken != "join-secret-value" {
		t.Fatalf("merged: %+v", next)
	}
	if _, ok := next.RemoteAgents.Agents["mini"]; ok || next.RemoteAgents.Enabled {
		t.Fatalf("remote agents: %+v", next.RemoteAgents)
	}
	if next.Hive.Role != "queen" {
		t.Fatalf("role %q", next.Hive.Role)
	}

	worker := cur
	worker.Hive.Role = "worker"
	if _, err := phonePatch(worker, map[string]any{"hive": map[string]any{"role": ""}}); err == nil {
		t.Fatal("a worker was turned into something else from a phone")
	}
}

// Saved through the App, and read back the way the phone reads it.
func TestPatchPhoneSettingsSaves(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app := NewApp()
	app.settings = phoneTestSettings()
	v, err := app.PatchPhoneSettings(map[string]any{"embed_key": "e-key", "pii_redaction": true})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Secrets["embed_key"] || !v.PIIRedaction {
		t.Fatalf("view: %+v", v)
	}
	if s := app.GetSettings(); s.EmbedKey != "e-key" || s.LLMKey != "llm-secret-value" {
		t.Fatalf("saved: embed set %v, llm kept %v", s.EmbedKey == "e-key", s.LLMKey == "llm-secret-value")
	}
	if _, err := app.PatchPhoneSettings(map[string]any{"hive": map[string]any{"peer_token": "x"}}); err == nil {
		t.Fatal("hive.peer_token was taken")
	}
}

// A paired device is turned away from the whole file; the owner is not.
func TestDeviceCannotReadTheSettingsFile(t *testing.T) {
	app := NewApp()
	app.settings = phoneTestSettings()
	do := func(method, kind string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/rpc/"+method, strings.NewReader("[]"))
		if kind != "" {
			r = r.WithContext(withClientKind(r.Context(), kind))
		}
		rec := httptest.NewRecorder()
		rpcCall(app, rec, r)
		return rec
	}
	for _, m := range []string{"GetSettings", "SaveSettings", "PatchSettings", "RevealChannelToken"} {
		if rec := do(m, "device"); rec.Code != http.StatusForbidden {
			t.Errorf("%s from a device: %d", m, rec.Code)
		}
	}
	if rec := do("GetSettings", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "llm-secret-value") {
		t.Errorf("GetSettings for the owner: %d", rec.Code)
	}
	rec := do("PhoneSettings", "device")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret-value") {
		t.Fatalf("PhoneSettings from a device: %d", rec.Code)
	}
}

// The phone reads every feature and switches them through features.
func TestPhoneSettingsFeatures(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("SUPERLEO_BROWSER_MODE", "")
	t.Setenv("SUPERLEO_DESKTOP", "")
	app := NewApp()
	app.settings = phoneTestSettings()
	v := app.PhoneSettings()
	if !v.Features["wellbeing"] || !v.Features["finance"] || v.Features["browser"] {
		t.Fatalf("features: %v", v.Features)
	}
	v, err := app.PatchPhoneSettings(map[string]any{"features": map[string]any{"finance": false, "desktop": true}, "pii_redaction": true})
	if err != nil {
		t.Fatal(err)
	}
	if v.Features["finance"] || !v.Features["desktop"] || !v.PIIRedaction {
		t.Fatalf("after patch: %+v", v)
	}
	if s := app.GetSettings(); len(s.FeaturesOff) != 1 || !s.DesktopControl {
		t.Fatalf("saved: %v %v", s.FeaturesOff, s.DesktopControl)
	}
	if _, err := app.PatchPhoneSettings(map[string]any{"features": map[string]any{"nope": true}}); err == nil {
		t.Fatal("an unknown feature was taken")
	}
	if _, err := app.PatchPhoneSettings(map[string]any{"features": map[string]any{"finance": "yes"}}); err == nil {
		t.Fatal("a non-bool was taken")
	}
	backend.ApplyFeatures(&backend.Settings{})
}
