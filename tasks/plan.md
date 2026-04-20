# Multimedia Server — Implementation Plan

## 의존성 그래프

```
[Phase 1: 프로젝트 기반]
  └─ Go module init + 디렉토리 구조 + Docker 설정
        │
        ▼
[Phase 2: 핵심 백엔드 — 수직 슬라이스 순서]
  T1: 파일 탐색 API (GET /api/browse)
      → 이것이 없으면 프론트엔드가 아무것도 표시 못함
  T2: 파일 업로드 API (POST /api/upload)
      → 테스트 파일 없으면 다른 기능 검증 불가
  T3: 스트리밍 API (GET /api/stream) — Range 지원
      → 동영상/음악 재생의 핵심
  T4: 이미지 서빙 + 섬네일 (GET /api/thumb)
      → T2(업로드) 완료 후 섬네일 연동
  T5: 파일 삭제 API (DELETE /api/file)
      → 독립적, 나중에 해도 됨
        │
        ▼
[Phase 3: 프론트엔드]
  T6: HTML 골격 + CSS + 파일 브라우저 UI
      → T1 API 완성 후
  T7: 이미지 갤러리 (섬네일 그리드 + 원본 뷰어)
      → T4 완성 후
  T8: 동영상/음악 플레이어
      → T3 완성 후
  T9: 업로드 UI (드래그 앤 드롭)
      → T2 완성 후
        │
        ▼
[Phase 4: Docker + 마무리]
  T10: Dockerfile + docker-compose.yml
  T11: path traversal 방어 + 통합 테스트
```

---

## Phase 1 — 프로젝트 기반

### T0: 프로젝트 초기화
**목표:** Go 모듈, 디렉토리 구조, 의존성 설치  
**완료 기준:**
- `go mod init` 완료, `go.mod` 생성
- 패키지: `github.com/disintegration/imaging`, `modernc.org/sqlite`
- `cmd/server/main.go` — HTTP 서버 기동 (포트 8080)
- `internal/handler/`, `internal/thumb/`, `internal/db/`, `web/` 디렉토리 생성

**검증:** `go build ./...` 성공

---

## Phase 2 — 백엔드 수직 슬라이스

### T1: 파일 탐색 API
**경로:** `internal/handler/browse.go`  
**엔드포인트:** `GET /api/browse?path=`  
**반환:**
```json
{
  "path": "/movies",
  "entries": [
    {"name": "film.mp4", "type": "video", "size": 1234567, "is_dir": false},
    {"name": "subfolder", "type": "dir", "is_dir": true}
  ]
}
```
**완료 기준:**
- `/data` 루트 밖으로 나가는 path traversal 차단 (400 반환)
- `type` 필드: `image` | `video` | `audio` | `other` | `dir`
- 숨김 파일(`.thumb`) 목록에서 제외

**검증:** `httptest`로 정상/traversal 케이스 테스트

---

### T2: 파일 업로드 API
**경로:** `internal/handler/files.go`  
**엔드포인트:** `POST /api/upload?path=`  
**동작:**
- multipart/form-data, `file` 필드
- 저장 경로 path traversal 검증
- 이미지 파일이면 고루틴으로 섬네일 생성 (비동기)
- 201 반환

**완료 기준:**
- 파일 저장 성공
- 이미지 업로드 시 `.thumb/{filename}.jpg` 자동 생성
- 지원 확장자 외 파일도 저장 허용 (제한 없음)

**검증:** 이미지/동영상 업로드 후 파일 존재 확인

---

### T3: 스트리밍 API (Range 지원)
**경로:** `internal/handler/stream.go`  
**엔드포인트:** `GET /api/stream?path=`  
**동작:**
- `http.ServeContent` 활용 (Range 자동 처리)
- MIME 타입 맵:
  - `.mp4` → `video/mp4`, `.mkv` → `video/x-matroska`
  - `.avi` → `video/x-msvideo`, `.ts` → `video/mp2t`
  - `.mp3` → `audio/mpeg`, `.flac` → `audio/flac`
  - `.aac` → `audio/aac`, `.ogg` → `audio/ogg`
  - `.wav` → `audio/wav`, `.m4a` → `audio/mp4`

**완료 기준:**
- `Range: bytes=0-` 요청에 206 Partial Content 반환
- 파일 미존재 시 404 반환
- path traversal 차단

**검증:** curl로 Range 헤더 테스트

---

### T4: 이미지 서빙 + 섬네일
**경로:** `internal/handler/image.go`, `internal/thumb/thumb.go`  
**엔드포인트:** `GET /api/thumb?path=`  
**동작:**
- `.thumb/{filename}.jpg` 파일 존재하면 반환
- 없으면 즉시 생성 후 반환 (lazy generation fallback)
- 원본 이미지: `/api/stream?path=` 재사용

**섬네일 스펙:**
- 크기: 200×200px, fit (비율 유지, 크롭 없음)
- 포맷: JPEG, quality 85

**완료 기준:**
- JPG/PNG/WEBP/GIF 모두 섬네일 생성 성공
- GIF는 첫 프레임 사용
- 비이미지 파일 요청 시 400 반환

**검증:** 각 포맷별 업로드 후 `/api/thumb` 응답 확인

---

### T5: 파일 삭제 API
**경로:** `internal/handler/files.go`  
**엔드포인트:** `DELETE /api/file?path=`  
**동작:**
- 파일 삭제
- 이미지면 `.thumb/{filename}.jpg`도 함께 삭제
- 204 반환

**완료 기준:**
- path traversal 차단
- 미존재 파일 404 반환

---

## Phase 3 — 프론트엔드

### T6: 파일 브라우저 UI
**파일:** `web/index.html`, `web/style.css`, `web/app.js`  
**기능:**
- 초기 로드 시 `/api/browse?path=/` 호출
- 폴더 클릭 → 하위 디렉토리 탐색
- Breadcrumb 네비게이션
- 파일 타입별 아이콘
- 현재 경로 표시

---

### T7: 이미지 갤러리
**기능:**
- 이미지 파일 → 섬네일 그리드 표시
- 섬네일 클릭 → 라이트박스 원본 뷰어
- ESC/클릭으로 닫기
- 이전/다음 탐색

