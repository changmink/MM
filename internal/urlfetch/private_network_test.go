package urlfetch

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestIsBlockedDestination(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"private 10/8", "10.1.2.3", true},
		{"private 172.16/12", "172.20.1.1", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link local", "169.254.10.20", true},
		{"unspecified", "0.0.0.0", true},
		{"multicast", "224.0.0.1", true},
		{"public v4", "8.8.8.8", false},
		{"public v6", "2001:4860:4860::8888", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.ip)
			if got := isBlockedDestination(ip); got != tt.want {
				t.Fatalf("isBlockedDestination(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

type fakeResolver struct {
	addrs []string
	err   error
}

func (r fakeResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	out := make([]net.IPAddr, 0, len(r.addrs))
	for _, raw := range r.addrs {
		out = append(out, net.IPAddr{IP: net.ParseIP(raw)})
	}
	return out, r.err
}

func TestLookupPublicIPs_BlocksIfAnyAddressIsPrivate(t *testing.T) {
	resolver := fakeResolver{addrs: []string{"8.8.8.8", "127.0.0.1"}}
	_, err := lookupPublicIPs(context.Background(), resolver, "mixed.example")
	if !errors.Is(err, errPrivateNetwork) {
		t.Fatalf("got %v, want errPrivateNetwork", err)
	}
}

func TestResolveHost_FailsClosedOnPartialResolverError(t *testing.T) {
	resolver := fakeResolver{
		addrs: []string{"8.8.8.8"},
		err:   errors.New("partial DNS failure"),
	}
	ips, err := resolveHost(context.Background(), resolver, "partial.example")
	if err == nil {
		t.Fatal("expected resolver error")
	}
	if len(ips) != 0 {
		t.Fatalf("ips = %v, want none when resolver returns an error", ips)
	}
}

func TestFetch_BlocksPrivateNetworkByDefault(t *testing.T) {
	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(),
		"http://127.0.0.1/photo.jpg", dest, "/", int64(4)<<30, nil)
	if ferr == nil || ferr.Code != "private_network" {
		t.Fatalf("got %v, want private_network", ferr)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".urlimport-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}
