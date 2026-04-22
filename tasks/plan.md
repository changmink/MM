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

## Phase 9 — 파일/폴더 이름 변경 (`feature/file-rename`)

### 배경
SPEC §2.1.1 추가. 파일/폴더 rename 기능. 파일은 확장자 고정, 이미지/동영상은 `.thumb/{name}.jpg` + `.jpg.dur` 사이드카 동기화. 충돌 시 409 (자동 suffix 없음).

### 의존성 그래프

```
R-1 (backend: PATCH /api/file + 사이드카 rename + 단위/통합 테스트)
R-2 (backend: PATCH /api/folder + 통합 테스트)      ← R-1과 병렬 가능
  └─ R-3 (frontend: rename 모달 + 버튼 + JS 로직)   ← R-1·R-2 둘 다 완료 후
       └─ R-4 (E2E 수동 검증 + 회귀 체크)
```

R-1과 R-2는 같은 파일(`files.go`)을 수정하므로 순차 진행 권장 (병합 충돌 회피). R-3는 R-1·R-2 완료 후. R-4는 체크포인트.

### 영향 파일 범위

| 파일 | 변경 내용 |
|---|---|
| `internal/handler/files.go` | `handleFile` 메서드 스위치 확장 (DELETE→DELETE/PATCH), `handleFolder`에 PATCH case 추가, `renameFile`/`renameFolder`/`validateRenameName` 신규 |
| `internal/handler/files_test.go` | rename 케이스 추가 (성공/409/400/404/traversal/사이드카) |
| `web/index.html` | rename 모달 요소 추가 |
| `web/style.css` | 기존 `.modal` 스타일 재사용, 필요 시 rename 버튼 아이콘만 |
| `web/app.js` | `renameEntry(entry)` 함수, `buildTable`/`buildImageGrid`/`buildVideoGrid`에 rename 버튼 추가 |

---

### R-1: 백엔드 — PATCH /api/file (파일 rename + 사이드카)

**파일:** `internal/handler/files.go`, `internal/handler/files_test.go`

**handleFile 리팩토링:**
```go
func (h *Handler) handleFile(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodDelete:
        h.deleteFile(w, r)    // 기존 로직 이동
    case http.MethodPatch:
        h.renameFile(w, r)
    default:
        writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
    }
}
```

**`renameFile` 동작 순서:**
1. `media.SafePath(h.dataDir, rel)` → 원본 경로 검증
2. JSON 바디 `{"name": "..."}` 파싱
3. `os.Stat(srcAbs)` → 미존재 404, 디렉토리면 400 `"not a file"`
4. 원본 확장자 추출: `origExt := filepath.Ext(filepath.Base(srcAbs))`
5. 사용자 입력 base name 정제: `newBase := stripExt(body.Name)` (사용자가 확장자 포함 입력해도 무시)
6. `validateRenameName(newBase)` — 빈 문자열, `/`·`\\`, `.`/`..`, 길이 > 255 → 400 `"invalid name"`
7. `newName := newBase + origExt`
8. 동일 이름 검사: `newName == filepath.Base(srcAbs)` → 400 `"name unchanged"`
9. `dstAbs := filepath.Join(filepath.Dir(srcAbs), newName)` + `media.SafePath` 재검증
10. `os.Stat(dstAbs)` → 존재하면 409 `"already exists"`
11. `os.Rename(srcAbs, dstAbs)` → 실패 시 500
12. **사이드카 동기화** (이미지/동영상인 경우):
    - `oldThumb := filepath.Join(filepath.Dir(srcAbs), ".thumb", oldName+".jpg")`
    - `newThumb := filepath.Join(filepath.Dir(srcAbs), ".thumb", newName+".jpg")`
    - `os.Rename(oldThumb, newThumb)` — `IsNotExist` 무시, 다른 에러는 `slog.Warn`만 (본 파일 rename은 성공)
    - 동영상이면 `.jpg.dur` 사이드카도 동일 로직
13. 응답: `200 OK`, `{"path": relResult, "name": newName}`

**공용 검증 함수 추가:**
```go
func validateRenameName(name string) error {
    if name == "" || name == "." || name == ".." { return fmt.Errorf("invalid name") }
    if len(name) > 255 { return fmt.Errorf("invalid name") }
    for _, c := range name {
        if c == '/' || c == '\\' { return fmt.Errorf("invalid name") }
    }
    return nil
}
// 기존 validateFolderName은 삭제하고 validateRenameName으로 통합 가능 (같은 규칙)
```

