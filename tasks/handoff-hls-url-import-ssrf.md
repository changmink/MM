# HLS URL Import SSRF 강화 핸드오프

> **Status: merged** — `internal/urlfetch/dialer.go` SSRF 가드 + HLS playlist size cap이 develop에 들어갔다. 본 핸드오프는 historical record로 보존.

## 현재 상태

- 작업 브랜치: `feature/harden-hls-url-import-ssrf`
- 작업 worktree: `/mnt/d/file-server-hls-ssrf`
- 기준 브랜치: `origin/develop`
- 관련 이슈: https://github.com/changmink/MM/issues/5
- 현재 worktree 상태: 문서 추가 전까지 clean

`develop`에는 이미 다음 PR이 머지되어 있다.

- PR #4: URL import private network 차단
- PR #6: `AGENTS.md` 추가

이 브랜치는 PR #4 이후 남은 HLS 경로의 DNS rebinding 한계를 강하게 막는 작업을 진행하기 위한 브랜치다.

## 반드시 먼저 읽을 문서

1. `AGENTS.md`
2. `CLAUDE.md`
3. `SPEC.md`의 URL import / HLS import / Boundaries 섹션
4. 이 문서

`CLAUDE.md` 규칙상 모든 소통, 문서, 주석, 커밋 메시지는 한글로 작성한다. 코드 식별자와 외부 도구 이름은 영문 그대로 둔다.

## 문제 요약

일반 HTTP URL import 경로는 PR #4에서 다음 방식으로 DNS rebinding 창을 닫았다.

1. Go 코드가 hostname을 DNS 해석한다.
2. loopback/private/link-local/multicast/unspecified IP를 거부한다.
3. 통과한 IP literal로 직접 dial한다.

반면 HLS 경로는 아직 강한 방어가 아니다.

현재 HLS 흐름은 master playlist를 Go에서 읽고, 선택된 variant URL을 한 번 검증한 뒤, 원본 URL 문자열을 `ffmpeg -i <variantURL>`에 넘긴다. 이후 variant playlist와 media segment fetch는 ffmpeg가 직접 수행하며, ffmpeg는 자체 DNS 해석을 한다. 따라서 악성 DNS 서버가 Go 검증 시점에는 public IP를 반환하고, ffmpeg fetch 시점에는 private IP를 반환하는 DNS rebinding 우회가 가능하다.

## 목표

HLS import에서 공격자가 제어하는 hostname을 ffmpeg가 직접 DNS 해석하지 않도록 만들어야 한다.

최소 목표:

- HLS variant/media playlist 및 segment/key URI가 private network로 향하지 않는지 Go 쪽에서 검증한다.
- ffmpeg가 검증되지 않은 remote URL을 직접 가져오지 않게 한다.
- 기존 size limit, timeout, cleanup, SSE 이벤트 스키마를 유지한다.

## 권장 접근

가장 안전한 방향은 HLS fetch를 Go 쪽에서 수행하고, ffmpeg에는 검증된 local 입력만 넘기는 구조다.

가능한 설계:

1. 기존 protected URL client 또는 동일한 DNS 검사 로직을 사용해 master playlist를 가져온다.
2. master playlist에서 선택된 variant playlist를 Go 쪽에서 다시 가져온다.
3. variant/media playlist 안의 segment URI와 key URI를 모두 base URL 기준으로 resolve한다.
4. 각 URI에 대해 scheme, DNS, private network 여부를 검사한다.
5. ffmpeg가 원격 URL을 직접 fetch하지 않도록 local temporary playlist/resource로 재작성한다.
6. ffmpeg에는 local temporary playlist 경로를 `-i`로 넘긴다.

주의: 단순히 variant hostname을 IP literal로 바꾸는 방식은 HTTPS SNI/인증서/Host header 문제 때문에 견고하지 않다.

## 관련 코드 위치

- `internal/urlfetch/fetch.go`
  - `NewClient`
  - private network 검사 로직
  - `Fetch`
- `internal/urlfetch/hls.go`
  - `parseMasterPlaylist`
  - `fetchHLS`
  - `runHLSRemux`
  - `watchOutputFile`
- `internal/urlfetch/fetch_hls_test.go`
- `internal/urlfetch/hls_test.go`
- `internal/urlfetch/hls_remux_test.go`
- `internal/handler/import_url_test.go`

## 현재 HLS 제약 문서화 위치

`SPEC.md`에는 현재 한계가 acknowledged risk로 적혀 있다.

- URL import 다운로드 흐름의 private network 검사 설명
- `## 9. Boundaries`의 `Known limitations`

구현이 완료되면 이 내용을 “known limitation”에서 제거하거나, 새 설계 기준으로 업데이트해야 한다.

## 테스트 방향

추가해야 할 테스트 예시:

- master playlist는 public으로 통과하지만 variant URL이 private IP로 resolve되면 거부
- variant playlist 안 segment URL 중 하나라도 private IP로 resolve되면 거부
- `#EXT-X-KEY` URI가 private IP로 resolve되면 거부
- relative segment URI가 base URL 기준으로 올바르게 resolve되고 검증됨
- 정상 HLS fixture는 기존처럼 MP4로 remux됨
- 실패 시 `.urlimport-*.tmp` 등 임시 파일이 남지 않음
- ffmpeg에는 remote URL이 아니라 local playlist/resource가 전달됨

DNS rebinding 자체는 fake resolver나 테스트 전용 transport로 제어 가능한 구조를 먼저 만들어야 한다.

## 검증 명령

기본 검증:

```bash
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test ./internal/urlfetch
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test ./internal/handler
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test ./...
```

HLS/ffmpeg 관련 테스트는 로컬에 `ffmpeg`가 없으면 일부 skip될 수 있다.

## 작업 시 주의점

- `ffmpeg` stderr나 내부 경로를 SSE 응답으로 노출하지 않는다.
- 기존 SSE 이벤트 스키마(`start`, `progress`, `done`, `error`, `summary`)를 깨지 않는다.
- size limit과 timeout은 설정 snapshot 기준으로 유지한다.
- remote URL fetch는 `http`/`https`만 허용한다.
- 모든 temporary 파일은 실패/취소 시 정리한다.
- `SPEC.md`를 구현과 함께 업데이트한다.

## 다음 세션 시작 프롬프트 예시

```text
작업 경로는 /mnt/d/file-server-hls-ssrf 입니다.
AGENTS.md, CLAUDE.md, tasks/handoff-hls-url-import-ssrf.md를 먼저 읽고,
이슈 #5(HLS URL import DNS rebinding 방어 강화)를 진행해 주세요.
```
