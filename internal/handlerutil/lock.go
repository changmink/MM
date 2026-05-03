package handlerutil

import "sync"

// LockPath는 공유 sync.Map을 키로 사용해 path 단위 producer를 직렬화한다.
// 각 키는 첫 호출 시 LoadOrStore로 *sync.Mutex를 생성하고 그 뮤텍스를 잠근
// 뒤 Unlock 함수를 반환한다. 호출자는 `defer unlock()` 패턴으로 사용.
//
// 맵 엔트리는 Unlock 후에도 남는다 — 단일 사용자 LAN 가정에서 키 집합이
// 디스크의 unique source path 수로 자연 bound되어 있어 leak으로 이어지지
// 않는다. 다중 사용자 가정으로 확장될 경우 LRU eviction 도입 필요.
//
// 사용처: ffmpeg를 spawn하는 모든 경로
//   - handler/stream.go streamTS — TS 실시간 remux per cache key
//   - handler/convert/convert.go RemuxTSToMP4 — TS→MP4 영구 변환 per src
//   - handler/convert/webp.go EncodeWebP — clip→WebP per src
func LockPath(m *sync.Map, key string) func() {
	v, _ := m.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
