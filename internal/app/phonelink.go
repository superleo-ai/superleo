// Phones reaching the desktop app.
//
// The companion server (companion.go) is loopback only, so a phone has nothing
// to connect to on a computer that runs SuperLeo as an app. The phone link is
// that something: the same handler, served over TLS on the local network and
// through the relay (internal/relay) for a phone that is somewhere else. Both
// end in this process, with this computer's own certificate; the phone pins
// its fingerprint from the pairing QR code, so the relay passes bytes it
// cannot read.
//
// It starts when a phone is paired and, after that, whenever the app starts
// with a paired phone. The phone_relay setting picks the relay: ours, "off"
// for the local network only, or the person's own (host or host:port);
// SUPERLEO_RELAY, when set, overrides it. phone_urls adds addresses of the
// person's own tunnels to this port. A change to either restarts the link.
package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/liliang-cn/superleo/internal/relay"
)

// phoneLinkPort is tried first so a phone's saved address survives restarts.
var phoneLinkPort = 43561

type phoneLink struct {
	srv    *http.Server
	ln     *feedListener
	port   int
	id     string
	fp     string
	relays []string // host:port each, none when off
	extra  []string // phone_urls, https://host[:port]
	client *relay.Client
	cancel context.CancelFunc
	done   chan struct{}
}

// phoneIdentity is this computer's key and certificate for phones: made once,
// kept in the data directory, so the fingerprint a phone pinned stays true.
type phoneIdentity struct {
	key  *ecdsa.PrivateKey
	cert tls.Certificate
	fp   string
	id   string
}

func phoneIdentityDir() string { return filepath.Join(backend.DataDir(), "phone") }