---

### T8: 동영상/음악 플레이어
**기능:**
- 동영상: HTML5 `<video>` 인라인 플레이어 (controls)
- 음악: 하단 고정 오디오 플레이어 + 재생목록
- 현재 폴더의 오디오 파일 자동 재생목록 구성

---

### T9: 업로드 UI
**기능:**
- 드래그 앤 드롭 영역
- 파일 선택 버튼
- 업로드 진행률 표시 (XHR progress event)
- 완료 후 파일 목록 새로고침

---

## Phase 4 — Docker + 마무리

### T10: Docker 설정
**파일:** `Dockerfile`, `docker-compose.yml`  
**Dockerfile:**
```
FROM golang:1.22-alpine AS builder
→ go build -o /server ./cmd/server

FROM alpine:3.19
COPY --from=builder /server /server
COPY web/ /web/
EXPOSE 8080
```
**docker-compose.yml:**
- named volume `media` → `/data`
- web static 파일 경로: `/web`

**완료 기준:** `docker compose up` 후 `localhost:8080` 접근 성공

---

### T11: 보안 + 통합 테스트
**path traversal 방어:**
```go
func safePath(root, rel string) (string, error) {
    abs := filepath.Join(root, filepath.Clean("/"+rel))
    if !strings.HasPrefix(abs, root) {
        return "", ErrPathTraversal
    }
    return abs, nil
}
```
**테스트 케이스:**
- `?path=../../etc/passwd` → 400
- `?path=/valid/file.mp4` → 정상
- 업로드 → 섬네일 → 스트리밍 전체 흐름

---

## 구현 순서 요약

| 순서 | Task | 예상 소요 |
|------|------|-----------|
| 1 | T0: 프로젝트 초기화 | 짧음 |
| 2 | T1: 파일 탐색 API | 짧음 |
| 3 | T2: 파일 업로드 API | 중간 |
| 4 | T4: 섬네일 생성 | 중간 |
| 5 | T3: 스트리밍 API | 짧음 |
| 6 | T5: 파일 삭제 API | 짧음 |
| 7 | T6: 파일 브라우저 UI | 중간 |
| 8 | T7: 이미지 갤러리 | 중간 |
| 9 | T8: 동영상/음악 플레이어 | 중간 |
| 10 | T9: 업로드 UI | 중간 |
| 11 | T10: Docker 설정 | 짧음 |
| 12 | T11: 보안 + 통합 테스트 | 중간 |

---

## Phase 5 — TS 트랜스코딩 (신규)

### 문제
`.ts` 파일은 `video/mp2t` MIME 타입이지만 Chrome/Firefox 데스크탑은 `<video>` 태그로 재생하지 못한다.

### 해결
요청 시 ffmpeg로 TS → fragmented MP4 실시간 remux. 재인코딩 없이 컨테이너만 변환하므로 CPU 부하 없음.

### T12: Dockerfile에 ffmpeg 설치
```
RUN apk add --no-cache ca-certificates tzdata ffmpeg
```

### T13: media.IsTS() 헬퍼
`internal/media/types.go`에 추가:
```go
func IsTS(name string) bool {
    return strings.ToLower(extOf(name)) == ".ts"
}
```

### T14: stream.go — .ts 분기 + streamTS()
`fi.IsDir()` 검사 직후, `http.ServeContent` 전에 분기 삽입:
```go
if media.IsTS(fi.Name()) {
    h.streamTS(w, r, abs)
    return
}
```

`streamTS` 메서드:
```go
func (h *Handler) streamTS(w http.ResponseWriter, r *http.Request, absPath string) {
    cmd := exec.CommandContext(r.Context(), "ffmpeg",
        "-loglevel", "error",
        "-i", absPath,
        "-c:v", "copy", "-c:a", "copy",
        "-f", "mp4",
        "-movflags", "frag_keyframe+empty_moov+default_base_moof",
        "pipe:1",
    )
    stdout, _ := cmd.StdoutPipe()
    stderr, _ := cmd.StderrPipe()
    if err := cmd.Start(); err != nil {
        writeError(w, http.StatusInternalServerError, "ffmpeg start failed")
        return
    }
    go io.Copy(io.Discard, stderr)
    w.Header().Set("Content-Type", "video/mp4")
    w.Header().Set("Accept-Ranges", "none")
    io.Copy(w, stdout)
    cmd.Wait()
}
```

**ffmpeg 인자:**
- `-c:v copy -c:a copy` — 재인코딩 없이 remux (CPU 부하 없음)
- `frag_keyframe+empty_moov+default_base_moof` — 파일 크기 없이 즉시 재생 가능한 fMP4
- `exec.CommandContext(r.Context(), ...)` — 클라이언트 연결 끊기면 자동 SIGKILL

**제약:**
- Range 미지원 (`Accept-Ranges: none`)
- 잘못된 TS → 200 OK + 빈 body (헤더 먼저 전송 후 ffmpeg 실패)

### T15: stream_test.go — 트랜스코딩 테스트
```go
func requireFFmpeg(t *testing.T) {
    if _, err := exec.LookPath("ffmpeg"); err != nil {
        t.Skip("ffmpeg not found")
    }
}
```
케이스:
- `ts returns 200 with video/mp4`
- `ts sets Accept-Ranges: none`
- `ts range request returns 200 not 206`

### T16: 검증
1. `docker compose up --build` 빌드 성공
2. 브라우저에서 .ts 파일 클릭 → 재생 확인
3. `curl -v "http://localhost:8080/api/stream?path=/sample.ts"` → `Content-Type: video/mp4`

---

## Phase 6 — 폴더 생성/삭제

### 의존성 그래프

```
T-F1 (백엔드 handleFolder)
  ├── T-F2 (백엔드 테스트)       ← T-F1 완료 후
  └── T-F3 (프론트 HTML/CSS)
        └── T-F4 (프론트 JS)    ← T-F3 완료 후
```

T-F1과 T-F3는 병렬 가능. T-F2는 T-F1 완료 후, T-F4는 T-F3 완료 후.

---

### T-F1: 백엔드 — handleFolder