**테스트 (`files_test.go`):**
- `TestRenameFileSuccess` — 성공 시 200 + `{path, name}`, 원본 경로에 파일 없음, 신규 경로에 존재
- `TestRenameFileExtensionPreserved` — 사용자가 `new.mp4` 입력, 원본 `.mkv` → 결과는 `new.mkv`
- `TestRenameFileThumbFollows` — 이미지 rename 시 `.thumb/{new}.jpg` 이동 (이미지 픽스처 사용)
- `TestRenameFileDurationSidecarFollows` — 동영상 rename 시 `.thumb/{new}.jpg.dur` 이동 (테스트용 사이드카 작성 후 검증)
- `TestRenameFileMissingSidecarOK` — 사이드카 없어도 200 반환
- `TestRenameFileConflict` — 동일 디렉토리에 같은 이름 있으면 409
- `TestRenameFileNameUnchanged` — 새 이름 = 기존 이름이면 400 `name unchanged`
- `TestRenameFileInvalidName` — 빈 문자열 / `.` / `..` / `a/b` → 400 `invalid name`
- `TestRenameFileNotFound` — 미존재 경로 → 404
- `TestRenameFileIsDir` — 디렉토리 경로 전달 → 400 `not a file`
- `TestRenameFileTraversal` — `path=../escape`, `name`에 `\\` 포함 → 400 `invalid path` / `invalid name`

**완료 기준:**
- `go test ./internal/handler/... -v` 전체 PASS
- `go build ./...` 성공
- 수동 curl 예시:
  ```
  curl -X PATCH "http://localhost:8080/api/file?path=/movies/old.mp4" \
    -H "Content-Type: application/json" -d '{"name":"new"}'
  → 200 {"path":"/movies/new.mp4","name":"new.mp4"}
  ```

---

### R-2: 백엔드 — PATCH /api/folder (폴더 rename)

**파일:** `internal/handler/files.go`, `internal/handler/files_test.go`

**handleFolder에 PATCH case 추가:**
```go
case http.MethodPatch:
    h.renameFolder(w, r)
```

**`renameFolder` 동작 순서:**
1. `media.SafePath(h.dataDir, rel)` → 원본 경로 검증
2. `srcAbs == filepath.Clean(h.dataDir)` → 400 `"cannot rename root"`
3. JSON 바디 파싱
4. `validateRenameName(body.Name)` → 400
5. `os.Stat(srcAbs)` → 미존재 404, 파일이면 400 `"not a directory"`
6. 동일 이름 검사 → 400 `"name unchanged"`
7. `dstAbs := filepath.Join(filepath.Dir(srcAbs), body.Name)` + `media.SafePath` 재검증
8. `os.Stat(dstAbs)` → 존재하면 409
9. `os.Rename(srcAbs, dstAbs)` → 실패 시 500 (폴더 전체가 한 번에 이동, 하위 `.thumb/` 자동 동반)
10. 응답: `200 OK`, `{"path": relResult, "name": body.Name}`

**테스트:**
- `TestRenameFolderSuccess` — 폴더 + 하위 파일 + `.thumb/` 존재 확인 후 rename, 새 경로에 모두 남아있음
- `TestRenameFolderConflict` — 동일 디렉토리 내 같은 이름 폴더 있으면 409
- `TestRenameFolderNotFound` — 404
- `TestRenameFolderIsFile` — 파일 경로 전달 → 400 `not a directory`
- `TestRenameFolderRoot` — 빈 path / `/` → 400 `cannot rename root`
- `TestRenameFolderInvalidName` — 400
- `TestRenameFolderNameUnchanged` — 400

**완료 기준:**
- `go test ./internal/handler/... -v` 전체 PASS
- 수동 curl:
  ```
  curl -X PATCH "http://localhost:8080/api/folder?path=/movies" \
    -H "Content-Type: application/json" -d '{"name":"films"}'
  → 200 {"path":"/films","name":"films"}
  ```

---

### R-3: 프론트엔드 — rename 모달 + 버튼 + JS 로직

**파일:** `web/index.html`, `web/style.css`, `web/app.js`

**`index.html` 변경:**
- 기존 folder-create 모달 근처에 rename 모달 추가:
  ```html
  <div id="rename-modal" class="modal-overlay hidden">
    <div class="modal">
      <h3 id="rename-title">이름 변경</h3>
      <label id="rename-ext-hint" class="modal-hint"></label>
      <input id="rename-input" type="text" />
      <p id="rename-error" class="modal-error hidden"></p>
      <div class="modal-actions">
        <button id="rename-cancel">취소</button>
        <button id="rename-confirm">변경</button>
      </div>
    </div>
  </div>
  ```

**`style.css` 변경:**
- 기존 `.modal-*` 스타일 재사용. 추가로:
  - `.modal-hint { color: #888; font-size: 0.85rem; margin-bottom: 4px; }` — 확장자 안내 ("확장자: .mp4")
  - rename 버튼 아이콘은 UTF `✎` 또는 이모지 없이 텍스트 "이름 변경" 사용 (기존 "삭제" 버튼과 동일 스타일)

