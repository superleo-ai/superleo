package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/liliang-cn/superleo/internal/relay"
)

// The local network first, then the person's own addresses, then the relay.
func TestPhoneURLsOrder(t *testing.T) {
	got := phoneURLs([]string{"192.168.1.5", "10.0.0.2"}, 43561,
		[]string{"https://desk.example.com", "https://frp.example.net:7443"},
		"abcdefghijklmnop", "gateway.superleo.cn:443", "relay.example.com:43443")
	want := []string{
		"https://192.168.1.5:43561",
		"https://10.0.0.2:43561",
		"https://desk.example.com",
		"https://frp.example.net:7443",
		"https://abcdefghijklmnop.gateway.superleo.cn",
		"https://abcdefghijklmnop.relay.example.com:43443",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("urls\n got %q\nwant %q", got, want)
	}
	// No relay: nothing after the own addresses.
	if got := phoneURLs(nil, 43561, []string{"https://a.example.com"}, "abcdefghijklmnop"); !reflect.DeepEqual(got, []string{"https://a.example.com"}) {
		t.Fatalf("relay off: %q", got)
	}
}

func TestRelayAddrFromTheSetting(t *testing.T) {
	t.Setenv("SUPERLEO_RELAY", "")
	def := strings.Join(relay.Defaults, ",")
	for setting, want := range map[string]string{
		"":                       def,
		"  ":                     def,
		"off":                    "",
		"OFF":                    "",
		"relay.example.com":      "relay.example.com:" + relay.DefaultPort,
		"relay.example.com:9443": "relay.example.com:9443",
	} {
		if got := strings.Join(relayAddrs(setting), ","); got != want {
			t.Errorf("relayAddrs(%q) = %q, want %q", setting, got, want)
		}
	}
	if def != "gateway.superleo.cn:443,gateway.superleo.app:443" {
		t.Fatalf("default relays %q", def)
	}
}

func TestSUPERLEO_RELAYOverridesTheSetting(t *testing.T) {
	t.Setenv("SUPERLEO_RELAY", "env.example.com:1234")
	for _, setting := range []string{"", "off", "relay.example.com"} {
		if got := strings.Join(relayAddrs(setting), ","); got != "env.example.com:1234" {
			t.Errorf("relayAddrs(%q) with the env set = %q", setting, got)
		}
	}
	t.Setenv("SUPERLEO_RELAY", "off")
	if got := relayAddrs("relay.example.com"); len(got) != 0 {
		t.Errorf("SUPERLEO_RELAY=off left %q", got)
	}
}

func TestPhoneLinkSettingsChanged(t *testing.T) {
	t.Setenv("SUPERLEO_RELAY", "")
	a := &backend.Settings{}
	cases := []struct {
		b    backend.Settings
		want bool
	}{
		{backend.Settings{}, false},
		{backend.Settings{QueenName: "bee"}, false},
		{backend.Settings{PhoneRelay: "off"}, true},
		{backend.Settings{PhoneRelay: relay.DefaultHost}, true}, // the old relay is no longer the default
		{backend.Settings{PhoneURLs: []string{"https://a.example.com"}}, true},
		{backend.Settings{PhoneURLs: []string{"not a url"}}, false}, // unusable, so nothing changes
	}
	for _, c := range cases {
		if got := phoneLinkSettingsChanged(a, &c.b); got != c.want {
			t.Errorf("%+v: changed = %v, want %v", c.b, got, c.want)
		}
	}
}

// A settings change reaches a running phone link without restarting the app.
func TestPhoneLinkPicksUpNewSettings(t *testing.T) {
	app := companionTestApp(t)
	app.ctx = context.Background()
	prevEmit := windowEmit
	windowEmit = func(context.Context, string, map[string]any) {}
	t.Cleanup(func() { windowEmit = prevEmit })
	prev := phoneLinkPort
	phoneLinkPort = 0
	t.Cleanup(func() { phoneLinkPort = prev })
	t.Cleanup(app.stopPhoneLink)
	t.Setenv("SUPERLEO_RELAY", "")

	app.settings = &backend.Settings{PhoneRelay: "off"}
	if _, err := app.ensurePhoneLink(); err != nil {
		t.Fatal(err)
	}
	if info := app.PhoneLinks(); info.Relay != "" || !info.Running {
		t.Fatalf("relay off: %+v", info)
	}

	before := app.settings
	app.settings = &backend.Settings{PhoneRelay: "relay.invalid:1", PhoneURLs: []string{"https://desk.example.com/"}}
	if !phoneLinkSettingsChanged(before, app.settings) {
		t.Fatal("no change seen")
	}
	app.restartPhoneLink()
	info := app.PhoneLinks()
	n := len(info.URLs)
	if n < 2 || info.URLs[n-2] != "https://desk.example.com" || !strings.HasSuffix(info.URLs[n-1], ".relay.invalid:1") {
		t.Fatalf("after the change: %q", info.URLs)
	}
	if info.Relay != info.URLs[n-1] {
		t.Fatalf("relay %q, urls %q", info.Relay, info.URLs)
	}
}
