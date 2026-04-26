package urlfetch

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
)

// testMaxBytes is the generous cap value used by tests that aren't
// exercising the cap-enforcement path itself — 4 GiB is larger than any
// fixture a unit test generates, so call sites pass this value to Fetch /
// runHLSRemux without worrying about accidental rejection.
const testMaxBytes = int64(4) << 30

// sequenceResolver returns a Resolver that yields a different set of IPs on
// each LookupIPAddr call, cycling through provided answers in order. Used by
// DNS rebinding tests where the resolver must answer "public" first and
// "private" on a subsequent lookup. Calls past len(answers) keep returning
// the last entry — that mirrors a stable host whose DNS just stopped
// changing after the rebinding flip.
type sequenceResolver struct {
	mu      sync.Mutex
	answers []map[string][]net.IPAddr
	calls   int
}

func newSequenceResolver(answers ...map[string][]net.IPAddr) *sequenceResolver {
	return &sequenceResolver{answers: answers}
}

func (r *sequenceResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(r.answers) == 0 {
		return nil, &net.DNSError{Err: "no answers configured", Name: host, IsNotFound: true}
	}
	idx := r.calls - 1
	if idx >= len(r.answers) {
		idx = len(r.answers) - 1
	}
	ips, ok := r.answers[idx][host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return ips, nil
}

// Calls reports how many times LookupIPAddr has been invoked. Test helper
// for assertions like "ensure rebinding actually fired N times".
func (r *sequenceResolver) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestSequenceResolver_OrderedAnswers locks the cycling contract: N-th call
// returns answers[N-1], calls past len(answers) return the last entry.
// Drives AC-8 / AC-9 (DNS rebinding test scenarios) — without this the
// rebinding tests would have no way to express "first lookup public, second
// lookup private" deterministically.
func TestSequenceResolver_OrderedAnswers(t *testing.T) {
	pub := net.IPAddr{IP: net.ParseIP("8.8.8.8")}
	priv := net.IPAddr{IP: net.ParseIP("127.0.0.1")}

	r := newSequenceResolver(
		map[string][]net.IPAddr{"foo.example": {pub}},
		map[string][]net.IPAddr{"foo.example": {priv}},
	)

	for i, want := range []net.IP{pub.IP, priv.IP, priv.IP /* cycles to last */} {
		ips, err := r.LookupIPAddr(context.Background(), "foo.example")
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if len(ips) != 1 || !ips[0].IP.Equal(want) {
			t.Errorf("call %d ip = %v, want %v", i+1, ips, want)
		}
	}

	if got := r.Calls(); got != 3 {
		t.Errorf("Calls = %d, want 3", got)
	}
}

// TestSequenceResolver_UnknownHost: a host not in the current answer map
// returns a *net.DNSError with IsNotFound=true so callers see the same
// shape as net.DefaultResolver would for NXDOMAIN.
func TestSequenceResolver_UnknownHost(t *testing.T) {
	r := newSequenceResolver(map[string][]net.IPAddr{
		"known.example": {{IP: net.ParseIP("1.1.1.1")}},
	})

	_, err := r.LookupIPAddr(context.Background(), "missing.example")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("err type = %T, want *net.DNSError; err = %v", err, err)
	}
	if !dnsErr.IsNotFound {
		t.Errorf("IsNotFound = false, want true")
	}
}

// TestSequenceResolver_NoAnswersConfigured: defensive — if a test forgets
// to pass answers we want a clear error, not a panic on the empty slice.
func TestSequenceResolver_NoAnswersConfigured(t *testing.T) {
	r := newSequenceResolver()
	_, err := r.LookupIPAddr(context.Background(), "any.example")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("err = %v, want DNSError IsNotFound", err)
	}
	if r.Calls() != 1 {
		t.Errorf("Calls = %d, want 1 (call should still be counted)", r.Calls())
	}
}