**`app.js` 변경:**

1. 신규 함수:
   ```js
   function openRenameModal(entry) {
     // entry: {name, path, is_dir, type}
     const isFolder = entry.is_dir;
     const baseName = isFolder ? entry.name : entry.name.replace(/\.[^.]+$/, '');
     const ext = isFolder ? '' : entry.name.slice(baseName.length);
     // title, hint, input 값 세팅, 모달 표시
     // confirm 핸들러: PATCH /api/file or /api/folder
   }

   async function submitRename(entry, newBase) {
     const url = entry.is_dir
       ? `/api/folder?path=${encodeURIComponent(entry.path)}`
       : `/api/file?path=${encodeURIComponent(entry.path)}`;
     const res = await fetch(url, {
       method: 'PATCH',
       headers: {'Content-Type': 'application/json'},
       body: JSON.stringify({name: newBase}),
     });
     if (res.ok) {
       // 모달 닫기 + loadBrowse(currentPath)
     } else {
       const err = await res.json().catch(() => ({error: 'rename failed'}));
       // 에러 메시지 표시, 모달 유지
       // 409 → "이미 같은 이름이 있습니다"
       // 400 name unchanged → "이름이 같습니다"
       // 400 invalid name → "유효하지 않은 이름입니다"
     }
   }
   ```

2. `buildTable`, `buildImageGrid`, `buildVideoGrid` 수정:
   - 각 행/카드의 action 영역에 "이름 변경" 버튼 추가, 클릭 시 `openRenameModal(entry)` 호출
   - 기존 삭제 버튼 왼쪽에 배치
   - 이벤트 버블링 방지: `e.stopPropagation()` (카드 클릭과 분리)

3. 키보드 지원:
   - Enter → confirm
   - Escape → cancel

**검증 (수동):**
- 파일 rename: base name만 수정되고 확장자 유지
- 이미지 rename 후 썸네일 유지 (깜빡임 없음)
- 동영상 rename 후 duration 오버레이 유지
- 폴더 rename 후 하위 파일 접근 가능
- 409 메시지 모달에 표시, 모달 유지
- 이름 미변경 시 400 메시지
- Escape로 취소 시 아무 변화 없음

**완료 기준:** 콘솔 에러 없이 브라우저 흐름 완료

---

### R-4: E2E 수동 검증 + 회귀 체크 (체크포인트)

1. `go run ./cmd/server` (또는 `docker compose up --build`)
2. **파일 rename:**
   - `sample.mp4` 업로드 → 이름 변경 "demo" 입력 → `demo.mp4`로 표시
   - 썸네일 + duration 오버레이 유지 확인
   - 이미지 `photo.jpg` 동일하게 테스트 (썸네일 유지)
3. **확장자 방어:** 이름 변경에 "new.mkv" 입력 → 결과 `new.mp4` (원본 확장자 유지)
4. **충돌:** `a.mp4`, `b.mp4` 있을 때 `a.mp4`를 "b"로 변경 → 409 메시지
5. **이름 미변경:** `a.mp4`를 "a"로 변경 → 400 메시지
6. **폴더 rename:** `movies/` → `films/` 변경 → 하위 파일 접근 가능, 썸네일 유지
7. **잘못된 입력:** 빈 문자열, `a/b`, `..` → 400 메시지
8. **회귀 체크:**
   - 파일/폴더 삭제 기존대로 동작
   - 폴더 생성 기존대로 동작
   - 업로드 기존대로 동작
   - 스트리밍 기존대로 동작
9. **모바일 뷰포트 (DevTools):** rename 버튼·모달 레이아웃 깨짐 없음

---

### Out of scope
- 경로 이동 (cross-directory move) — 별도 기능
- 여러 항목 일괄 rename
- 확장자 변경 허용 (MIME 일관성 훼손 위험)
- DB 메타데이터 캐시 (SPEC §5.3 노트대로 보류)

### 위험 / 롤백
- **위험 1:** rename 중 서버 크래시 → OS rename은 atomic하지만 사이드카는 별도 작업. 사이드카 rename 실패 시 원본 썸이 orphan으로 남음 → 다음 `/api/thumb` 호출 시 lazy 재생성으로 자가복구
- **위험 2:** 사용자가 확장자 입력 시 혼란 → UI 힌트("확장자: .mp4")로 완화
- **위험 3:** Windows의 대소문자 무시 파일시스템 → `a.mp4` → `A.mp4` rename은 OS가 no-op할 수 있음. 현재 스펙에서는 400 name unchanged로 거부 (case-sensitive 비교). 리눅스 컨테이너 환경이 주 대상이므로 acceptable.
- **롤백:** Phase 9의 commit revert. 영속 데이터 변경 없음 (사이드카는 .thumb/ 내부, 정적 서빙 영향 없음)
