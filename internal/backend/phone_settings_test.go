package backend

import (
	"strings"
	"testing"
)

func TestValidatePhoneRelay(t *testing.T) {
	for _, ok := range []string{"", "off", "OFF", "relay.example.com", "relay.example.com:43443", "my-relay.example.co.uk:1", "localhost"} {
		if err := ValidatePhoneRelay(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for bad, why := range map[string]string{
		"https://relay.example.com": "host or host:port",
		"relay.example.com/x":       "host or host:port",
		"relay.example.com:":        "port",
		"relay.example.com:0":       "port",
		"relay.example.com:70000":   "port",
		"relay.example.com:abc":     "port",
		"1.2.3.4":                   "domain name",
		"1.2.3.4:43443":             "domain name",
		"[::1]:43443":               "domain name",
		"-bad.example.com":          "host name",
		"bad_host.example.com":      "host name",
		"user@relay.example.com":    "host or host:port",
		"relay example.com":         "host or host:port",
		"a:b:c":                     "host or host:port",
	} {
		err := ValidatePhoneRelay(bad)
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: got %v, want an error about %s", bad, err, why)
		}
	}
}

func TestValidatePhoneURLs(t *testing.T) {
	if err := ValidatePhoneURLs([]string{"https://desk.example.com", "https://frp.example.net:7443/", "https://100.64.0.1:43561"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		urls []string
		why  string
	}{
		{[]string{"http://desk.example.com"}, "not an https://"},
		{[]string{"desk.example.com"}, "not an https://"},
		{[]string{"https://"}, "not an https://"},
		{[]string{"https://desk.example.com/phone"}, "only https://host"},
		{[]string{"https://desk.example.com?x=1"}, "only https://host"},
		{[]string{"https://u:p@desk.example.com"}, "only https://host"},
		{[]string{"https://desk.example.com:99999"}, "port"},
		{[]string{"https://bad_host.example.com"}, "host name"},
		{[]string{"https://desk.example.com", "https://DESK.example.com/"}, "twice"},
		{[]string{"https://a1.x", "https://a2.x", "https://a3.x", "https://a4.x", "https://a5.x", "https://a6.x", "https://a7.x", "https://a8.x", "https://a9.x"}, "at most"},
	} {
		err := ValidatePhoneURLs(c.urls)
		if err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%q: got %v, want an error about %s", c.urls, err, c.why)
		}
	}
}

func TestPhoneURLsClean(t *testing.T) {
	s := Settings{PhoneURLs: []string{" https://Desk.example.com/ ", "nope", "https://frp.example.net:7443"}}
	got := strings.Join(s.PhoneURLsClean(), " ")
	if got != "https://desk.example.com https://frp.example.net:7443" {
		t.Fatalf("clean = %q", got)
	}
}

// Both keys are an address phone traffic goes to: the person says yes,
// except to going back to the local network only.
func TestPhoneKeysTiers(t *testing.T) {
	if SelfKeyTier("phone_relay", "relay.example.com") != TierNeedsApproval || SelfKeyTier("phone_urls", []any{"https://a.example.com"}) != TierNeedsApproval {
		t.Fatal("phone keys should need approval")
	}
	if SelfKeyTier("phone_relay", "off") != TierWritable {
		t.Fatal(`phone_relay "off" narrows`)
	}
	cfg := Settings{}
	if _, err := SetSettingKey(&cfg, "phone_relay", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := CheckSettingKeys(&cfg, []string{"phone_relay"}); err == nil {
		t.Fatal("an IP relay passed the key check")
	}
	if _, err := SetSettingKey(&cfg, "phone_urls", []any{"https://a.example.com"}); err != nil || len(cfg.PhoneURLs) != 1 {
		t.Fatalf("phone_urls: %v %q", err, cfg.PhoneURLs)
	}
}
