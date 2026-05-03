package handlerutil_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"file_server/internal/handlerutil"
)

// TestLockPath_SerializesSameKey 동일 키에 대한 두 호출이 직렬화되는지.
// 첫 호출의 unlock 전엔 두 번째 호출이 진입할 수 없어야 한다.
func TestLockPath_SerializesSameKey(t *testing.T) {
	var m sync.Map
	const key = "same"

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	work := func() {
		unlock := handlerutil.LockPath(&m, key)
		defer unlock()
		cur := inFlight.Add(1)
		for {
			max := maxInFlight.Load()
			if cur <= max || maxInFlight.CompareAndSwap(max, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work()
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("max in-flight = %d, want 1 (same-key calls must serialize)", got)
	}
}

// TestLockPath_DifferentKeysIndependent 다른 키에 대한 호출은 동시에 진행 가능.
func TestLockPath_DifferentKeysIndependent(t *testing.T) {
	var m sync.Map
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	start := make(chan struct{})
	work := func(key string) {
		<-start
		unlock := handlerutil.LockPath(&m, key)
		defer unlock()
		cur := inFlight.Add(1)
		for {
			max := maxInFlight.Load()
			if cur <= max || maxInFlight.CompareAndSwap(max, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
	}

	const n = 4
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			work(string(rune('a' + i)))
		}(i)
	}
	close(start)
	wg.Wait()

	if got := maxInFlight.Load(); got <= 1 {
		t.Errorf("max in-flight = %d, want >1 (different-key calls must run in parallel)", got)
	}
}