**파일:** `internal/handler/files.go`, `internal/handler/handler.go`

**POST /api/folder?path=**
1. `r.URL.Query().Get("path")` → `media.SafePath`로 부모 경로 검증
2. 요청 바디 JSON `{"name": "..."}` 파싱
3. 이름 유효성 검사 (400 반환 케이스):
   - 빈 문자열
   - `/` 포함
   - 이름이 `.` 또는 `..`
4. `filepath.Join(parentAbs, name)` 후 SafePath 재검증 (traversal 방지)
5. `os.Stat` → 이미 존재하면 409
6. `os.Mkdir(targetAbs, 0755)` (한 단계만, MkdirAll 아님)
7. 성공: 201 + `{"path": "/movies/new-folder"}`

**DELETE /api/folder?path=**
1. `media.SafePath`로 경로 검증
2. `os.Stat` → 미존재 404, 파일이면 400 `"not a directory"`
3. `os.RemoveAll(abs)` — `.thumb/` 포함 전체 재귀 삭제
4. 성공: 204

`handler.go`에 추가:
```go
mux.HandleFunc("/api/folder", h.handleFolder)
```

**검증:** `go build ./...` 성공

---

### T-F2: 백엔드 테스트

**파일:** `internal/handler/files_test.go`

POST /api/folder 케이스:
- [ ] 정상 생성 → 201, path 반환
- [ ] 이미 존재하는 이름 → 409
- [ ] 빈 이름 → 400
- [ ] 슬래시 포함 이름 (`a/b`) → 400
- [ ] `.` 이름 → 400
- [ ] `..` 이름 → 400
- [ ] path traversal (`../escape`) → 400

DELETE /api/folder 케이스:
- [ ] 내용 있는 폴더 재귀 삭제 → 204, 파일시스템에서 제거 확인
- [ ] 존재하지 않는 경로 → 404
- [ ] 파일 경로 전달 (디렉토리 아님) → 400
- [ ] path traversal → 400

**검증:** `go test ./internal/handler/... -v` 전체 PASS

---

### T-F3: 프론트엔드 — HTML/CSS (모달)

**파일:** `web/index.html`, `web/style.css`

`index.html` 변경:
- 툴바에 "새 폴더" 버튼 추가 (`id="new-folder-btn"`)
- 폴더 생성 모달 추가 (기본 hidden):
  - 이름 입력 `<input id="folder-name-input">`
  - 취소/만들기 버튼
  - 에러 메시지 영역 `<p id="folder-error">`

`style.css` 변경:
- `.modal-overlay` — 전체화면 반투명 오버레이
- `.modal` — 중앙 카드 (max-width 360px)
- `.modal-error` — 빨간 에러 텍스트

**검증:** 브라우저에서 모달 HTML/CSS 렌더링 확인

---

### T-F4: 프론트엔드 — JS 로직

**파일:** `web/app.js`

추가 기능:
- `createFolder()` — 모달 표시, POST 후 목록 새로고침
  - 201 → 모달 닫기 + `browse(currentPath, false)`
  - 409 → 에러 "이미 존재하는 폴더입니다"
  - 400 → 에러 "유효하지 않은 이름입니다"
  - Enter 키 → 확인 동작
- `deleteFolder(path)` — 재귀 삭제 경고 confirm, DELETE 후 새로고침

`buildTable()` 수정:
- 현재: 모든 항목 → `deleteFile(entry.path)`
- 변경: `entry.is_dir` 분기 → `deleteFolder` / `deleteFile`

**검증:**
- 새 폴더 생성 → 목록에 즉시 반영
- 파일 포함 폴더 삭제 → 경고 확인 → 재귀 삭제 후 목록 새로고침
- 파일 삭제는 기존과 동일 동작 (회귀 없음)

---

## Phase 7 — 동영상 섬네일

### 의존성 그래프

```
VT-1 (media.IsVideo 헬퍼)
  └─ VT-2 (thumb.GenerateFromVideo + IsBlankFrame)    ← VT-1 완료 후
       ├─ VT-3 (placeholder embed)                    ← VT-2와 병렬 가능
       └─ VT-4 (handleThumb 동영상 분기)              ← VT-2 + VT-3 완료 후
            └─ VT-5 (browse thumb_available 동영상 포함) ← VT-4와 병렬 가능
                 └─ VT-6 (테스트)                     ← VT-4 + VT-5 완료 후
```

---

### VT-1: `media.IsVideo()` 헬퍼 추가
**파일:** `internal/media/types.go`

`videoExts` 맵이 이미 있으므로 `IsImage`와 동일한 패턴으로 추가:
```go
func IsVideo(name string) bool {
    return videoExts[strings.ToLower(extOf(name))]
}
```

---

### VT-2: `thumb.GenerateFromVideo()` + `IsBlankFrame()`
**파일:** `internal/thumb/thumb.go`

**`IsBlankFrame(img image.Image) bool`** — 전체 흑/백 판정:
- 이미지 전체 픽셀 순회
- 픽셀 하나라도 R+G+B가 [10, 745] 범위면 → false (정상 프레임)
- 모두 범위 밖이면 → true (빈 프레임)

**`GenerateFromVideo(src, dst string) error`** — ffmpeg 프레임 추출:
- ffprobe로 영상 길이 조회
- 50% 시점 → IsBlankFrame 검사 → 25% 재시도 → 75% 재시도 → 모두 실패 시 error

ffmpeg 명령:
```
ffmpeg -y -loglevel error -ss <offset> -i <src> -vframes 1 -vf "scale=200:200:force_original_aspect_ratio=decrease,pad=200:200:(ow-iw)/2:(oh-ih)/2" <dst>
```

---

### VT-3: `placeholder.jpg` embed
**파일:** `internal/thumb/placeholder.go` (신규), `internal/thumb/placeholder.jpg` (신규)

```go
package thumb

import _ "embed"

//go:embed placeholder.jpg
var Placeholder []byte
```

---

### VT-4: `handleThumb` 동영상 분기 추가
**파일:** `internal/handler/thumb.go`

- `IsImage` → 기존 imaging 로직
- `IsVideo` → `GenerateFromVideo` 호출, 실패 시 `thumb.Placeholder` 반환 (200 OK)
- 그 외 → `400 "unsupported file type"`

