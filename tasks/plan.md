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
| `internal/handler/files.go` | `handleFile` 메서드 스위치 확장 (DELETE→DELETE/PATCH), `handleFolder`에 PATCH case 추가, `renameFile`/`renameFolder`/`validateName`(통합) 신규 |
| `internal/handler/files_test.go` | rename 케이스 추가 (성공/409/400/404/traversal/사이드카) |
| `web/index.html` | rename 모달 요소 추가 |
| `web/style.css` | 기존 `.modal` 스타일 재사용, 필요 시 rename 버튼 아이콘만 |
| `web/app.js` | `openRenameModal`/`submitRename` 함수, `buildTable`/`buildImageGrid`/`buildVideoGrid`에 rename 버튼 추가 |

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
6. `validateName(newBase)` — 빈 문자열, `/`·`\\`, `.`/`..`, 길이 > 255 → 400 `"invalid name"`. 재부착 후 `len(newName) > 255` 다시 확인 (확장자 포함 길이 초과 방지)
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
func validateName(name string) error {
    if name == "" || name == "." || name == ".." { return fmt.Errorf("invalid name") }
    if len(name) > 255 { return fmt.Errorf("invalid name") }
    for _, c := range name {
        if c == '/' || c == '\\' { return fmt.Errorf("invalid name") }
    }
    return nil
}
// validateFolderName을 validateName으로 rename하여 폴더 생성·파일/폴더 rename 공용
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
4. `validateName(body.Name)` → 400
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
## Phase 10 — URL Import (이미지 다운로드 + 업로드) (`feature/url-image-import`)

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

---

## Phase 11 — URL Import 확장: 동영상/음악 + SSE 프로그래스 (`feature/url-image-import` 연장)