func loadOrCreatePhoneIdentity(dir string) (*phoneIdentity, error) {
	keyPath, certPath := filepath.Join(dir, "key.pem"), filepath.Join(dir, "cert.pem")
	if kb, err := os.ReadFile(keyPath); err == nil {
		if cb, err := os.ReadFile(certPath); err == nil {
			if id, err := phoneIdentityFrom(kb, cb); err == nil {
				return id, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "SuperLeo"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	kb := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	cb := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, kb, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, cb, 0o644); err != nil {
		return nil, err
	}
	return phoneIdentityFrom(kb, cb)
}

func phoneIdentityFrom(kb, cb []byte) (*phoneIdentity, error) {
	cert, err := tls.X509KeyPair(cb, kb)
	if err != nil {
		return nil, err
	}
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("phone key is not ECDSA")
	}
	id, err := relay.ID(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &phoneIdentity{key: key, cert: cert, fp: certFingerprint(cert.Certificate[0]), id: id}, nil
}

// certFingerprint is how a pairing link names the certificate: sha256 of its
// DER, base64url without padding.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// relayAddrs are the relays to use, host:port each, or none: SUPERLEO_RELAY
// (comma-separated) when it is set, else the phone_relay setting, else the
// default relays.
func relayAddrs(setting string) []string {
	v := strings.TrimSpace(os.Getenv("SUPERLEO_RELAY"))
	if v == "" {
		v = strings.TrimSpace(setting)
	}
	switch {
	case v == "":
		return append([]string(nil), relay.Defaults...)
	case strings.EqualFold(v, "off"):
		return nil
	}
	var out []string
	for _, a := range strings.Split(v, ",") {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(a); err != nil {
			a = net.JoinHostPort(a, relay.DefaultPort)
		}
		out = append(out, a)
	}
	return out
}

// feedListener is the phone link's one listener: connections from the local
// network and from the relay both arrive here.
type feedListener struct {
	tcp  net.Listener
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newFeedListener(tcp net.Listener) *feedListener {
	l := &feedListener{tcp: tcp, ch: make(chan net.Conn), done: make(chan struct{})}
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			l.feed(c)
		}
	}()
	return l
}

func (l *feedListener) feed(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *feedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *feedListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.tcp.Close()
}

func (l *feedListener) Addr() net.Addr { return l.tcp.Addr() }

func listenPhones(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err == nil {
		return ln, nil
	}
	return net.Listen("tcp", ":0")
}

// ensurePhoneLink starts the phone link if it is not running. Desktop only:
// serve mode is reached at whatever address it was deployed under.
func (a *App) ensurePhoneLink() (*phoneLink, error) {
	if a.ctx == nil {
		return nil, errors.New("only the desktop app opens a phone link")
	}
	if a.isHiveMember() {
		return nil, errors.New("phones pair with the hive's queen, not with this Mac")
	}
	c, err := a.ensureCompanion()
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.phones != nil {
		return a.phones, nil
	}
	var cfg backend.Settings
	if a.settings != nil {
		cfg = *a.settings
	}
	ident, err := loadOrCreatePhoneIdentity(phoneIdentityDir())
	if err != nil {
		return nil, fmt.Errorf("phone certificate: %w", err)
	}
	tcp, err := listenPhones(phoneLinkPort)
	if err != nil {
		return nil, err
	}
	ln := newFeedListener(tcp)
	ctx, cancel := context.WithCancel(context.Background())
	p := &phoneLink{
		srv: &http.Server{
			Handler:           c.srv.Handler,
			ReadHeaderTimeout: 30 * time.Second,
		},
		ln:     ln,
		port:   tcp.Addr().(*net.TCPAddr).Port,
		id:     ident.id,
		fp:     ident.fp,
		relays: relayAddrs(cfg.PhoneRelay),
		extra:  cfg.PhoneURLsClean(),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{ident.cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	})
	go func() {
		defer close(p.done)
		if err := p.srv.Serve(tlsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("phone link", "error", err)
		}
	}()
	if len(p.relays) > 0 {
		p.client = &relay.Client{Addr: p.relays[0], Addrs: p.relays[1:], Key: ident.key, Deliver: ln.feed}
		go p.client.Run(ctx)
	}
	a.phones = p
	return p, nil
}

func (a *App) stopPhoneLink() {
	a.mu.Lock()
	p := a.phones
	a.phones = nil
	a.mu.Unlock()
	if p == nil {
		return
	}
	p.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.srv.Shutdown(ctx); err != nil {
		_ = p.srv.Close()
	}
	_ = p.ln.Close()
	<-p.done
}

// phoneLinkSettingsChanged reports a change of what the phone link is
// started with.
func phoneLinkSettingsChanged(before, after *backend.Settings) bool {
	if before == nil || after == nil {
		return false
	}
	return strings.Join(relayAddrs(before.PhoneRelay), " ") != strings.Join(relayAddrs(after.PhoneRelay), " ") ||
		strings.Join(before.PhoneURLsClean(), " ") != strings.Join(after.PhoneURLsClean(), " ")
}

var phoneRestartMu sync.Mutex

// restartPhoneLink starts a running phone link again with the settings as
// they are now. One not running is left alone: it reads them when it starts.
func (a *App) restartPhoneLink() {
	phoneRestartMu.Lock()
	defer phoneRestartMu.Unlock()
	a.mu.Lock()
	running := a.phones != nil
	a.mu.Unlock()
	if !running {
		return
	}
	a.stopPhoneLink()
	if _, err := a.ensurePhoneLink(); err != nil {
		slog.Warn("phone link", "error", err)
	}
	a.emit("phonelink:changed", nil)
}

// startPhoneLinkIfPaired opens the link at startup for phones paired before.
func (a *App) startPhoneLinkIfPaired() {
	if a.isHiveMember() {
		return
	}
	s, err := openDevices(devicesPath())
	if err != nil || len(s.list()) == 0 {
		return
	}
	if _, err := a.ensurePhoneLink(); err != nil {
		slog.Warn("phone link", "error", err)
	}
}

// urls are where a phone can reach this computer: the local network first,
// then its Tailscale address, then the person's own addresses, then the relay.
func (p *phoneLink) urls() []string {
	return phoneURLs(append(lanAddrs(), tailnetAddrs()...), p.port, p.extra, p.id, p.relays...)
}

func phoneURLs(lan []string, port int, extra []string, id string, relays ...string) []string {
	out := []string{}
	for _, ip := range lan {
		out = append(out, "https://"+net.JoinHostPort(ip, fmt.Sprint(port)))
	}
	out = append(out, extra...)
	return append(out, relayURLs(id, relays)...)
}

func relayURLs(id string, relays []string) []string {
	var out []string
	for _, r := range relays {
		if u := relayURL(id, r); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// relayURL is where the relay at host:port reaches the computer with this
// id, or "" for no relay.
func relayURL(id, relayHostPort string) string {
	if relayHostPort == "" || id == "" {
		return ""
	}
	host, port, _ := net.SplitHostPort(relayHostPort)
	if port == "443" {
		return "https://" + id + "." + host
	}
	return "https://" + net.JoinHostPort(id+"."+host, port)
}

// pairLink is what the QR code carries.
func (p *phoneLink) pairLink(code string) string {
	urls := p.urls()
	if len(urls) == 0 {
		return ""
	}
	q := url.Values{}
	q.Set("server", urls[0])
	for _, u := range urls[1:] {
		q.Add("alt", u)
	}
	q.Set("fp", p.fp)
	q.Set("code", code)
	return "superleo://pair?" + q.Encode()
}

// lanAddrs are this computer's private IPv4 addresses on real interfaces:
// not virtual machine bridges, VPN tunnels or container networks.
func lanAddrs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	sort.SliceStable(ifs, func(i, j int) bool { return ifs[i].Index < ifs[j].Index })
	var out []string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtualInterface(ifc.Name) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := n.IP.To4(); ip4 != nil && ip4.IsPrivate() {
				out = append(out, ip4.String())
			}
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// tailnetAddrs are this computer's Tailscale IPv4 addresses (100.64.0.0/10),
// on whatever interface carries them: utun on a Mac, "Tailscale" on Windows.
// A phone on the same tailnet reaches the computer there from anywhere.
func tailnetAddrs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	sort.SliceStable(ifs, func(i, j int) bool { return ifs[i].Index < ifs[j].Index })
	var out []string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		out = append(out, tailnetIPs(addrs)...)
		if len(out) >= 2 {
			return out[:2]
		}
	}
	return out
}

// tailnetIPs picks the Tailscale IPv4 addresses out of an interface's.
func tailnetIPs(addrs []net.Addr) []string {
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := n.IP.To4(); ip4 != nil && cgnatNet.Contains(ip4) {
			out = append(out, ip4.String())
		}
	}
	return out
}