캐시 히트 경로는 이미지/동영상 공통으로 thumbPath 존재 시 바로 서빙.

---

### VT-5: `browse.go` — 동영상 `thumb_available` 포함
**파일:** `internal/handler/browse.go`

`ft == media.TypeImage` → `ft == media.TypeImage || ft == media.TypeVideo`

---

### VT-6: 테스트
- `thumb_test.go`: `TestIsBlankFrame`, `TestGenerateFromVideo` (requireFFmpeg 스킵)
- `handler/thumb_test.go`: 동영상 200+image/jpeg, unsupported 400
- `handler/browse_test.go`: 동영상 `thumb_available: true`

**완료 기준:** `go test ./... -v` 전체 PASS

---

## Phase 8 — 동영상 길이 표시 (`feature/video-duration`)

### 배경
SPEC §2.3.2 추가. 썸네일 우하단에 재생 시간 오버레이. 사이드카 파일(`.thumb/{name}.jpg.dur`)에 duration을 평문 float로 캐시.

### 의존성 그래프

```
VD-1 (thumb 패키지: 사이드카 I/O + GenerateFromVideo 통합)
  └─ VD-2 (browse handler: duration_sec 노출 + 기존 캐시 백필)
       └─ VD-3 (frontend: formatDuration + 오버레이 + CSS)
            └─ VD-4 (E2E 수동 검증)
```

VD-1 → VD-2 → VD-3 순차. 각 단계에서 commit + 테스트.

### 사이드카 라이프사이클

| 상태 | `.jpg` | `.jpg.dur` | browse 응답 | 동작 |
|---|---|---|---|---|
| 신규 동영상 (썸 미생성) | ✗ | ✗ | `null` | `/api/thumb` 첫 호출 시 함께 생성 |
| 정상 썸 생성 후 | ✓ | ✓ | float | 캐시 hit |
| 기존 썸 (이 기능 이전) | ✓ | ✗ | float (백필 후) | browse 첫 호출 때 ffprobe 1회 → `.dur` 작성 |
| placeholder 사용 (손상) | ✗ | ✗ | `null` | UI 오버레이 숨김 |

**왜 thumb 엔드포인트가 아닌 browse에서 백필?** 기존 사용자는 이미 `.thumb/{name}.jpg`가 있어 `/api/thumb`을 다시 호출하지 않음. 캐시 무효화 없이 마이그레이션하려면 browse에서 백필해야 함.

---

### VD-1: thumb 패키지 — 사이드카 I/O
**파일:** `internal/thumb/thumb.go`, `internal/thumb/thumb_test.go`

**추가 함수 (export):**
- `ProbeDuration(src string) (float64, error)` — 기존 비공개 `videoDuration` 승격
- `DurationSidecarPath(thumbPath string) string` → `thumbPath + ".dur"`
- `WriteDurationSidecar(thumbPath string, sec float64) error` — 평문 `strconv.FormatFloat(sec, 'f', 3, 64)` 저장
- `ReadDurationSidecar(thumbPath string) (float64, bool)` — 미존재/파싱 실패 시 `(0, false)`

**`GenerateFromVideo` 변경:**
- 성공 경로(JPEG 저장 직후) 끝에 `WriteDurationSidecar(dst, duration)` 호출 (실패해도 thumbnail은 성공이므로 에러 무시 + 로그 없음 — 다음 browse에서 백필됨)
- placeholder fallback 경로(GenerateFromVideo가 error 반환)에선 사이드카 작성 안 함 (호출자 책임)

**테스트:**
- `TestProbeDuration` — 4초짜리 mp4에서 4.0±0.5 반환
- `TestDurationSidecarRoundTrip` — write → read 동일값 (3 decimal places)
- `TestReadDurationSidecarMissing` — 미존재 시 `(0, false)`
- `TestReadDurationSidecarMalformed` — 잘못된 내용 시 `(0, false)`
- `TestGenerateFromVideoWritesSidecar` — `GenerateFromVideo` 성공 후 `.dur` 존재 확인 (값이 ProbeDuration과 일치)

**완료 기준:** `go test ./internal/thumb/... -v` 전체 PASS, `/api/thumb` 행동 변화 없음

---

### VD-2: browse handler — `duration_sec` 노출 + 백필
**파일:** `internal/handler/browse.go`, `internal/handler/browse_test.go`

**`entry` 구조체 추가:**
```go
DurationSec *float64 `json:"duration_sec"`  // omitempty 없이 — null 일관성
```

**video entry 처리 로직:**
```go
thumbPath := filepath.Join(abs, ".thumb", name+".jpg")
if _, err := os.Stat(thumbPath); err == nil {
    thumbAvail = true
    if sec, ok := thumb.ReadDurationSidecar(thumbPath); ok {
        durSec = &sec
    } else {
        // 백필: 기존 썸은 있지만 사이드카 없음
        if sec, err := thumb.ProbeDuration(absSrc); err == nil && sec > 0 {
            _ = thumb.WriteDurationSidecar(thumbPath, sec)
            durSec = &sec
        }
        // ffprobe 실패 시 nil 유지
    }
}
```

**테스트:**
- `TestBrowseDurationSecPresent` — 사이드카 있을 때 `duration_sec` 값 반환
- `TestBrowseDurationSecBackfill` — 썸은 있고 사이드카 없을 때 → 응답에 값 + 사이드카 파일 생성됨 (requireFFmpeg)
- `TestBrowseDurationSecNullForImage` — 이미지 entry는 null
- `TestBrowseDurationSecNullWhenNoThumb` — 썸 없으면 null

**완료 기준:**
- `go test ./internal/handler/... -v` 전체 PASS
- `go build ./...` 성공
- 수동 `curl /api/browse?path=/...` → 동영상 entry에 `duration_sec` 필드 확인

---

### VD-3: frontend — overlay + 포맷팅
**파일:** `web/app.js`, `web/style.css`

