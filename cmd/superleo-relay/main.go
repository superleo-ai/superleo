// superleo-relay puts a phone through to a SuperLeo on a computer it shares no
// network with. It reads the name a phone's TLS asks for and copies bytes; the
// TLS itself is between the phone and the computer (internal/relay).
//
//	superleo-relay -addr :43443 -domain relay.superleo.app
//	superleo-relay -addr :43443,127.0.0.1:43444 -domain gateway.superleo.app,relay.superleo.app
//
// Each domain needs a wildcard DNS record, *.relay.superleo.app, pointing
// here; several listen addresses and domains are comma-separated. With
// -peers, relays pass phones on to each other, so a computer linked to any
// one of them is reached through all of them.
package main

import (
	"flag"
	_ "github.com/liliang-cn/superleo/internal/brand" // the old SuperLeo names keep working
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/liliang-cn/superleo/internal/relay"
)

func main() {
	addr := flag.String("addr", ":"+relay.DefaultPort, "where phones and computers connect, comma-separated")
	domain := flag.String("domain", relay.DefaultHost, "phones reach a computer at <id>.<domain>, comma-separated")
	peers := flag.String("peers", "", "the other relays, host:port, comma-separated: a phone whose computer is linked there is passed on")
	flag.Parse()

	srv := &relay.Server{Domains: split(*domain), Peers: split(*peers)}
	addrs := split(*addr)
	errs := make(chan error, len(addrs))
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			slog.Error("cannot listen", "addr", a, "error", err)
			os.Exit(1)
		}
		slog.Info("relay listening", "addr", ln.Addr().String(), "domains", srv.Domains)
		go func() { errs <- srv.Serve(ln) }()
	}
	slog.Error("serve", "error", <-errs)
	os.Exit(1)
}

func split(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
