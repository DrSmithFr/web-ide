package lan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/mdns"
)

// Service is the DNS-SD service the pods announce.
const Service = "_web-ide._tcp"

// Record is what a pod announces: nothing secret. Account is a tag of the account, not its
// id, so the announcement cannot be followed from one network to the next.
type Record struct {
	Pod     string // a short id of the device key
	Account string // AccountTag of the pinned account
	Join    string // the code of a pod waiting to be paired (pair join, no relay)
}

// Found is a pod answering the browse.
type Found struct {
	Addr    string // host:port of its LAN port
	Pod     string
	Account string
	Join    string
}

// Resolver announces the pod and finds the others: mDNS, or a fake in tests.
type Resolver interface {
	Announce(port int, r Record) (stop func(), err error)
	Browse(ctx context.Context) ([]Found, error)
}

// AccountTag is the account as announced: 16 hex digits of a hash of its id.
func AccountTag(account string) string {
	sum := sha256.Sum256([]byte("web-ide lan|" + account))
	return hex.EncodeToString(sum[:8])
}

// shortID is the instance name of a device key: 12 hex digits of its hash.
func shortID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

// MDNS is the Resolver of a real network (hashicorp/mdns: a responder on 5353, which
// shares the port with Avahi).
type MDNS struct{}

var quiet = log.New(io.Discard, "", 0)

// Announce answers the mDNS queries for the pod until stop is called.
func (MDNS) Announce(port int, r Record) (func(), error) {
	txt := []string{"v=1", "pod=" + r.Pod}
	if r.Account != "" {
		txt = append(txt, "acct="+r.Account)
	}
	if r.Join != "" {
		txt = append(txt, "join="+r.Join)
	}
	svc, err := mdns.NewMDNSService("web-ide-"+r.Pod, Service, "", "web-ide-"+r.Pod+".local.", port, localIPs(), txt)
	if err != nil {
		return nil, err
	}
	srv, err := mdns.NewServer(&mdns.Config{Zone: svc, Logger: quiet})
	if err != nil {
		return nil, err
	}
	return func() { _ = srv.Shutdown() }, nil
}

// Browse asks once for the pods of the network.
func (MDNS) Browse(ctx context.Context) ([]Found, error) {
	entries := make(chan *mdns.ServiceEntry, 32)
	var found []Found
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			if f, ok := parseEntry(e); ok {
				found = append(found, f)
			}
		}
	}()
	err := mdns.QueryContext(ctx, &mdns.QueryParam{Service: Service, Domain: "local", Timeout: 2 * time.Second,
		Entries: entries, DisableIPv6: true, Logger: quiet})
	close(entries)
	<-done
	return found, err
}

func parseEntry(e *mdns.ServiceEntry) (Found, bool) {
	if e.AddrV4 == nil || e.Port == 0 {
		return Found{}, false
	}
	f := Found{Addr: net.JoinHostPort(e.AddrV4.String(), strconv.Itoa(e.Port))}
	for _, kv := range e.InfoFields {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "pod":
			f.Pod = v
		case "acct":
			f.Account = v
		case "join":
			f.Join = v
		}
	}
	return f, f.Account != "" || f.Join != ""
}

// localIPs are the IPv4 addresses of the interfaces that are up, loopback excluded.
func localIPs() []net.IP {
	var ips []net.IP
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				ips = append(ips, n.IP)
			}
		}
	}
	if len(ips) == 0 {
		ips = []net.IP{net.IPv4(127, 0, 0, 1)}
	}
	return ips
}
