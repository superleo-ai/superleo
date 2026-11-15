package backend

// How a paired phone reaches the desktop when it is not on the same network:
// Settings.PhoneRelay and Settings.PhoneURLs, checked here and used by
// app/phonelink.go.
//
// A relay is reached at <id>.<host>:<port>, <id> being the desktop's own,
// and routes by that name (TLS SNI), so it needs a domain name: an IP
// address has no subdomains. A phone pins the desktop's certificate, so
// whatever sits in between must pass TLS through untouched; an address that
// is not https:// cannot be one of those.

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// PhoneRelayOff is Settings.PhoneRelay for the local network only.
const PhoneRelayOff = "off"

// MaxPhoneURLs is how many addresses of their own a person may add.
const MaxPhoneURLs = 8

// ValidatePhoneRelay reports a phone_relay value the phone link cannot use.
func ValidatePhoneRelay(v string) error {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, PhoneRelayOff) {
		return nil
	}
	bad := func(why string) error {
		return fmt.Errorf("phone_relay %q: %s", v, why)
	}
	if strings.Contains(v, "://") || strings.ContainsAny(v, "/?#@ \t") {
		return bad("give the relay as host or host:port, e.g. relay.example.com:43443")
	}
	host, port := v, ""
	if strings.Contains(v, ":") {
		h, p, err := net.SplitHostPort(v)
		if err != nil {
			return bad("give the relay as host or host:port, e.g. relay.example.com:43443")
		}
		host, port = h, p
		if port == "" {
			return bad("the port must be 1-65535")
		}
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return bad("the port must be 1-65535")
		}
	}
	if net.ParseIP(host) != nil {
		return bad("a relay needs a domain name, not an IP address: each computer is reached at <id>.<host>")
	}
	if !validHostname(host) {
		return bad("not a valid host name")
	}
	return nil
}

// ValidatePhoneURLs reports a phone_urls list the phone link cannot use.
func ValidatePhoneURLs(urls []string) error {
	if len(urls) > MaxPhoneURLs {
		return fmt.Errorf("phone_urls: at most %d addresses", MaxPhoneURLs)
	}
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := ParsePhoneURL(raw)
		if err != nil {
			return err
		}
		if seen[u] {
			return fmt.Errorf("phone_urls: %s is listed twice", u)
		}
		seen[u] = true
	}
	return nil
}

// ParsePhoneURL checks one phone_urls entry and returns it as the phone is
// given it: https://host[:port], no trailing slash.
func ParsePhoneURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("phone_urls: %q is not an https:// address", raw)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("phone_urls: %q should be only https://host or https://host:port", raw)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("phone_urls: %q: the port must be 1-65535", raw)
		}
	}
	if net.ParseIP(u.Hostname()) == nil && !validHostname(u.Hostname()) {
		return "", fmt.Errorf("phone_urls: %q: not a valid host name", raw)
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// ValidatePhoneLink checks both phone settings.
func (s *Settings) ValidatePhoneLink() error {
	if err := ValidatePhoneRelay(s.PhoneRelay); err != nil {
		return err
	}
	return ValidatePhoneURLs(s.PhoneURLs)
}

// PhoneURLsClean is PhoneURLs as phones are given them, skipping any that
// do not parse (a hand-edited file).
func (s *Settings) PhoneURLsClean() []string {
	var out []string
	for _, raw := range s.PhoneURLs {
		if u, err := ParsePhoneURL(raw); err == nil {
			out = append(out, u)
		}
	}
	return out
}

// validHostname: dot-separated labels of letters, digits and hyphens, none
// starting or ending with a hyphen, short enough to carry a 16-character id
// in front.
func validHostname(h string) bool {
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253-17 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
