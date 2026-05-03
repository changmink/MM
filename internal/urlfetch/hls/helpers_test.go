package hls

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
)

const testMaxBytes = int64(4) << 30

var errPrivateNetwork = errors.New("private_network")

type testClientConfig struct {
	blockPrivate bool
}

type testClientOption func(*testClientConfig)

func AllowPrivateNetworks() testClientOption {
	return func(*testClientConfig) {}
}

func WithResolver(Resolver) testClientOption {
	return func(cfg *testClientConfig) {
		cfg.blockPrivate = true
	}
}

func NewClient(opts ...testClientOption) *http.Client {
	var cfg testClientConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.blockPrivate {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errPrivateNetwork
		})}
	}
	return http.DefaultClient
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

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
		return nil, fmt.Errorf("no answers for %s", host)
	}
	idx := r.calls - 1
	if idx >= len(r.answers) {
		idx = len(r.answers) - 1
	}
	ips, ok := r.answers[idx][host]
	if !ok {
		return nil, fmt.Errorf("no such host %s", host)
	}
	return ips, nil
}