func virtualInterface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"bridge", "utun", "awdl", "llw", "vmnet", "vboxnet", "docker", "veth", "virbr", "tailscale", "zt", "lima", "vethernet", "anpi", "ap1"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return strings.Contains(n, "virtual") || strings.Contains(n, "vmware") || strings.Contains(n, "hyper-v") || strings.Contains(n, "wsl")
}

// PhoneLinks lists where a paired phone can reach this computer and the
// certificate it must present. A phone asks after connecting, so an address
// that changed reaches it. Empty outside the desktop app.
func (a *App) PhoneLinks() PhoneLinkInfo {
	a.mu.Lock()
	p := a.phones
	var cfg backend.Settings
	if a.settings != nil {
		cfg = *a.settings
	}
	a.mu.Unlock()
	env := strings.TrimSpace(os.Getenv("SUPERLEO_RELAY"))
	if p == nil {
		info := PhoneLinkInfo{URLs: []string{}, RelayEnv: env}
		// Not running yet (no phone paired): the relay address this
		// computer will have, for the Settings page.
		if a.ctx != nil && !a.isHiveMember() {
			if rs := relayAddrs(cfg.PhoneRelay); len(rs) > 0 {
				if ident, err := loadOrCreatePhoneIdentity(phoneIdentityDir()); err == nil {
					info.Relays = relayURLs(ident.id, rs)
					info.Relay = info.Relays[0]
				}
			}
		}
		return info
	}
	return PhoneLinkInfo{
		URLs:        p.urls(),
		Fingerprint: p.fp,
		Relayed:     p.client != nil && p.client.Linked(),
		Relay:       first(relayURLs(p.id, p.relays)),
		Relays:      relayURLs(p.id, p.relays),
		Running:     true,
		RelayEnv:    env,
	}
}

// PhoneLinkInfo is PhoneLinks' answer.
type PhoneLinkInfo struct {
	URLs        []string `json:"urls"`
	Fingerprint string   `json:"fp,omitempty"`
	Relayed     bool     `json:"relayed"`
	// Relay is this computer's address at its first relay, "" with none;
	// Relays is its address at each.
	Relay  string   `json:"relay,omitempty"`
	Relays []string `json:"relays,omitempty"`
	// Running says the link is open (a phone is paired).
	Running bool `json:"running,omitempty"`
	// RelayEnv is SUPERLEO_RELAY when set: it overrides phone_relay.
	RelayEnv string `json:"relay_env,omitempty"`
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
