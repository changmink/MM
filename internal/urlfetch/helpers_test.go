package urlfetch

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
)

// testMaxBytes는 상한 강제 경로를 직접 검증하지 않는 테스트가 사용하는
// 넉넉한 상한값이다 — 4 GiB는 단위 테스트 픽스처보다 충분히 커서, 호출자가
// 이 값을 Fetch / runHLSRemux에 그대로 넘겨도 우발적 거부 걱정이 없다.
const testMaxBytes = int64(4) << 30

// sequenceResolver는 LookupIPAddr 호출마다 다른 IP 집합을 반환하는
// Resolver를 만든다. 제공된 answers 순서로 사이클링한다. resolver가 첫 번째
// 호출에는 "public", 다음 호출에는 "private"을 답해야 하는 DNS rebinding
// 테스트에서 사용한다. len(answers)를 넘어선 호출은 마지막 항목을 반복
// 반환한다 — rebinding 이후 더는 바뀌지 않는 안정된 호스트를 모사한다.
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

// Calls는 LookupIPAddr가 몇 번 호출됐는지 보고한다. "rebinding이 실제로 N번
// 발생했는지 확인" 같은 단언에 쓰는 테스트 헬퍼다.
func (r *sequenceResolver) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestSequenceResolver_OrderedAnswers는 사이클링 계약을 고정한다: N번째
// 호출이 answers[N-1]을 반환하고, len(answers) 이후 호출은 마지막 항목을
// 반환한다. AC-8 / AC-9(DNS rebinding 테스트 시나리오)를 구동한다 — 이게
// 없으면 rebinding 테스트가 "첫 lookup은 public, 두 번째는 private"를
// 결정적으로 표현할 방법이 없다.
func TestSequenceResolver_OrderedAnswers(t *testing.T) {
	pub := net.IPAddr{IP: net.ParseIP("8.8.8.8")}
	priv := net.IPAddr{IP: net.ParseIP("127.0.0.1")}

	r := newSequenceResolver(
		map[string][]net.IPAddr{"foo.example": {pub}},
		map[string][]net.IPAddr{"foo.example": {priv}},
	)

	for i, want := range []net.IP{pub.IP, priv.IP, priv.IP /* 마지막 항목으로 사이클링 */} {
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

// TestSequenceResolver_UnknownHost: 현재 answer 맵에 없는 호스트는
// IsNotFound=true인 *net.DNSError를 반환해, 호출자가 NXDOMAIN에 대한
// net.DefaultResolver의 응답과 같은 모양을 보게 한다.
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

// TestSequenceResolver_NoAnswersConfigured: 방어적 — 테스트가 answers를
// 빠뜨렸을 때 빈 슬라이스 panic 대신 명확한 에러를 받기 위함.
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