**`app.js` 변경:**
- 신규 `formatDuration(sec)` 함수 (Helpers 섹션):
  - `null` / `undefined` / `<= 0` / `NaN` → `null`
  - `< 3600` → `"M:SS"` (초만 0 패딩, 분은 패딩 없음)
  - `>= 3600` → `"H:MM:SS"` (시간 패딩 없음, 분/초 0 패딩)
- `buildVideoGrid` 수정: `formatDuration(entry.duration_sec)`로 변환 후 non-null이면 카드에 `<span class="duration-badge">{text}</span>` 추가

**`style.css` 변경 (`.thumb-card` 블록 근처):**
```css
.thumb-card .duration-badge {
  position: absolute;
  bottom: 4px;
  right: 4px;
  background: rgba(0,0,0,0.75);
  color: #fff;
  padding: 2px 6px;
  border-radius: 3px;
  font-size: 0.75rem;
  font-weight: 600;
  pointer-events: none;
}
```

**검증 (수동):**
- 콘솔에서 `formatDuration(0)` → null, `formatDuration(5)` → "0:05", `formatDuration(65)` → "1:05", `formatDuration(3600)` → "1:00:00", `formatDuration(3725)` → "1:02:05"
- 브라우저에서 동영상 그리드 → 우하단에 시간 오버레이 표시

**완료 기준:** Slice 3 commit 후 VD-4로 진행

---

### VD-4: E2E 수동 검증 (체크포인트)
1. `go run ./cmd/server` (또는 docker compose up --build)
2. 새 동영상 업로드 → 그리드에서 썸네일 우하단에 시간 표시
3. 오버레이 영역 클릭 → 라이트박스 동영상 재생 (pointer-events: none 동작 확인)
4. 기존 동영상 마이그레이션: 임의 동영상의 `.thumb/{name}.jpg.dur` 삭제 → browse 새로고침 → 사이드카 재생성 + 오버레이 정상 표시
5. 손상 동영상 (placeholder 경로): 빈 `.mp4` 파일 → placeholder 표시 + 오버레이 없음
6. 모바일 뷰포트 (DevTools): 오버레이 가독성 + 레이아웃 깨짐 없음

---

### Out of scope
- DB 메타데이터 캐시 (SPEC §5.3 노트대로 보류)
- audio entry duration 표시
- 라이트박스 / video player 내부의 시간 표시
- 일괄 마이그레이션 스크립트 (lazy backfill로 충분)

### 위험 / 롤백
- **위험:** ffprobe 미설치 환경 → 백필 silent fail, null 반환, UI 오버레이 숨김 (acceptable degradation)
- **위험:** 사이드카 파싱 오류 → `(0, false)` 반환 → 다음 browse에서 재시도 (자가복구)
- **롤백:** Phase 8의 commit 3개 revert. `.dur` 파일은 `.thumb/` 안에 있어 정적 서빙 안 됨 → 무해

---

## Phase 9 — URL Import (이미지 다운로드 + 업로드) (`feature/url-image-import`)

### 배경
SPEC §2.6 / §5.1 추가. 사용자가 이미지 URL 목록을 모달에 입력하면 서버가 각 URL을 다운로드하면서(스트리밍) 디스크에 저장한다. 50MB 캡, `image/*` 검증, atomic rename, 자동 리네임. 기존 업로드 흐름의 `createUniqueFile`/`thumbPool` 재사용.

### 결정 사항 (SPEC과의 정렬)
- **충돌 리네임 패턴**: SPEC 예시는 `foo (1).jpg`이지만 구현은 기존 `createUniqueFile`의 `foo_1.jpg` 패턴을 재사용한다 (일관성). SPEC의 예시 텍스트도 같이 정정.
- **batch 처리 동시성**: 서버 측 sequential 처리로 시작 (구현 단순성). 50개 × 평균 5초 = 약 4분 한도면 사용성 충분. 추후 필요 시 semaphore 4 병렬로 확장.
- **결과 UI**: 별도 toast 시스템 도입하지 않고 기존 모달 안에서 결과 인라인 렌더링 (성공 카운트 + 실패 URL 목록). 모달 닫을 때 성공 1건 이상이면 `browse` 새로고침.

### 의존성 그래프

```
UI-1 (urlfetch 패키지: 단일 URL 다운로드 + 테스트)
  └─ UI-2 (handler /api/import-url + 테스트)
       └─ UI-5 (E2E 수동 검증)  ◄── UI-4도 완료 후 진입
UI-3 (HTML/CSS 모달 + 버튼)
  └─ UI-4 (JS: 모달 로직 + 결과 렌더링 + browse 새로고침)
       └─ UI-5
```

UI-1 & UI-3 병렬 가능. UI-2는 UI-1 후, UI-4는 UI-3 후. UI-5는 모두 완료 후.

체크포인트: UI-2 완료 시 `curl -X POST` 으로 백엔드 단독 검증 → 통과해야 UI-4 진행.

---

### UI-1: `internal/urlfetch` 패키지 (단일 URL 다운로드)

**신규 파일:** `internal/urlfetch/fetch.go`, `internal/urlfetch/fetch_test.go`

**공개 API:**
```go
package urlfetch

type Result struct {
    URL      string   `json:"url"`
    Path     string   `json:"path"`     // 저장 경로 (relPath, "/foo/bar.jpg")
    Name     string   `json:"name"`     // 최종 파일명
    Size     int64    `json:"size"`
    Type     string   `json:"type"`     // "image" 고정 (현재 단계)
    Warnings []string `json:"warnings"`
}

type FetchError struct {
    Code string // "invalid_scheme" | "missing_content_length" | ...
    Err  error  // underlying (logging용, 응답엔 노출 안 함)
}
func (e *FetchError) Error() string { return e.Code }

// Fetch: rawURL을 destDir 절대경로 아래로 다운로드.
// destDir은 호출자가 SafePath로 이미 검증한 절대 경로. relDir은 응답용 (예: "/photos").
// 반환된 Result.Path는 relDir + 최종 파일명 (slash-joined).
func Fetch(ctx context.Context, client *http.Client, rawURL, destDir, relDir string) (*Result, *FetchError)

// NewClient: 표준 클라이언트 (연결 10s + Total 60s + 리다이렉트 5회).
// 인증 헤더 자동 첨부 절대 안 함 (default Transport, 쿠키 jar 없음).
func NewClient() *http.Client
```