### 배경
SPEC §2.6 / §5.1 개정. 기존 이미지 전용 URL import를:
- Content-Type 허용 목록 확장 (video/* 4종 + audio/* 6종)
- 크기 캡 50MB → **2 GiB**
- 총 타임아웃 60s → **10분**
- 응답을 JSON batch → **SSE 스트림** (URL별 `start`/`progress`/`done`/`error` + 마지막 `summary`)
- UI는 URL별 프로그래스 바 실시간 업데이트

Phase 9 구현 파일을 in-place 수정. 브랜치 유지 (`feature/url-image-import`), 기존 엔드포인트 API 계약 변경 (아직 main 머지 전이라 호환 불필요).

### 결정 사항 (SPEC과 정렬)
- **순차 batch 유지**: SSE 스트리밍으로 진행 가시성이 생기므로 동시성 병렬화 불필요. 단순성 우지.
- **progress throttle**: 1 MiB 누적 또는 250 ms 경과 시점 먼저 도달한 쪽에서 방출. 동일 `received` 중복 제거.
- **빈 파일명 fallback**: 타입별 기본값 (`image`/`video`/`audio`) + 확장자.
- **썸네일**: 이미지·동영상만 `thumbPool.Submit`, 음악은 skip.
- **SSE 4xx 처리**: 요청 자체가 거부되면 SSE 시작 전 일반 JSON 에러 응답 (400/404/405). 스트림 시작 후 문제는 `error` 이벤트로만 전달.

### 의존성 그래프

```
URL-V1 (urlfetch 확장: Content-Type allowlist + 2 GiB + 10분 + progress 콜백)
  └── URL-V2 (handler: SSE 스트리밍 응답)
       └── URL-V3 (frontend: SSE 소비 + URL별 프로그래스 바)
            └── URL-V4 (E2E 수동 검증)
```

URL-V1 → URL-V2 → URL-V3 순차. 각 단계에서 commit.
체크포인트: URL-V2 완료 시 `curl -N`으로 SSE 이벤트 raw 확인 → 통과해야 URL-V3 진행.

---

### URL-V1: `urlfetch` 확장 — 미디어 타입 + 용량/시간 + progress

**파일:** `internal/urlfetch/fetch.go`, `internal/urlfetch/fetch_test.go`

**변경 — constants:**
- `MaxBytes`: `50 << 20` → `2 << 30` (2 GiB = `2147483648`)
- `TotalTimeout`: `60 * time.Second` → `10 * time.Minute`
- `DialTimeout`: 10s 유지

**변경 — Content-Type 맵 (`contentTypeToExt`):**
```go
var contentTypeToExt = map[string]string{
    "image/jpeg":        ".jpg",
    "image/png":         ".png",
    "image/webp":        ".webp",
    "image/gif":         ".gif",
    "video/mp4":         ".mp4",
    "video/x-matroska":  ".mkv",
    "video/x-msvideo":   ".avi",
    "video/mp2t":        ".ts",
    "audio/mpeg":        ".mp3",
    "audio/flac":        ".flac",
    "audio/aac":         ".aac",
    "audio/ogg":         ".ogg",
    "audio/wav":         ".wav",
    "audio/mp4":         ".m4a",
}
```

**변경 — `Result.Type` 도출:** `media.DetectType(finalName)` 호출로 `"image"/"video"/"audio"` 자동 결정 (하드코딩 `"image"` 제거).

**변경 — 빈 파일명 fallback:**
```go
defaultBase := map[string]string{
    "image/": "image", "video/": "video", "audio/": "audio",
}
// sanitized가 비었으면 defaultBase[prefix] 사용
```

**신규 — progress 콜백 시그니처:**
```go
type ProgressFunc func(received int64)

// Fetch 시그니처 확장 (progress는 nil 허용)
func Fetch(ctx context.Context, client *http.Client,
           rawURL, destDir, relDir string,
           progress ProgressFunc) (*Result, *FetchError)
```

**구현 — throttled progress wrapper (internal):**
- `io.Copy` 중 주기적으로 progress 보고
- 방식: `progressReader`를 `tmpFile`과 `resp.Body` 사이에 끼우고, 내부에서 카운터 + 마지막 방출 시각/바이트 기록
- 트리거: `received - lastEmitted >= 1 MiB` OR `time.Since(lastEmittedAt) >= 250 ms`
- 동일 값이면 방출 안 함
- `progress == nil`이면 일반 `io.Copy` 경로 (오버헤드 0)

**테스트 추가 (`fetch_test.go`):**
- `TestFetch_OK_MP4` — `video/mp4` 응답 → `type: "video"` + `.mp4` 저장
- `TestFetch_OK_MP3` — `audio/mpeg` 응답 → `type: "audio"` + `.mp3` 저장
- `TestFetch_OK_MKV` — `video/x-matroska` + URL `.mp4` → 확장자 교체 `.mkv` + `extension_replaced`
- `TestFetch_DefaultName_Video` — URL `?id=x` + `video/mp4` → 저장명 `video.mp4`
- `TestFetch_DefaultName_Audio` — URL `?id=x` + `audio/mpeg` → 저장명 `audio.mp3`
- `TestFetch_ContentLengthTooLarge_2GiB` — Header = 2GiB+1 → `too_large`
- `TestFetch_ContentLengthOk_2GiB` — Header = 2GiB 정확히 → 통과 (스트림 테스트는 skip — 실데이터 2GiB는 unit test 부적합; 스트림 초과는 작은 값으로 대체)
- `TestFetch_Progress_Emitted_ForLargePayload` — 3 MiB 페이로드 + progress 콜백 → callback ≥ 1회 호출, `received` 단조 증가, 마지막 `received <= Result.Size`
- `TestFetch_Progress_NotEmitted_ForSmallPayload` — 10 KiB + 250ms 완료 → callback 0~1회만
- `TestFetch_Progress_NilCallback_OK` — callback nil → 에러 없음, 결과 정상

**기존 테스트 호환성:**
- `TestFetch_ContentLengthTooLarge` 등 50MB 경계 기반 케이스는 2 GiB 기준으로 숫자만 수정 (또는 작은 경계값으로 재구성: mock server에서 `Content-Length: 3145728` 선언 + 실제 4 MiB 스트림 → `too_large`, MaxBytes를 테스트 hook으로 덮지 않고 실제 상수 사용이면 너무 큼 — **결정: 테스트 전용 상수 override는 피하고, `too_large` 케이스는 header 값만 선언(`Content-Length: 3221225473`)해서 body 미전송 + 사전 거부로 검증**)
- `TestFetch_NonImageContentType` → `TestFetch_UnsupportedContentType` 으로 이름 변경, `text/html` 그대로 유지

**완료 기준:**
- `go test ./internal/urlfetch/... -v` 전체 PASS (기존 + 신규)
- `go build ./...` 성공
- `Fetch` 시그니처 변경에 따른 `handler/import_url.go` 호출부 1군데 수정 (progress nil 전달하면 기존 동작 유지 — URL-V2에서 덮어씀)

---

### URL-V2: `handleImportURL` — SSE 스트리밍 응답

**파일:** `internal/handler/import_url.go`, `internal/handler/import_url_test.go`

**응답 전환:**
- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `X-Accel-Buffering: no` (reverse proxy buffering 방지)
- 각 이벤트: `data: {json}\n\n`, 이벤트 이름 생략
- `http.Flusher` 캐스팅 후 매 이벤트 후 `Flush()` 호출

**흐름:**
1. 기존 4xx 분기(path/body/empty/too-many)는 그대로 JSON `{"error":"..."}` + 상태코드 반환 (SSE 시작 전)
2. 검증 통과 후 SSE 헤더 기록 + Flush (응답 시작 표시)
3. 각 URL 순차:
   - `start` 이벤트 — 이 URL의 기초 정보. 다만 `total`/`type`은 `Fetch` 호출 후에야 알 수 있음 → 두 가지 옵션:
     a. `start`를 `Fetch` 직전에 `{phase:"start", index, url}`만 방출, 진행 중 `progress` 방출, `done`에서 최종 정보 전달
     b. `start`를 Content-Length 검증 통과 후 `urlfetch`가 콜백으로 알려줌 → 더 풍부한 `start` 가능
   - **선택: 옵션 b**. `urlfetch.Fetch`에 `StartFunc func(total int64, mediaType string)` 콜백 추가 (nil 허용). progress와 별개.
4. `progress` 이벤트 — URL-V1의 progress 콜백에서 `{phase:"progress", index, received}` 방출
5. `done` 이벤트 — `Fetch` 성공 시 Result를 phase=done으로 직렬화. 이어서 `thumbPool.Submit` (이미지/동영상만)
6. `error` 이벤트 — `FetchError` 시 `{phase:"error", index, url, error}` 방출
7. 전체 루프 끝 후 `summary` 이벤트 방출

**새로운 urlfetch API (V1이 시그니처 바꿨으므로 확정):**
```go
type Callbacks struct {
    Start    func(total int64, mediaType string)
    Progress func(received int64)
}
func Fetch(ctx, client, rawURL, destDir, relDir string, cb *Callbacks) (*Result, *FetchError)
```
→ URL-V1에서 `ProgressFunc`만 추가했지만 V2 설계와 정합을 위해 **URL-V1 단계에서 `*Callbacks` 구조체로 확정**. V1 테스트도 이 API로 작성.

**취소 처리:**
- 클라이언트 disconnect → `r.Context()` 취소 → 현재 `Fetch`의 `ctx` 취소 → `io.Copy` 중단 → `error` 이벤트 방출 시도 실패는 무시하고 함수 종료

**테스트 (`import_url_test.go`):**
- `parseSSEEvents(body string) []map[string]any` 헬퍼 추가
- `TestImportURL_Single_OK` (기존) → SSE 파싱으로 재작성: `start`/`done`/`summary` 순서 + `done.path` 확인
- `TestImportURL_Multiple_PartialSuccess` (기존) → SSE 파싱: 3개 URL 각각 이벤트 확인
- `TestImportURL_Video_OK` — video mock origin → `start.type="video"` + `done.type="video"`
- `TestImportURL_Audio_OK` — audio mock origin → `done.type="audio"` + `.thumb/` **미생성** (음악은 thumb skip)
- `TestImportURL_Progress_Emitted` — 3 MiB 페이로드 → progress 이벤트 ≥1개 + `received` 단조 증가
- `TestImportURL_Summary_Last` — 이벤트 순서: 마지막이 `summary`, `succeeded + failed == len(urls)`
- `TestImportURL_EmptyArray` / `TooMany` / `PathTraversal` / `PathNotFound` / `MethodNotAllowed` / `InvalidBody` — 기존 JSON 에러 경로 유지 (변경 없음)

**완료 기준:**
- `go test ./internal/handler/... -v` 전체 PASS
- `curl -N -X POST -H 'Content-Type: application/json' -d '{"urls":["https://example.com/img.jpg"]}' "http://localhost:8080/api/import-url?path=/"` → 이벤트 순차 출력 확인 (`start`/`done`/`summary`)

**체크포인트:** SSE raw 검증 통과 후에만 URL-V3 진행.

---

### URL-V3: 프론트엔드 — SSE 소비 + 프로그래스 바

**파일:** `web/index.html`, `web/style.css`, `web/app.js`

**`index.html` 변경:**
- 모달 hint 텍스트: "최대 50개, **2GB/파일**"
- placeholder 예시 갱신: 이미지/동영상/음악 혼합 예
- 버튼 라벨: "URL에서 이미지 가져오기" → **"URL에서 가져오기"**
- `#url-result` 내부 구조 변경: 리스트가 아닌 **URL별 진행 행**:
  ```html
  <div class="url-row" data-index="0">
    <div class="url-row-head">
      <span class="url-row-name" title="{full}">...</span>
      <span class="url-row-status">대기 중</span>
    </div>
    <div class="url-row-bar"><div class="url-row-fill" style="width:0%"></div></div>
  </div>
  ```

**`style.css` 신규/수정:**
- `.url-row` — 행 레이아웃
- `.url-row-head` — 이름/상태 flex
- `.url-row-bar` / `.url-row-fill` — 프로그래스 바 (상태별 색: 진행 중 accent, 완료 green, 실패 danger)
- `.url-row.done .url-row-fill { background: var(--accent-success, #4caf50) }`
- `.url-row.error .url-row-fill { background: var(--danger) }`
- `.url-row-status` — 작은 폰트, 상태에 따라 색 변화

**`app.js` 변경:**
- `submitURLImport` 재작성:
  ```js
  const resp = await fetch('/api/import-url?path=...', {method:'POST', ...});
  if (!resp.ok) { // 4xx JSON error
    showURLError(await resp.text());
    return;
  }
  // URL별 행 미리 렌더
  urlResult.innerHTML = urls.map((u, i) => rowHTML(i, u)).join('');
  urlResult.classList.remove('hidden');

  // SSE 파서
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  while (true) {
    const {value, done} = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, {stream: true});
    const frames = buffer.split('\n\n');
    buffer = frames.pop(); // 미완성 마지막 프레임 보관
    for (const f of frames) {
      if (!f.startsWith('data:')) continue;
      const ev = JSON.parse(f.slice(5).trim());
      handleEvent(ev);
    }
  }
  ```
- `handleEvent(ev)`:
  - `start`: 행의 total 저장, 상태 "다운로드 중", 파일명 표시 (`ev.name`), 총 크기 표시 (`ev.total`)
  - `progress`: `received/total * 100`% → `url-row-fill.style.width`. total 없으면 indeterminate (CSS animation)
  - `done`: 100%, `.url-row` 에 `done` 클래스, 상태 "완료 · {size}"
  - `error`: `.url-row` 에 `error` 클래스, fill 100% + red, 상태 라벨 (기존 한국어 map 재사용 + 신규 코드 없음)
  - `summary`: 모달 하단에 `성공 N개 · 실패 M개` 배지 표시, 버튼 라벨 원복
- `closeURLModal`: 기존 로직 유지 (`urlAnySucceeded`면 browse 새로고침)
- formatSize 헬퍼 활용 (기존 `formatSize` 있음 — app.js line 472)

**검증:**
- 브라우저에서 이미지 1개 → 행 하나 0→100% 빠르게 완료
- 대용량 동영상 URL → 0→100% 실시간 증가 (progress 이벤트 반영)
- 혼합 케이스: 행마다 상태/색상 분기
- 4xx (빈 URL, 50+개): 인라인 에러만 (SSE 시작 전)

**완료 기준:** 로컬 서버에서 수동 확인 + `?v=4` → `?v=5` 캐시 버스트.

---

### URL-V4: E2E 수동 검증 (체크포인트)

1. `docker compose -p file_server up --build -d` 재빌드
2. 모달 열기 → 버튼 라벨 "URL에서 가져오기" 확인, hint에 "2GB" 표시
3. 이미지 URL 1개 → 행 표시 → 완료 → 닫기 → 그리드 반영 + 썸네일 생성
4. MP4 동영상 URL (수백 MB) → 진행률 실시간 증가 → 완료 → `/data/{name}.mp4` 저장 + `.thumb/` 생성
5. MP3 음악 URL → 완료 → `.thumb/` **생성 안 됨** 확인
6. 혼합 배치 (이미지/동영상/음악/실패 1개) → 각 행 개별 상태 + summary 카운트 일치
7. 2 GiB 초과 Content-Length 선언 URL → `error: too_large` + 행 빨강
8. 지원 외 Content-Type (text/html) → `error: unsupported_content_type`
9. 네트워크 타임아웃 (10분 초과 시뮬레이션) — optional: mock용 느린 origin 로컬 실행
10. 모바일 뷰: 행 레이아웃 깨짐 없음, 프로그래스 바 가독성

---

### Out of scope (이번 단계도 제외)
- 다운로드 중 취소 버튼 (행별 abort)
- 병렬 다운로드
- 재개 (Range resume)
- duration_sec/동영상 썸네일 품질 검증 (기존 VT Phase에서 담당)
- SSRF 강방어

---

### 위험 / 롤백
- **위험: SSE 버퍼링** — nginx 등 프록시에서 `X-Accel-Buffering: no` 무시 시 progress 지연. 현재는 직접 연결이라 문제 없음. 배포 환경 바뀌면 확인.
- **위험: 2 GiB 파일 장시간 점유** — 10분 타임아웃 내 완료 못 하면 실패. 의도된 UX (라인 속도 = 2GB/10min = 약 27 Mbps 필요). 필요 시 타임아웃 조정.
- **위험: `io.Copy` 중 클라이언트 disconnect** — `r.Context()` 취소로 `ctx` 연쇄 취소 → `io.Copy` EOF → 다음 URL 진입 전 Fetch 루프 조기 종료. 서버 측 goroutine 누수 없음.
- **롤백:** URL-V1~V3 commit revert. Phase 9 상태로 복귀 (이미지 전용 JSON batch). 기존 URL-V4 manual은 다시 필요.

---

## Phase 13 — HLS URL Import (`feature/hls-url-import`)

SPEC.md §2.6.1에 따라 HLS(`.m3u8`) 스트림을 URL Import 경로에 통합한다. `Content-Length` 사전 검증이 불가능한 HLS는 ffmpeg `-c copy` 리먹싱으로 단일 MP4로 저장한다.

### 의존성 그래프

```
[urlfetch 확장 — 모두 순수 Go, 각 단계 단위 테스트 가능]
  H1: HLS 감지 + 마스터 플레이리스트 파서
       ↓
  H2: ffmpeg 리먹싱 러너 (spawn / size watcher / kill / atomic rename)
       ↓
  H3: Fetch() 내부에 HLS 분기 통합 (H1 + H2 wiring, 파일명·warnings·type)
       ↓
[handler + 프론트엔드 — HLS가 `total`을 내지 않는 점을 반영]
  H4: sseStart.Total json `omitempty` (HLS에서 0 → JSON에서 생략)
       ↓
  H5: frontend — `total` 부재 시 indeterminate 프로그래스, 신규 에러 라벨
       ↓
[검증]
  H6: E2E 수동 검증 (공개 HLS 샘플 스트림 + mislabeled CT 케이스)
```

모든 변경은 기존 URL Import 경로(`Fetch`의 non-HLS 분기)를 건드리지 않도록, HLS 감지를 초기 분기점으로 추가한다.

---

### H1 — urlfetch: HLS 감지 + 마스터 플레이리스트 파서

**파일:** `internal/urlfetch/hls.go` (신규), `internal/urlfetch/hls_test.go` (신규)

**범위:**
- `isHLSResponse(mediaType string, urlPath string) bool`
  - `application/vnd.apple.mpegurl`, `application/x-mpegurl` (대소문자 무시)
  - 폴백: `urlPath`가 `.m3u8`로 끝나고 `mediaType`이 `"", "text/plain", "application/octet-stream"` (및 파싱 실패로 빈 문자열 받은 경우)
- `parseMasterPlaylist(body []byte, base *url.URL) (variantURL *url.URL, err error)`
  - 본문 크기 ≤ 1 MiB 가정 (초과는 호출자가 사전 거부 — H3에서 `io.LimitReader`로 처리)
  - `#EXT-X-STREAM-INF:` 라인 파싱 → 다음 URL 라인이 variant
  - `BANDWIDTH=` 속성 비교 → 최고값 선택, 동률은 먼저 선언된 것 유지
  - 누락 variant는 0으로 간주 (후순위)
  - `#EXT-X-STREAM-INF`가 전혀 없으면 → `base` URL 그대로 반환 (media playlist로 간주)
  - 상대 URL은 `base.ResolveReference(…)`로 절대화
  - variant URL 스킴이 `http`/`https`가 아니면 에러 (이중 방어)
- 전용 에러: `errHLSPlaylistTooLarge`, `errHLSVariantScheme` (FetchError 코드로 변환은 H3에서)

**완료 기준:**
- `go test ./internal/urlfetch -run TestHLS` 통과
- 테스트 케이스:
  - `isHLSResponse`: vnd CT / x-mpegurl CT / 대소문자 / 파라미터 포함 (`application/vnd.apple.mpegurl; charset=utf-8`) / `.m3u8` + text/plain 폴백 / `.m3u8` + octet-stream 폴백 / 폴백 Content-Type이 `video/mp4`면 거부 / `.m3u8` 대문자 확장자
  - `parseMasterPlaylist`: 3개 variant 중 최고 BANDWIDTH 선택 / 동률 시 첫 번째 / BANDWIDTH 누락 variant 후순위 / 상대 URL resolve / 절대 URL 그대로 / `#EXT-X-STREAM-INF` 없으면 base 반환 / `file://` variant 거부 / 빈 본문 → base 반환 (media playlist 간주)

**검증:** `go test ./internal/urlfetch/... -run TestHLS -v`

**공개 API 변경 없음** — H1은 internal helper만 추가.

---

### H2 — urlfetch: ffmpeg 리먹싱 러너

**파일:** `internal/urlfetch/hls.go` (H1에 추가)

**범위:**
- `runHLSRemux(ctx context.Context, variantURL string, tmpPath string, cb *Callbacks) error`
  - `exec.CommandContext(ctx, "ffmpeg", …)` 로 spawn
  - 인자: `-hide_banner -loglevel error -protocol_whitelist "http,https,tls,tcp,crypto" -i <variantURL> -c copy -bsf:a aac_adtstoasc -f mp4 -movflags +faststart <tmpPath>`
  - stderr는 `bytes.Buffer`로 캡처 (실패 시 로그)
  - **사이즈 watcher goroutine**: 500 ms 주기로 `os.Stat(tmpPath)` → 현재 크기를 `cb.Progress`로 전달 (throttling은 기존 `progressReader` 규칙과 동일 — H2 안에서 시간·바이트 두 threshold 체크)
  - 사이즈가 `MaxBytes`(2 GiB) 초과 → `cmd.Process.Kill()` + 에러 반환 (`errTooLarge`)
  - `ctx` 취소 → `CommandContext`가 자동 kill
  - `cmd.Wait()` exit code non-zero → `errFFmpegExit{stderr}` 반환
  - 호출자는 성공 시 파일 닫고 rename까지 책임
- 스택가드:
  - ffmpeg 미설치 환경 → `exec.LookPath` 단계에서 감지 → `errFFmpegMissing` (코드 변환: `ffmpeg_error`)
  - 임시 파일 생성·정리는 H3(Fetch wiring)에서 — H2는 path만 받음

**완료 기준:**
- `go test ./internal/urlfetch -run TestHLSRemux` 통과
- 테스트 전략:
  - ffmpeg 미설치 시 `t.Skip("ffmpeg not found")` — 기존 stream_test.go와 동일 패턴
  - 소형 HLS fixture 생성: ffmpeg로 테스트 시작 시 MP4 → TS 세그먼트 3개 + m3u8 playlist를 `httptest.Server`로 호스팅
  - 케이스:
    - 정상 리먹싱 → tmp 파일에 MP4 시그니처(`ftyp` box) 쓰여있음
    - `ctx` 취소 → 프로세스 즉시 종료, 에러는 `context.Canceled`
    - 2 GiB 초과 시뮬레이션: MaxBytes를 테스트에서 32 KiB로 override(테스트 전용 `WithMaxBytes` helper 추가) → kill + `errTooLarge` 반환
    - ffmpeg 종료 코드 non-zero (잘못된 variant URL) → `errFFmpegExit`, stderr 메시지 포함
    - progress 콜백 호출 ≥ 1회 (출력 파일이 1 MiB 이상일 때)

**검증:** `go test ./internal/urlfetch/... -run TestHLSRemux -v`

---

### H3 — urlfetch.Fetch: HLS 분기 통합 (체크포인트)

**파일:** `internal/urlfetch/fetch.go`, `internal/urlfetch/fetch_test.go`

**변경:**
- `Fetch`의 Content-Type 검증 전에 HLS 분기:
  ```go
  mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
  if isHLSResponse(mediaType, parsed.Path) {
      return fetchHLS(ctx, resp, parsed, destDir, relDir, warnings, cb)
  }
  // 기존 흐름 (Content-Length, Content-Type 허용 목록, io.Copy)
  ```
- `fetchHLS(ctx, resp, parsed, destDir, relDir, warnings, cb)` 신규:
  1. 본문을 `io.LimitReader(resp.Body, 1<<20 + 1)` 로 읽기 → 1 MiB 초과면 `FetchError{Code: "hls_playlist_too_large"}`
  2. `parseMasterPlaylist(body, parsed)` 호출 → variant URL 획득 (실패 시 `ffmpeg_error` 또는 구분된 코드)
  3. variant URL 스킴 재검증 (`http`/`https`만)
  4. 파일명: URL 마지막 세그먼트에서 base name 추출 (`sanitizeFilename` + `TrimSuffix`로 확장자 제거) → `.mp4` 부착. 추출 불가 시 `video.mp4`
  5. `warnings = append(warnings, "extension_replaced")` 항상 추가 (`.m3u8` → `.mp4`)
  6. `fileType := "video"` 고정
  7. `cb.Start(name, 0, "video")` — total을 0으로 전달 (H4에서 JSON `omitempty` 처리)
  8. `os.CreateTemp(destDir, ".urlimport-*.tmp")` 생성, 경로만 `runHLSRemux`에 전달 (ffmpeg 자체가 파일을 열어 쓰기)
     - 주의: ffmpeg가 파일을 열기 위해 tmp 파일을 먼저 삭제하거나 덮어써야 할 수 있음 → `tmpFile.Close()` 한 뒤 `os.Remove(tmpPath)` 하고 경로만 넘기는 방식이 안전. 최종 rename 직전 tmp 존재 확인.
     - 또는: `-y` 플래그로 overwrite 허용
  9. H2의 `runHLSRemux(ctx, variantURL.String(), tmpPath, cb)` 호출
  10. 에러 매핑: `errTooLarge` → `too_large`, `errFFmpegMissing`/`errFFmpegExit` → `ffmpeg_error`, `errHLSPlaylistTooLarge` → `hls_playlist_too_large`, `errHLSVariantScheme` → `invalid_scheme`
  11. 성공 시 `renameUnique(tmpPath, destDir, name)` 호출 (기존 함수 재사용)
  12. 최종 크기는 `os.Stat`으로 확정, `Result{Size: stat.Size(), Type: "video", Warnings: warnings}` 반환
- `FetchError` 코드 목록 업데이트 (문서용, 컴파일러가 강제 안 함)

**완료 기준:**
- 기존 `fetch_test.go` 모든 테스트 그대로 통과 (회귀 없음)
- 신규 통합 테스트 (httptest.Server로 m3u8 + .ts 세그먼트 호스팅):
  - 정상 media playlist + 3 세그먼트 → MP4 저장, `Result.Type == "video"`, `Result.Name == "<base>.mp4"`, warnings 에 `extension_replaced` 포함
  - 정상 master playlist (2 variants) → 높은 BANDWIDTH의 media playlist 선택 후 세그먼트 다운로드 확인 (세그먼트 요청 로그로 검증)
  - Content-Type `text/plain` + URL `.m3u8` → HLS 분기 진입 확인 (MP4 저장됨)
  - 대문자 확장자 `.M3U8` → HLS 분기 진입 확인
  - 1 MiB + 1 byte 본문 → `error: "hls_playlist_too_large"`
  - master playlist의 variant가 `file:///etc/passwd` → `error: "invalid_scheme"`, tmp 정리 확인
  - ffmpeg 미설치 시 skip (`t.Skip`)
  - HLS 감지되지만 본문이 `#EXTM3U` 없이 빈 플레이리스트 → media playlist로 간주, ffmpeg가 실패 → `ffmpeg_error`
- `go test ./... ./internal/urlfetch/...` 통과

**검증 (체크포인트):**
- `go test ./... -count=1`
- 수동: `curl -N -X POST 'http://localhost:8080/api/import-url?path=/movies' -H 'Content-Type: application/json' -d '{"urls":["https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"]}'` 로 SSE 스트림 확인 (`start` 이벤트에 `total` 없음, `progress` 여러 번, `done` 이후 MP4 저장)
- **여기까지 백엔드 완결** — H4/H5 진입 전 수동 검증으로 중단 가능

---

### H4 — sseStart.Total JSON `omitempty`

**파일:** `internal/handler/import_url.go`, `internal/handler/import_url_test.go`

**변경:**
- `sseStart` 구조체:
  ```go
  type sseStart struct {
      Phase string `json:"phase"`
      Index int    `json:"index"`
      URL   string `json:"url"`
      Name  string `json:"name"`
      Total int64  `json:"total,omitempty"`  // HLS 경로에서는 0 → 필드 생략
      Type  string `json:"type"`
  }
  ```
- SPEC.md §5.1의 "알 수 없으면 생략" 규칙에 부합 — 기존 동영상/이미지/음악은 Content-Length로 total>0이라 동작 변화 없음
- 기존 테스트 중 `total` 을 0으로 넣어 검증하던 케이스가 있다면 수정 (`TestHandleImportURL_...` 전수 점검)

**완료 기준:**
- 기존 `import_url_test.go` 통과
- 신규 단위 테스트: `sseStart{Total:0}` JSON 마샬 → `total` 키 부재 확인
- 신규 통합 테스트 (HLS path): SSE 스트림의 `start` 이벤트에 `total` 필드 없음을 확인 (`json.RawMessage` 파싱 후 `_, ok := obj["total"]; !ok`)

**검증:** `go test ./internal/handler/... -run TestHandleImportURL -count=1`

---

### H5 — frontend: indeterminate progress + 신규 에러 라벨

**파일:** `web/app.js`, `web/style.css`, `web/index.html` (버전 쿼리 버스트)

**변경:**
- `URL_ERROR_LABELS`에 추가:
  ```js
  ffmpeg_error: 'HLS 리먹싱 실패',
  hls_playlist_too_large: 'HLS 플레이리스트 크기 초과',
  ```
- `handleSSEEvent` `start` 분기:
  - `ev.total` 이 undefined 또는 0 → 행에 `indeterminate` 클래스 추가 (`row.classList.add('url-row-indeterminate')`)
  - status 텍스트: `${ev.type} · 크기 미상 (HLS)` (기존 `크기 미상` 문구 재사용, `type==='video'` 조건에서 HLS 라벨 추가는 optional)
- `handleSSEEvent` `progress` 분기:
  - total===0일 때 기존 코드가 이미 `received` 바이트만 표시 — 유지
  - **indeterminate bar**: CSS animation으로 움직이는 스트라이프나 좁은 bar를 좌우로 shuttling
- `handleSSEEvent` `done` 분기:
  - `url-row-indeterminate` 클래스 제거 + fill width 100% 고정 (기존 동작 유지)
- `style.css`:
  ```css
  .url-row-indeterminate .url-progress-fill {
    width: 40% !important;
    animation: url-indeterminate 1.2s ease-in-out infinite;
    background: var(--accent, #4a90e2);
  }
  @keyframes url-indeterminate {
    0%   { transform: translateX(-100%); }
    100% { transform: translateX(250%); }
  }
  ```
- `index.html` 의 `<script src="app.js?v=N">` 버전 번호 +1

**완료 기준:**
- 수동 테스트: total 없는 start 이벤트 → 바 왼→오 애니메이션 → done 시 고정
- 에러 라벨 렌더: ffmpeg_error → `실패 · HLS 리먹싱 실패`

**검증:**
- Chrome DevTools로 SSE 이벤트 주입(`fetch` 모킹) 또는 실제 HLS URL 확인

---

### H6 — E2E 수동 검증 (체크포인트)

1. `docker compose -p file_server up --build -d`
2. 공개 HLS 샘플 URL로 모달에서 가져오기:
   - `https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8` (master playlist, VOD)
   - 기대: `start` → indeterminate 바 → 일정 시간 뒤 `done` → `/data/x36xhzz.mp4` 존재 + `.thumb/x36xhzz.mp4.jpg` 생성 + duration 사이드카 생성
3. media playlist 단독 URL (master 없는 경우) — 예: 서버에 `test.m3u8` + .ts fixture를 로컬 호스팅 후 `http://localhost:9999/test.m3u8` 넣기
4. CDN mislabel 시뮬: 로컬 origin이 `.m3u8`을 `text/plain`으로 내려주도록 설정 → 여전히 HLS 분기 진입
5. Live stream — 예: `https://mux.com/…/live.m3u8` (없으면 생략). 10분 tick 도달 → `error: download_timeout`, tmp 정리 확인
6. `file:///etc/passwd` 담긴 master playlist를 로컬에서 만들어 테스트 → `invalid_scheme` 에러
7. 혼합 배치: 이미지 + HLS + 실패 URL → summary 카운트 일치, HLS만 indeterminate
8. 모바일 뷰: indeterminate bar 가독성, 라벨 wrap
9. DevTools로 네트워크 보며 SSE 이벤트 순서 확인 (start → progress 여러 개 → done) — `total` 필드 없음 검증

**완료 기준:** 위 9개 케이스 모두 기대 동작 확인 + `git status` clean → PR 생성 가능.

---

### Out of scope (Phase 13)
- DASH(`.mpd`) 지원 — SPEC Never 명시
- DRM / FairPlay / HLS AES-128 암호화 세그먼트
- Live HLS 특별 처리 (DVR window 인식, Re-run 등) — 공통 timeout으로만 cap
- HLS 원본(`.m3u8` + `.ts`) 그대로 저장
- variant 수동 선택 UI (항상 최고 BANDWIDTH)
- ffmpeg 재인코딩 (`-c copy` 리먹싱만)

### 위험 / 롤백
- **위험: ffmpeg 없는 환경** — Alpine 이미지에 이미 설치되어 있음 (Phase 5). 로컬 개발용 Windows/Mac에서 ffmpeg 없으면 HLS import만 실패하고 기존 URL import는 그대로 동작.
- **위험: HLS variant URL이 인증 필요** — 인증 헤더 자동 첨부 안 하므로 `ffmpeg_error` 로 실패. 의도된 동작.
- **위험: ffmpeg가 세그먼트 스트림을 stdout이 아닌 파일로 쓰는 동안 tmp 파일 크기 급증** — 2 GiB watcher가 500 ms 주기로 감시 → 약 2 GiB 근처까지는 초과 가능. 허용 오차 수준.
- **위험: 1 MiB 제한이 실전 master playlist에 너무 타이트?** — 실전은 보통 수 KiB. 1 MiB는 defense-in-depth, 실 부족 사례 발견 시 조정.
- **롤백:** H1~H5 commit revert → Phase 12 상태 (이미지/동영상/음악 URL import, HLS 거부) 복귀. 기존 테스트 그대로.

---

## Phase 14 — 정렬·필터 툴바 (`feature/sort-filter`) — spec [`SPEC.md §2.5.2`](../SPEC.md)

**목표:** `#file-list` 위에 타입 세그먼트 + 이름 검색 + 정렬 select로 구성된 툴바를 추가하고, 상태를 URL 쿼리(`sort`, `q`, `type`)로 동기화한다. 서버 변경 없음.

### 의존성 그래프

```
  S1 (상태 모델)
    ├──► S2 (HTML/CSS 툴바 shell)
    │      ├──► S3 (app.js 이벤트 + URL 동기)
    │      │      └──► S4 (renderFileList 정렬·필터 적용)
    │      │             └──► S5 (라이트박스·오디오 연동 + 빈 상태 문구)
    │      │                    └──► S6 (E2E 수동 검증 — 체크포인트)
```

각 슬라이스는 "코드 변경 + 최소 검증"으로 독립 verifiable. S2까지는 쓰레기 상태(툴바 UI만 있고 동작 안 함)로 남으면 안 되므로 S2·S3·S4는 **한 커밋**으로 묶어 배포 가능한 최소 슬라이스를 만든다.

---

### S1 — 상태 모델 설계 (문서)

**파일:** 없음 (plan.md에 이미 설계 포함)

**상태 객체:**
```js
const view = {
  sort: 'name:asc',  // 'name:asc'|'name:desc'|'size:asc'|'size:desc'|'date:asc'|'date:desc'
  q: '',             // trim된 검색어, 소문자 비교는 비교 시점에 수행
  type: 'all',       // 'all'|'image'|'video'|'audio'|'other'
};
let allEntries = []; // 원본 entries (필터/정렬 전). browse() 성공 시 갱신.
```

**URL 매핑:**
- `sort` 파라미터 누락 → `'name:asc'`, 허용 값 밖 → 기본값으로 fallback 후 URL에서 제거(replaceState)
- `q` 누락 → `''`
- `type` 누락 또는 허용값 밖 → `'all'`

**완료 기준:** SPEC.md §2.5.2의 "URL 파라미터" 및 "정렬 규칙" 항목과 일치. 구현 슬라이스에서 이 설계를 따른다.

---

### S2+S3+S4 — 툴바 UI + 이벤트 + 정렬·필터 적용 (한 커밋)

**파일:** `web/index.html`, `web/style.css`, `web/app.js`

**index.html:**
- `<main>` 아래 `<section id="upload-zone">` 다음, `<section id="file-list">` 앞에 툴바 삽입:
  ```html
  <div id="browse-toolbar" class="browse-toolbar">
    <div class="toolbar-types" role="group" aria-label="타입 필터">
      <button type="button" data-type="all" class="type-btn active">전체</button>
      <button type="button" data-type="image" class="type-btn">이미지</button>
      <button type="button" data-type="video" class="type-btn">동영상</button>
      <button type="button" data-type="audio" class="type-btn">음악</button>
      <button type="button" data-type="other" class="type-btn">기타</button>
    </div>
    <input id="toolbar-search" type="search" placeholder="이름으로 검색" autocomplete="off">
    <select id="toolbar-sort">
      <option value="name:asc">이름 ↑</option>
      <option value="name:desc">이름 ↓</option>
      <option value="size:asc">크기 ↑</option>
      <option value="size:desc">크기 ↓</option>
      <option value="date:asc">수정일 ↑</option>
      <option value="date:desc">수정일 ↓</option>
    </select>
  </div>
  ```
- `<script src="/app.js?v=13">` — 버전 bump (이전 v=12)

**style.css:**
- `.browse-toolbar { display: flex; gap: 8px; padding: 8px 24px; align-items: center; flex-wrap: wrap; border-bottom: 1px solid var(--border); }`
- `.toolbar-types { display: flex; gap: 4px; }`
- `.type-btn { padding: 4px 10px; border: 1px solid var(--border); background: none; color: var(--text-dim); border-radius: 4px; cursor: pointer; font-size: 0.8rem; }`
- `.type-btn.active { background: var(--accent); color: white; border-color: var(--accent); }`
- `.type-btn:hover:not(.active) { border-color: var(--accent); color: var(--accent); }`
- `#toolbar-search { flex: 1 1 180px; min-width: 120px; max-width: 320px; padding: 5px 10px; background: var(--surface); border: 1px solid var(--border); border-radius: 4px; color: var(--text); font-size: 0.85rem; }`
- `#toolbar-sort { padding: 5px 8px; background: var(--surface); border: 1px solid var(--border); border-radius: 4px; color: var(--text); font-size: 0.85rem; }`
- 모바일: `@media (max-width: 600px)`에서 `.browse-toolbar`가 자연스럽게 2줄로 wrap되는지 확인 — 특별한 추가 규칙 불필요.

**app.js 변경 영역:**
1. **DOM refs:** `browseToolbar`, `typeButtons` (NodeList), `toolbarSearch`, `toolbarSort`, `allEntries`(let).
2. **state 객체** `view` + 화이트리스트:
   ```js
   const SORT_VALUES = new Set(['name:asc','name:desc','size:asc','size:desc','date:asc','date:desc']);
   const TYPE_VALUES = new Set(['all','image','video','audio','other']);
   const view = { sort: 'name:asc', q: '', type: 'all' };
   ```
3. **URL I/O:**
   ```js
   function readViewFromURL() {
     const p = new URLSearchParams(location.search);
     const s = p.get('sort'); view.sort = SORT_VALUES.has(s) ? s : 'name:asc';
     view.q = (p.get('q') || '').trim();
     const t = p.get('type'); view.type = TYPE_VALUES.has(t) ? t : 'all';
   }
   function syncURL(push) {
     const p = new URLSearchParams();
     if (currentPath && currentPath !== '/') p.set('path', currentPath);
     else p.set('path', '/');
     if (view.sort !== 'name:asc') p.set('sort', view.sort);
     if (view.q) p.set('q', view.q);
     if (view.type !== 'all') p.set('type', view.type);
     const qs = '?' + p.toString();
     if (push) history.pushState({}, '', qs);
     else history.replaceState({}, '', qs);
   }
   ```
4. **applyView(entries):**
   ```js
   function applyView(entries) {
     const files = entries.filter(e => !e.is_dir);
     let out = view.type === 'all' ? files : files.filter(e => e.type === view.type);
     if (view.q) {
       const needle = view.q.toLowerCase();
       out = out.filter(e => e.name.toLowerCase().includes(needle));
     }
     const [key, dir] = view.sort.split(':');
     const mul = dir === 'desc' ? -1 : 1;
     out.sort((a, b) => {
       let cmp = 0;
       if (key === 'name') cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
       else if (key === 'size') cmp = a.size - b.size;
       else if (key === 'date') cmp = new Date(a.mod_time) - new Date(b.mod_time);
       if (cmp === 0 && key !== 'name') {
         cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
       }
       return mul * cmp;
     });
     return out;
   }
   ```
5. **browse() 수정:** 기존 `history.pushState`를 `syncURL(pushState)`로 교체. `imageEntries`/`videoEntries`/`playlist` 초기화 코드를 `renderView()`로 이동.
6. **renderView():** `browse()` 및 툴바 이벤트에서 공통 호출.
   ```js
   function renderView() {
     const visible = applyView(allEntries);
     imageEntries = visible.filter(e => e.type === 'image');
     videoEntries = visible.filter(e => e.type === 'video');
     playlist    = visible.filter(e => e.type === 'audio');
     renderBrowseSummary(visible);
     renderFileList(visible);
   }
   ```
7. **renderFileList 변경:** 0 결과일 때 문구 분기:
   ```js
   if (!fileCount) {
     const msg = (view.q || view.type !== 'all')
       ? '검색 결과가 없습니다.'
       : '파일이 없습니다.';
     fileList.innerHTML = `<p style="color:var(--text-dim);padding:20px 0">${msg}</p>`;
   }
   ```
8. **툴바 이벤트 바인딩 (초기화 블록):**
   - 각 `.type-btn` click: `view.type = btn.dataset.type` → active 클래스 갱신 → `syncURL(false)` → `renderView()`
   - `toolbarSearch` 'input' 이벤트: `view.q = e.target.value.trim()` → `syncURL(false)` → `renderView()`
   - `toolbarSort` 'change' 이벤트: `view.sort = e.target.value` → `syncURL(false)` → `renderView()`
9. **popstate:** 기존 핸들러에서 `path`만 읽던 코드를 확장 — `readViewFromURL()` 호출 + 툴바 컨트롤 값 동기 함수(`syncToolbarUI()`) + `browse(p, false)`. `browse` 내부가 `syncURL(push)`로 URL을 다시 쓰지 않도록 push=false 분기에서 `syncURL` 호출 생략해도 됨 (popstate가 이미 URL 소스).
10. **syncToolbarUI():** DOM 컨트롤 값을 `view`에 맞춰 갱신 (type 버튼 active 클래스, 검색 input.value, sort select.value).
11. **최초 로드:** `readViewFromURL()` → `syncToolbarUI()` → 기존 `browse(initialPath, false)` 흐름.

**완료 기준 (슬라이스 전체):**
- 툴바가 `#file-list` 위에 렌더, 모바일(<600px)에서 wrap 정상.
- 타입 세그먼트 클릭 → 해당 타입만 표시 + URL `type=...` 갱신 (기본 `all`은 생략).
- 검색어 입력 → 즉시 필터링 + URL `q=...` 갱신.
- 정렬 변경 → 각 섹션 내부 정렬 순서 변경 + URL `sort=...` 갱신.
- URL 붙여넣기로 같은 상태 복원 (예: `/?path=/&sort=size:desc&type=video`).
- 뒤로가기: 경로 이동은 복원, 툴바 변경은 히스토리에 안 남음.
- 합계(§2.5.1) 필터 통과 결과 기준으로 갱신.
- Lightbox prev/next가 visible 범위 안에서만 순회.
- 0결과 시 "검색 결과가 없습니다" 문구.

**검증:**
- 수동: 위 체크리스트를 브라우저에서 확인.
- Go 테스트: 서버 변경 없음 → 기존 테스트 재실행 `go test ./... -count=1` — 회귀 없음.

---

### S5 — 빈 상태 문구 + lightbox/playlist 재연동 검증

S2+S3+S4 커밋에 포함됨. 별도 커밋 없음. 수동 QA에서 다음 케이스 명시적으로 확인:
- 필터로 이미지 2개만 남은 상태에서 lightbox 열기 → prev/next가 2개 순환, 가려진 이미지로 안 넘어감.
- 오디오 3개 중 "가"로 시작하는 1개만 필터 → 오디오 플레이어 next가 동작 안 함(다음곡 없음).
- 검색어로 모두 가려지면 파일 리스트 영역에 "검색 결과가 없습니다" 문구.
- 빈 폴더(원래 0개): 검색어 없어도 "파일이 없습니다".

---

### S6 — E2E 수동 검증 (체크포인트)

**시나리오:**
1. `docker compose -p file_server up -d --build` 후 `http://localhost:8080` 접속.
2. 루트 폴더(`/`)에서:
   - 기본 상태: 툴바 "전체", 정렬 "이름 ↑". URL에 쿼리 없음.
   - "크기 ↓" 선택 → 섹션 내부 큰 파일부터. URL `?path=/&sort=size:desc`.
   - 타입 "동영상" → 이미지/음악/기타 섹션 제목 숨김, 동영상만. URL `sort=size:desc&type=video`.
   - 검색 "2026" 입력 → 2026 포함 파일만. URL `q=2026&sort=size:desc&type=video`.
   - 새로고침 → 동일 상태 복원.
3. 하위 폴더로 이동 → 경로 바뀜, 툴바 상태 **유지**. URL에 `path=<sub>&sort=size:desc&type=video&q=2026`.
4. 브라우저 뒤로가기 → 루트 복원.
5. 검색/정렬만 빠르게 토글 → 히스토리 엔트리 증가 안 함(replaceState 확인, DevTools Application → History).
6. 잘못된 쿼리(`?sort=foo&type=bar`) 붙여넣기 → 기본값으로 동작 + URL에서 해당 파라미터 제거.
7. 결과 0개 필터 → "검색 결과가 없습니다" 문구.
8. 라이트박스에서 prev/next 확인 (위 S5 케이스).
9. 모바일 폭 <600px에서 툴바 wrap 확인.
10. 기존 기능 회귀: 업로드, rename, 삭제, URL import, 드래그 이동 정상.

**완료 기준:** 위 10개 케이스 모두 기대 동작 확인.

---

### Out of scope (Phase 14)
- 섹션별 개별 정렬/필터 UI
- 재귀 검색(하위 폴더까지 이름 매칭)
- 정렬/필터 상태를 `localStorage`에 영속
- 확장자·duration·해상도 등 세부 필터
- 서버 사이드 정렬/페이지네이션
- 사이드바 트리의 정렬/검색

### 위험 / 롤백
- **위험: 툴바 상태가 `browse()` 재호출(업로드·rename·삭제 후 `loadBrowse()`)에서 사라짐** — `view` 객체가 모듈 스코프이고 `allEntries`만 교체되므로 유지됨. 주의: 기존 코드에 `loadBrowse`/`browse` 호출부가 여러 곳 — `syncURL` 중복 호출 없도록 browse 내부에서만 URL 갱신.
- **위험: lightbox/playlist가 필터 적용 전 시점 배열을 참조할 가능성** — `renderView` 호출이 배열을 갱신하므로 이벤트 핸들러가 캐시한 이전 배열을 쓰지 않도록 직접 모듈 변수(`imageEntries`, `playlist`) 참조를 유지.
- **위험: `localeCompare(numeric: true)` 지원 브라우저** — 모든 모던 브라우저 지원. 구형 IE 제외(스펙상 모바일 브라우저 지원이지만 IE 아님).
- **롤백:** Phase 14 커밋 revert → §2.5.1 상태 복귀. 서버 변경 없으므로 데이터 영향 0.

---

## Phase 16 — 움짤 필터 (`feature/clip-filter`) — spec [`SPEC.md §2.5.3`](../SPEC.md)

> **참고:** Phase 15는 병렬 진행 중인 인코딩 기능 브랜치가 선점. 움짤 필터는 Phase 16으로 배정.

**목표:** Phase 14 타입 세그먼트에 6번째 버튼 "움짤" 추가. GIF는 무조건, 동영상은 `size ≤ 50 MiB && duration_sec ≤ 30s`일 때 움짤. 서버 변경 없음.

### 의존성 그래프

```
  CF-1 (선행: SPEC + plan)
     └─► CF-2 (단일 슬라이스: TYPE_VALUES + applyView + 버튼 + v bump)
             └─► CF-3 (수동 검증)
```

---

### CF-2 — 움짤 필터 단일 슬라이스

**파일:** `web/index.html`, `web/app.js`

**변경 포인트:**
- `index.html`
  - `.toolbar-types` 내부 마지막에 `<button type="button" data-type="clip" class="type-btn">움짤</button>` 추가 (6번째).
  - `<script src="/app.js?v=14">` — v=13 → v=14.
- `app.js`
  - `TYPE_VALUES`에 `'clip'` 추가 → `new Set(['all','image','video','audio','other','clip'])`.
  - `isClip` 헬퍼 추가 + `applyView`의 타입 필터 분기를 배타 규칙으로 확장:
    ```js
    function isClip(e) {
      if (e.mime === 'image/gif') return true;
      if (e.type === 'video') {
        return e.size <= 50 * 1024 * 1024
          && e.duration_sec != null
          && e.duration_sec <= 30;
      }
      return false;
    }

    let out;
    if (view.type === 'all') out = files;
    else if (view.type === 'clip') out = files.filter(isClip);
    else out = files.filter(e => e.type === view.type && !isClip(e));
    ```
  - `이미지 / 동영상 / 움짤`은 배타 — 움짤 조건 만족 파일은 이미지·동영상 탭에 표시되지 않음. `전체` 탭은 모든 파일을 자연 타입 섹션에 그대로 표시.
  - 이후 검색(`view.q`)·정렬(`view.sort`) 적용은 기존 로직 그대로.

**완료 기준:**
- 움짤 버튼 클릭 → GIF + 조건 만족 동영상만 표시.
- URL `?type=clip` 추가, 새로고침 시 복원.
- `?type=clip`에서 GIF가 없는 폴더로 이동 → 동영상 섹션에 짧은 동영상만, 이미지 섹션은 0개로 숨김.
- 0결과 시 "검색 결과가 없습니다" 문구 (§2.5.2 기존 분기 그대로 작동 — `view.type !== 'all'`).
- 합계(§2.5.1)는 걸러진 visible 파일 수·합 기준.
- Lightbox prev/next가 남은 GIF만 순환.

**검증:**
- 수동: CF-3 10케이스.
- 자동: 서버 변경 없음 → 기존 Go 테스트 회귀 없음 (별도 실행 불필요).

---

### CF-3 — 수동 검증 (체크포인트)

1. `docker compose -p file_server up -d --build` 후 `http://localhost:8080` 진입.
2. GIF와 짧은 동영상(≤30s, ≤50MB)과 긴 동영상(>30s 또는 >50MB)이 섞인 폴더에서:
   - "움짤" 클릭 → GIF + 짧은 동영상만. 긴 동영상·정적 이미지·음악 사라짐.
   - URL `?path=/...&type=clip` 확인.
3. `duration_sec`이 `null`인 동영상(placeholder 썸네일) → 움짤 모드에서 제외됨 확인.
4. 움짤 모드에서 검색 `foo` 입력 → GIF/짧은 동영상 중 이름에 foo 포함되는 것만.
5. 움짤 모드에서 정렬 "크기 ↓" → 큰 움짤부터.
6. 움짤 모드 + 이미지 lightbox → 남은 GIF만 prev/next 순환.
7. 움짤 → 이미지 버튼 토글 → 원래 이미지 전체 복원.
8. URL에 `?type=clip` 수동 입력 후 이동 → 상태 복원.
9. 잘못된 값 `?type=클립` → 기본값 `all`로 fallback + URL에서 `type` 제거.
10. 기존 기능 회귀: 업로드, rename, 삭제, URL import, 드래그 이동.

**완료 기준:** 10개 모두 기대 동작.

---

### Out of scope (Phase 16)
- APNG / animated WEBP 감지 (ffprobe 헤더 sniff 필요)
- GIF duration 서버 측 추출
- 움짤 조건(50MB / 30s) 사용자 설정
- 움짤 전용 뷰(자동 재생 미리보기, 섹션 병합)

### 위험 / 롤백
- **위험: GIF이지만 실제 정적 1프레임(드물지만 있음)** — 그래도 움짤로 분류됨. 사용자 결정 "GIF는 무조건" 그대로 수용. 필요시 후속 Phase에서 frame count 기반 정교화.
- **위험: Phase 15 인코딩 브랜치가 먼저 머지되면** — SPEC.md/plan.md/todo.md 섹션 번호 인접 충돌 가능. 텍스트 충돌이며 내용 충돌 아님. Phase 16이 뒤에 머지될 때 rebase에서 수기 해결.
- **롤백:** Phase 16 커밋 revert → Phase 14 상태 복귀. 서버·데이터 영향 없음.