**`NewClient` 구현 포인트:**
- `http.Transport`: `DialContext`에 `net.Dialer{Timeout: 10*time.Second}`
- `http.Client.Timeout: 60*time.Second` (전체 다운로드 캡)
- `CheckRedirect`: hop 카운트 > 5 면 `errors.New("too_many_redirects")` 리턴 (`Fetch`가 잡아서 `FetchError{Code:"too_many_redirects"}` 변환)
- 매 hop의 URL scheme 검증 (`http`/`https` 외 거부)

**`Fetch` 구현 흐름:**
1. URL 파싱 → `url.Parse`. 실패 → `invalid_url`
2. Scheme 검증 → `http`/`https` 외 → `invalid_scheme`. `http`이면 `warnings = ["insecure_http"]`
3. `http.NewRequestWithContext(ctx, "GET", rawURL, nil)`. 인증 헤더 추가 안 함
4. `client.Do(req)` → 에러 분류 (`net.OpError` Timeout flag, `tls.CertificateInvalidError`, redirect 에러). 매핑:
   - DNS / dial 실패 → `network_error` (or `connect_timeout` if `errors.Is(err, context.DeadlineExceeded)` 가능)
   - TLS 검증 실패 → `tls_error`
   - 리다이렉트 cap → `too_many_redirects`
   - context.DeadlineExceeded (전체) → `download_timeout`
5. `defer resp.Body.Close()`. `resp.StatusCode >= 400` → `http_error`
6. Header 검증:
   - `Content-Length` 누락 → `missing_content_length`
   - `Content-Length` 파싱 실패 또는 > 50MB (`50 << 20` 바이트) → `too_large`
   - `Content-Type` 누락 또는 mime 파트가 `image/`로 시작 안 하면 → `unsupported_content_type`
7. 파일명 결정:
   - `urlPath := url.Path`. `path.Base(urlPath)` (URL 경로는 항상 `/`이므로 `path` 패키지)
   - URL 디코드(`url.PathUnescape`), 빈/`/`/`.`/`..` 이면 `"image"`
   - `filepath.Base` 한 번 더 (디스크 측 안전)
   - sanitize: 컨트롤 문자(`< 0x20`, `0x7F`), `\\`, `/`, NUL 제거
   - URL 확장자 (`path.Ext`) 소문자
   - Content-Type → 확장자 매핑 (`image/jpeg` → `.jpg`, `image/png` → `.png`, `image/webp` → `.webp`, `image/gif` → `.gif`)
   - URL 확장자가 매핑된 확장자와 다르면 (`.jpeg` ↔ `.jpg`는 동일 취급) 확장자 교체 → `warnings += "extension_replaced"`
   - 확장자 없으면 매핑된 확장자 추가
8. 다운로드:
   - `tmpFile, err := os.CreateTemp(destDir, ".urlimport-*.tmp")` → 실패 시 `write_error`
   - `defer func(){ tmpFile.Close(); os.Remove(tmpFile.Name()) }()` (정리 보장)
   - `limited := io.LimitReader(resp.Body, 50<<20 + 1)` (캡 + 1바이트로 초과 감지)
   - `n, err := io.Copy(tmpFile, limited)`
     - 에러: `write_error` (디스크) or `network_error` (read)
     - context.DeadlineExceeded → `download_timeout`
     - n > 50MB → `too_large`
   - `tmpFile.Close()` → 실패 시 `write_error`
9. 최종 파일명 충돌 회피 (재사용):
   - `finalPath := filepath.Join(destDir, sanitizedName)`
   - 신규 helper `media.RenameUnique(tmpPath, finalPath) (finalPath string, err error)` — `os.Rename` + EEXIST 시 `_N` 접미사 시도. 또는 `createUniqueFile` 패턴을 borrow하여 `O_CREATE|O_EXCL`로 unique 빈 파일 만들고 `os.Rename` 덮어쓰기 (Windows에서 rename은 destination 존재 시 실패하므로 → `os.Rename`에 EEXIST 처리 필요. 간단하게: `for i := 0; i < 10000; i++`로 candidate 생성, `os.Link`(POSIX) 또는 `os.Rename`(Windows: 먼저 stat으로 미존재 확인 후 rename, race 시 다시) — **단순화**: stat → 존재하면 `_N` 시도, 미존재면 rename. race 손실은 `RemoveAll(.tmp)` 보장으로 무해.
   - 충돌 발생하여 리네임된 경우 → `warnings += "renamed"`
10. 임시 파일 정리: defer가 `os.Remove`하지만 rename 성공 후엔 임시 파일이 이미 사라진 상태이므로 무해 (ENOENT 무시)
11. Result 반환: `Path: path.Join(relDir, finalName)` (slash 형식)

**Content-Type → 확장자 매핑 (urlfetch 내부):**
| MIME (서브타입까지) | ext |
|---|---|
| `image/jpeg` | `.jpg` |
| `image/png` | `.png` |
| `image/webp` | `.webp` |
| `image/gif` | `.gif` |
| 그 외 `image/*` | `unsupported_content_type` (이번 단계 제한) |

**테스트 (`fetch_test.go` — `httptest.NewServer` 모의 origin):**
- `TestFetch_OK_JPEG` — 정상 다운로드, 파일 저장 확인, warnings 비어있음
- `TestFetch_InvalidScheme_FilePath` — `file:///etc/passwd` → `invalid_scheme`
- `TestFetch_NoContentLength` — `Transfer-Encoding: chunked` → `missing_content_length`
- `TestFetch_ContentLengthTooLarge` — Header 60MB 선언 → `too_large` (다운로드 미시작)
- `TestFetch_StreamExceedsCap` — Header 1KB 선언, 실제 60MB stream → `too_large` (스트림 중 중단)
- `TestFetch_NonImageContentType` — `text/html` → `unsupported_content_type`
- `TestFetch_HTTPWarning` — 정상 + warnings 에 `insecure_http` (httptest는 기본 http)
- `TestFetch_ExtensionMismatch` — URL `.jpg` + Content-Type `image/png` → 저장명은 `.png` + `extension_replaced`
- `TestFetch_NoExtensionInURL` — URL `?id=123` + Content-Type `image/jpeg` → 저장명 `image.jpg`
- `TestFetch_RedirectCap` — 6회 리다이렉트 → `too_many_redirects`
- `TestFetch_HTTP404` — `http_error`
- `TestFetch_FilenameSanitize` — URL path에 `..`/`/` 포함 → 안전한 base만 사용
- `TestFetch_Collision_RenamesUnique` — 동일 이름 파일 미리 생성 → `_1.jpg` 저장 + `renamed` warning
- `TestFetch_TempFileCleanedOnError` — 실패 케이스 모두에서 `.tmp` 잔재 없음 검증

**완료 기준:** `go test ./internal/urlfetch/... -v` 전체 PASS

---

### UI-2: `handler.handleImportURL` (`POST /api/import-url`)

**파일:** `internal/handler/import_url.go` (신규), `internal/handler/import_url_test.go` (신규), `internal/handler/handler.go` (라우트 등록)

**라우트:** `mux.HandleFunc("/api/import-url", h.handleImportURL)`

**핸들러 흐름:**
1. Method ≠ POST → 405
2. `rel := r.URL.Query().Get("path")`. `media.SafePath` → 실패 시 400 `invalid path`
3. `os.Stat(destAbs)` → 미존재 / 디렉토리 아님 → 404 `path not found`
4. `os.MkdirAll(destAbs, 0755)` (이미 존재해도 OK; SPEC상 path 없으면 404이지만 SafePath 통과한 경로면 보통 존재). 실제: stat이 우선이므로 MkdirAll은 생략 가능
5. JSON body parse: `{"urls": []string}`. 실패 → 400 `invalid body`
6. URL 배열 정리: trim, 빈 줄/공백 제거. 0개 → 400 `no urls`. > 50개 → 400 `too many urls`
7. `client := h.urlClient` (Handler 필드, `urlfetch.NewClient()`로 초기화)
8. 각 URL 순차 처리 (sequential):
   ```go
   for _, u := range urls {
       ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
       res, ferr := urlfetch.Fetch(ctx, client, u, destAbs, rel)
       cancel()
       if ferr != nil {
           failed = append(failed, failedItem{URL: u, Error: ferr.Code})
           continue
       }
       res.Type = "image"
       succeeded = append(succeeded, *res)
       // 비동기 썸네일 (기존 패턴)
       thumbDir := filepath.Join(destAbs, ".thumb")
       thumbPath := filepath.Join(thumbDir, res.Name+".jpg")
       finalSrc := filepath.Join(destAbs, res.Name)
       if !h.thumbPool.Submit(finalSrc, thumbPath) {
           slog.Warn("thumb pool full, deferring", "src", finalSrc)
       }
   }
   ```
9. 응답 JSON 200:
   ```json
   {"succeeded": [...], "failed": [{"url": "...", "error": "code"}]}
   ```

**Handler 수정 (`handler.go`):**
- `Handler` 구조체에 `urlClient *http.Client` 추가
- `Register`에서 `urlClient: urlfetch.NewClient()` 초기화
- `mux.HandleFunc("/api/import-url", h.handleImportURL)`

**테스트 (`import_url_test.go`):**
- `httptest.NewServer`로 모의 origin 띄우고 실제 URL을 요청에 사용
- `TestImportURL_Single_OK` — 1개 URL → succeeded 1, failed 0, 디스크 파일 확인
- `TestImportURL_Multiple_PartialSuccess` — 3개 (성공/HTTP 404/Content-Type 위반) → succeeded 1 + failed 2 + 각 error code 확인
- `TestImportURL_EmptyArray` — `{"urls": []}` → 400 `no urls`
- `TestImportURL_TooMany` — 51개 → 400 `too many urls`
- `TestImportURL_PathTraversal` — `?path=../escape` → 400 `invalid path`
- `TestImportURL_PathNotFound` — 미존재 디렉토리 → 404
- `TestImportURL_MethodNotAllowed` — GET → 405
- `TestImportURL_InvalidBody` — 비-JSON → 400
- `TestImportURL_ThumbnailQueued` — 성공 후 `.thumb/{name}.jpg` 잠시 후 존재 (poll로 확인)

**완료 기준:**
- `go test ./internal/handler/... -v` 전체 PASS
- `go build ./...` 성공
- 수동 `curl -X POST -H 'Content-Type: application/json' -d '{"urls":["http://localhost:8080/...자기 이미지 URL..."]}' "http://localhost:8080/api/import-url?path=/photos"` → 응답 + 파일 확인

**체크포인트:** 백엔드 단독 검증 통과 후에만 UI-4 진행.

---

### UI-3: 프론트엔드 — HTML/CSS (모달 + 버튼)

**파일:** `web/index.html`, `web/style.css`

**`index.html` 변경:**
- 헤더 / 업로드존 영역에 "URL에서 가져오기" 버튼 추가 (`id="url-import-btn"`). 위치: 새 폴더 버튼 옆 또는 업로드존 내부 (디자인 결정: **업로드존 내부, 라벨 옆**이 의미적으로 자연스러움)
- 모달 추가 (folder-modal과 같은 패턴, hidden 기본):
  ```html
  <div id="url-modal" class="modal-overlay hidden">
    <div class="modal">
      <h3>URL에서 이미지 가져오기</h3>
      <p class="modal-hint">한 줄에 하나씩 입력 (최대 50개, 50MB/파일)</p>
      <textarea id="url-input" rows="6" placeholder="https://example.com/image.jpg"></textarea>
      <div id="url-result" class="hidden"></div>
      <div class="modal-actions">
        <button id="url-cancel-btn">닫기</button>
        <button id="url-confirm-btn" class="btn-primary">가져오기</button>
      </div>
    </div>
  </div>
  ```

**`style.css` 변경:**
- `.modal textarea` — 폭 100%, 폰트 monospace, resize vertical
- `.modal-hint` — 작은 회색 보조 텍스트
- `#url-result` 내부:
  - `.url-result-summary` — bold 카운트
  - `.url-result-failed` — 실패 URL 리스트, 빨강
  - `.url-result-failed li` — `code` 폰트로 URL 표시 + 사유 라벨

**검증:** 브라우저에서 모달 렌더링 확인 (스크립트 없이 `classList.remove('hidden')`로 표시 테스트)

---

### UI-4: 프론트엔드 — JS 로직

**파일:** `web/app.js`

**추가 DOM refs:**
```js
const urlImportBtn = document.getElementById('url-import-btn');
const urlModal     = document.getElementById('url-modal');
const urlInput     = document.getElementById('url-input');
const urlResult    = document.getElementById('url-result');
const urlCancelBtn = document.getElementById('url-cancel-btn');
const urlConfirmBtn = document.getElementById('url-confirm-btn');
```

**기능:**
- `openURLModal()` — input/result 초기화, 모달 표시, focus textarea
- `closeURLModal()` — 숨김. 직전 요청에서 succeeded 1건 이상이면 `browse(currentPath, false)` 호출
- `submitURLImport()`:
  - 줄바꿈 split, trim, 빈 줄/공백 제거 (클라 측 dedupe까지는 안 함)
  - 0개 → 인라인 에러 "URL을 1개 이상 입력하세요"
  - confirm 버튼 disable, "가져오는 중..." 텍스트
  - `fetch('/api/import-url?path=' + encodeURIComponent(currentPath), {method, headers, body: JSON.stringify({urls})})` 
  - 응답 200이 아니면 (4xx body의 `error`) 인라인 표시
  - 200이면 결과 렌더:
    ```
    성공 N개 / 실패 M개
    ── 실패 목록 (있을 때만) ──
    • https://...    [too_large]
    • https://...    [unsupported_content_type]
    ```
  - error code → 한국어 라벨 매핑 (작은 객체):
    - `missing_content_length` → "Content-Length 헤더 없음"
    - `too_large` → "50MB 초과"
    - `unsupported_content_type` → "이미지 아님"
    - `invalid_scheme` → "지원하지 않는 스킴"
    - `http_error` → "HTTP 응답 에러"
    - `connect_timeout` / `download_timeout` → "타임아웃"
    - `tls_error` → "TLS 검증 실패"
    - `too_many_redirects` → "리다이렉트 과다"
    - `network_error` / `write_error` / `invalid_url` → "다운로드 실패"
- `flashSucceededCount` 상태로 닫을 때 새로고침 결정

**이벤트 와이어링:**
- `urlImportBtn.onclick = openURLModal`
- `urlCancelBtn.onclick = closeURLModal`
- `urlConfirmBtn.onclick = submitURLImport`
- `urlModal.onclick = e => { if (e.target === urlModal) closeURLModal() }` (오버레이 클릭으로 닫기)
- ESC 키 처리: 기존 lightbox/folder-modal 패턴 따라 (이미 있으면 추가, 없으면 단순 keydown 추가)

**검증:**
- 모달 열기/닫기, textarea 줄바꿈 입력
- 1건 정상 → succeeded 표시 + 닫으면 그리드에 새 이미지 등장
- 의도적 실패 (잘못된 URL, 비이미지 URL) → 실패 라벨 정확
- 50건 초과 입력 → 백엔드 `too many urls` 받아 인라인 에러
- path traversal 등 4xx → 친절한 메시지

---

### UI-5: E2E 수동 검증 (체크포인트)

1. `go run ./cmd/server` (또는 `docker compose up --build`)
2. 모달 열기 → 빈 입력 → "URL을 입력하세요" 인라인 에러
3. 정상 이미지 URL 1개 → 성공 1개 → 닫기 → 그리드에 표시 + 썸네일 자동 생성
4. 여러 URL (성공 + 비이미지 + 404) → 부분 성공 결과 정확
5. 50MB 넘는 이미지 URL → `too_large` 표시, 디스크에 잔재 파일 없음 (`ls .tmp/` 없음)
6. HTTP URL → 성공 (`insecure_http` warning은 응답에는 있지만 UI엔 표시 안 해도 OK; 추후 추가 가능)
7. URL 확장자(`.jpg`) ↔ 응답 타입(`image/png`) 불일치 → 디스크에 `.png`로 저장
8. 동일 URL 두 번 → 두 번째는 `_1` 붙어서 저장
9. path traversal `?path=../etc` → 불가능 (이 모달은 currentPath만 사용)
10. 모바일 뷰포트: 모달 가독성, textarea resize

---

### Out of scope (이번 단계 제외)
- 동영상/음악 URL (현재 image/* 만 — SPEC §2.6 명시)
- 도메인 화이트리스트
- SSRF 강방어 (사설 IP 차단)
- 진행률 표시 (다운로드 % per URL) — 동기 batch라 UI 단순화
- 인증 헤더/쿠키 입력 UI
- toast 알림 시스템 (모달 인라인 결과로 대체)
- DB 메타데이터 갱신 (브라우저는 fs 기반)

---

### 위험 / 롤백
- **위험: SSRF (사설 IP 호출)** — 답변상 약하게 가도 OK. LAN 내부 호스트 호출 가능 (예: `http://192.168.0.1/admin`). 개인 사용자가 의도적으로 입력하지 않는 한 무해. 추후 강화 시 host resolver에서 사설 IP 차단 hook 추가 가능.
- **위험: 임시 파일 누적** — 모든 실패 경로에서 `defer` 로 `.tmp` 정리. 비정상 종료(SIGKILL) 시 잔재 가능. 시작 시 `destDir/.urlimport-*.tmp` 청소 routine은 over-engineering이므로 도입 안 함.
- **위험: thumbPool 큐 가득** — 기존 동작 그대로 → `handleThumb` lazy 생성 fallback. 회귀 없음.
- **위험: 외부 호출로 인한 응답 지연** — sequential 50개 = 최대 50분. 개인용 + UI 모달이라 사용자가 의도적으로 50개 동시 입력은 드물 것. 필요 시 semaphore(4) 도입.
- **롤백:** Phase 9의 commit revert. `internal/urlfetch/` 디렉토리 삭제, handler.go 라우트/필드 제거, web/index.html·style.css·app.js의 url-modal 블록 제거. 기존 동작 영향 없음 (순수 추가 기능).
