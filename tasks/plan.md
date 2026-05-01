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

## Phase 15 — TS → MP4 영구 변환 (`feature/ts-to-mp4`) — spec [`SPEC.md §2.3.3`](../SPEC.md)

**목표:** `/data` 안의 기존 `.ts` 파일을 ffmpeg 리먹싱(`-c copy`)으로 동일 폴더의 `.mp4`로 영구 저장. `POST /api/convert` SSE 엔드포인트 + 동영상 카드의 개별 변환 버튼 + 폴더 일괄 변환 버튼. 재인코딩·다른 포맷 변환·변환 큐 영속화는 out of scope.

**기존 재사용 근거:** ffmpeg 호출 argv는 `internal/handler/stream.go:streamTS`(실시간 TS 리먹싱)에서 검증된 패턴을 그대로 사용. 진행 폴링(500 ms `os.Stat`)과 throttle(1 MiB / 250 ms)은 `internal/urlfetch/hls.go:runHLSRemux` 패턴을 재현(공유 패키지로 추출은 scope 밖, 중복 허용).

### 의존성 그래프

```
  C1 (internal/convert — ffmpeg remux 러너 + progress 콜백 + 테스트)
    └──► C2 (handler.handleConvert — POST /api/convert SSE, 검증, 배치, 취소 + 테스트)
           └──► 체크포인트: curl로 백엔드 E2E 확인 → 프론트 진입
                  └──► C3 (frontend — 카드 버튼 + 툴바 일괄 버튼 + 모달 + SSE 파싱)
                         └──► C4 (E2E 수동 검증 — 체크포인트)
```

---

### C1 — `internal/convert` 패키지: ffmpeg 리먹싱 러너

**파일:** `internal/convert/convert.go`, `internal/convert/convert_test.go`

**공개 API:**
```go
// Callbacks receive lifecycle events during a remux. Any field may be nil.
type Callbacks struct {
    OnStart    func(totalBytes int64)       // src 파일 크기. RemuxTSToMP4 호출 직후 1회.
    OnProgress func(outputBytes int64)      // 임시 MP4 파일 현재 크기. throttled.
}

// RemuxTSToMP4 리먹싱 실행. dstDir에 baseName+".mp4"가 이미 있으면 호출자가
// 사전에 감지해야 하며 이 함수는 목표 파일 존재 검사를 수행하지 않는다
// (호출자 레이어에서 SSE 에러 이벤트로 변환).
// 성공 시 최종 파일은 <dstDir>/<baseName>.mp4, size는 최종 크기.
// 실패 시 임시 파일은 항상 정리.
func RemuxTSToMP4(ctx context.Context, srcPath, dstDir, baseName string, cb Callbacks) (*Result, error)

type Result struct {
    Path string // dstDir/baseName+".mp4"
    Size int64
}

// Sentinel errors: 호출자가 errors.Is로 분기.
var (
    ErrFFmpegMissing = errors.New("ffmpeg_missing")
)

// FFmpegExitError wraps non-zero ffmpeg exit with captured stderr for logs.
// (urlfetch.ffmpegExitError와 동일 패턴, 중복 허용)
type FFmpegExitError struct {
    ExitCode int
    Stderr   string
}
```

**ffmpeg argv (stream.go:streamTS와 동일):**
```
ffmpeg -y -loglevel error
  -i <srcPath>
  -map 0:v:0 -map 0:a:0?
  -c:v copy -c:a copy
  -bsf:a aac_adtstoasc
  -movflags +faststart
  <tmpPath>
```

**동작 순서:**
1. `exec.LookPath("ffmpeg")` — 실패 시 `ErrFFmpegMissing` 즉시 반환 (tmp 생성 전).
2. `os.Stat(srcPath)` — size 확보 → `cb.OnStart(size)`.
3. `os.CreateTemp(dstDir, ".convert-*.mp4")` → 즉시 `Close` (ffmpeg가 `-y`로 재오픈).
4. `exec.CommandContext(ctx, "ffmpeg", ...)` → stderr을 `bytes.Buffer`로 캡처.
5. `cmd.Start()` → 별도 goroutine에서 500 ms 주기 `os.Stat(tmpPath)` → `cb.OnProgress` throttled (1 MiB 또는 250 ms, 둘 중 먼저).
6. `cmd.Wait()` 반환 대기.
7. exit code 0 → `os.Rename(tmpPath, finalPath)` atomic. 실패 시 tmp 제거.
8. exit code non-zero → `FFmpegExitError` 반환 + tmp 제거.
9. ctx 취소 → `cmd.Process.Kill()` (context가 알아서 kill signal 전달하지만 goroutine race 방지 위해 defer cleanup) + tmp 제거.

**동시성:** 이 함수 자체는 상태 없음. 동일 `srcPath`에 대한 중복 호출 직렬화는 호출자(`handler`) 책임.

**테스트 (`convert_test.go`):**
- 공통: `testdata/sample.ts` 픽스처가 필요. `go test` 진입 시 `ensureSampleTS(t)` helper로 한 번만 생성(ffmpeg로 1초 testsrc → .ts. ffmpeg 없으면 `t.Skip`).
- T1: `TestRemux_Success` — 정상 src → `Path`/`Size > 0`, 임시 파일 부재.
- T2: `TestRemux_OnStart_Called` — `OnStart(src.Size())`로 1회 호출 확인.
- T3: `TestRemux_OnProgress_MonotoneIncrease` — 호출 시 `outputBytes`가 단조 증가 (긴 src 필요 시 skip).
- T4: `TestRemux_AtomicRename` — 성공 전엔 `.mp4` 미존재, 성공 후 `.mp4` 존재 + 임시 파일 `.convert-*.mp4` 부재.
- T5: `TestRemux_CtxCancel` — start 후 1 ms 내 ctx 취소 → err non-nil, 임시 파일 정리 확인.
- T6: `TestRemux_NonZeroExit` — src=`/dev/null` 또는 빈 파일 → `FFmpegExitError`, 임시 파일 정리 확인.
- T7: `TestRemux_FFmpegMissing` — `PATH=""` 임시 환경 변수 → `ErrFFmpegMissing` 반환 + 임시 파일 생성도 되지 않음 확인.
- T8: `TestRemux_StderrCaptured` — non-zero exit 시 `FFmpegExitError.Stderr`가 비어있지 않음.

**완료 기준:**
- `go test ./internal/convert/ -count=1` 통과 (ffmpeg 없는 CI/로컬은 T1-T6 skip, T7/T8도 부분 skip; Docker 빌드 내에서는 전체 통과).

**검증:**
- `go test ./internal/convert/ -v -count=1` → 녹색.
- `go vet ./internal/convert/` → 무경고.

**파일 수:** 2 (신규). 크기: **S**.

---

### C2 — `handler.handleConvert` + `POST /api/convert` 라우트 + per-path mutex

**파일:** `internal/handler/convert.go`(신규), `internal/handler/convert_test.go`(신규), `internal/handler/server.go`(라우트 추가 1줄), `internal/handler/handler.go`(Handler에 `convertLocks sync.Map` 필드 추가 또는 기존 `streamLocks` 재사용)

**SSE 이벤트 타입 (convert.go 내부):**
```go
type convStart struct {
    Phase string `json:"phase"`  // "start"
    Index int    `json:"index"`
    Path  string `json:"path"`   // 원본 .ts 상대 경로
    Name  string `json:"name"`   // 최종 .mp4 파일명
    Total int64  `json:"total,omitempty"` // src size, 0 시 생략 (생략 안 할 예정이지만 omitempty)
    Type  string `json:"type"`   // 항상 "video"
}
type convProgress struct { Phase string; Index int; Received int64 }
type convDone struct { Phase string; Index int; Path, Name string; Size int64; Type string; Warnings []string }
type convError struct { Phase string; Index int; Path string; Error string }
type convSummary struct { Phase string; Succeeded, Failed int }
```

**handleConvert 흐름:**
1. `r.Method != POST` → 405.
2. JSON body parse → `{Paths []string, DeleteOriginal bool}`. 파싱 실패 → 400 `invalid request`.
3. `len(Paths) == 0` → 400 `no paths`. `> 50` → 400 `too many paths`.
4. SSE 헤더 설정: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`. Flusher 확보. 초기 `:\n\n` comment flush(프록시 버퍼 wake).
5. 각 path 순차 처리 (i = 0..len-1):
   - `r.Context().Err() != nil` → 루프 즉시 중단 (남은 항목 skip).
   - 검증 파이프라인 (실패 시 `convError` 방출하고 다음 path로):
     - `abs, err := media.SafePath(h.dataDir, rel)` — fail → `invalid_path`
     - `fi, err := os.Stat(abs)` — notexist → `not_found`, err → `write_error`
     - `fi.IsDir()` → `not_a_file`
     - `!strings.EqualFold(filepath.Ext(fi.Name()), ".ts")` → `not_ts`
     - 목표명 계산: `base := strings.TrimSuffix(fi.Name(), filepath.Ext(fi.Name()))`, `finalName := base + ".mp4"`, `finalPath := filepath.Join(filepath.Dir(abs), finalName)`
     - `_, err := os.Stat(finalPath)`; err == nil → `already_exists`
   - per-path mutex acquire: `unlock := h.lockConvertKey(abs); defer unlock()` — 동일 src에 대한 동시 중복 방지.
   - 재검사 (락 획득 후 재stat, 다른 요청이 먼저 생성했을 수 있음). 이미 존재 시 `already_exists`.
   - `convStart` 방출.
   - `convert.RemuxTSToMP4(r.Context(), abs, filepath.Dir(abs), base, convert.Callbacks{OnStart: ..., OnProgress: ...})`:
     - `OnStart`: 이미 방출했으므로 무시(또는 start 방출을 여기로 이동).
     - `OnProgress(bytes)` → `convProgress{Index: i, Received: bytes}` 방출.
   - 반환 에러 분기:
     - `errors.Is(err, convert.ErrFFmpegMissing)` → `ffmpeg_missing`
     - `errors.Is(err, context.Canceled)` → `canceled` + 루프 중단
     - `errors.Is(err, context.DeadlineExceeded)` → `convert_timeout`
     - `*convert.FFmpegExitError` → `ffmpeg_error` (stderr는 `log.Printf`로만)
     - 그 외 → `write_error`
   - 성공 시:
     - `DeleteOriginal == true`이면:
       - `os.Remove(abs)` — 실패는 `warnings = append(warnings, "delete_original_failed")` + `log.Printf`
       - 사이드카 2개 best-effort 삭제: `.thumb/{name}.ts.jpg`, `.thumb/{name}.ts.jpg.dur` — 실패는 로그만(경고 추가 안 함; 썸네일 부재는 원본 삭제와 별개).
     - `convDone{Size: result.Size, Warnings: warnings, Type: "video"}` 방출.
6. 루프 종료 후 `convSummary{Succeeded, Failed}` 방출.

**lockConvertKey:**
- 기존 `stream.go:lockStreamKey` 패턴 복사 또는 공유 helper 추출(이 PR 내 추가 refactor는 피함). 새 `convertLocks sync.Map` 필드 추가, key는 `abs` 경로 문자열.

**10분 타임아웃:**
- 파일당: `perFileCtx, cancel := context.WithTimeout(r.Context(), 10*time.Minute); defer cancel()`. `convert.RemuxTSToMP4`에 `perFileCtx` 전달. 전체 요청 타임아웃은 별도 적용하지 않음(배치 크기만큼 누적 가능).

**테스트 (`convert_test.go`):**
ffmpeg 있을 때만 실행되는 테스트는 `t.Helper` + `skipIfNoFFmpeg(t)` 가드. TS fixture 공유는 `testdata/sample.ts` → `setupTestFS(t)` helper가 tmpDir에 복사.
- T1: `TestHandleConvert_MethodNotAllowed` — GET → 405.
- T2: `TestHandleConvert_InvalidJSON` — 깨진 body → 400 `invalid request`.
- T3: `TestHandleConvert_NoPaths` — `{"paths":[]}` → 400 `no paths`.
- T4: `TestHandleConvert_TooManyPaths` — 51개 → 400 `too many paths`.
- T5: `TestHandleConvert_PathTraversal` — `../etc/passwd` → SSE `error: invalid_path`.
- T6: `TestHandleConvert_NotFound` — 없는 파일 → `error: not_found`.
- T7: `TestHandleConvert_NotAFile` — 디렉토리 경로 → `error: not_a_file`.
- T8: `TestHandleConvert_NotTS` — `foo.mp4` → `error: not_ts`.
- T9: `TestHandleConvert_CaseInsensitiveTS` — `foo.TS` → 변환 성공 (ffmpeg 있을 때).
- T10: `TestHandleConvert_AlreadyExists` — `foo.ts` + `foo.mp4` 존재 → `error: already_exists` (ffmpeg 호출 없이).
- T11: `TestHandleConvert_Success` — 정상 1개 → `start` → `progress`(≥0개) → `done` → `summary` succeeded=1. 원본 `.ts` 유지.
- T12: `TestHandleConvert_DeleteOriginal` — `{delete_original: true}` + 사이드카(`.jpg`/`.jpg.dur`) 존재 → 성공 후 원본 + 사이드카 모두 삭제 확인.
- T13: `TestHandleConvert_DeleteOriginalFailed` — 원본 삭제 실패 시뮬레이션(디렉토리 read-only + umask) → `done.warnings: ["delete_original_failed"]`.
- T14: `TestHandleConvert_BatchSequential` — 2개 정상 → index 0, 1 순서, summary succeeded=2.
- T15: `TestHandleConvert_PartialFailure` — 2개(1개는 not_ts, 1개는 정상) → summary succeeded=1, failed=1.
- T16: `TestHandleConvert_ContextCancel` — 핸들러 실행 중 client 연결 끊김 → `canceled` 이벤트 + 임시 파일 정리(`.convert-*.mp4` 부재).
- T17: `TestHandleConvert_FFmpegMissing` — `PATH=""` → `error: ffmpeg_missing`.

**curl 체크포인트 (수동):**
- Docker 빌드 후 `curl -N -X POST -H 'Content-Type: application/json' -d '{"paths":["movies/sample.ts"]}' http://localhost:8080/api/convert` → SSE 이벤트가 실시간 출력되는지.
- `{"paths":["movies/sample.ts"],"delete_original":true}` → 완료 후 `ls /data/movies` 결과 확인.

**완료 기준:**
- `go test ./internal/handler/ -run TestHandleConvert -count=1` 통과.
- curl SSE 스트림 정상 출력 + 실제 MP4 파일 생성 확인.

**검증:**
- `go test ./... -count=1` 회귀 없음.
- `go vet ./...` 무경고.
- curl E2E.

**파일 수:** 4 (신규 2, 수정 2). 크기: **M**.

---

### C3 — 프론트엔드: 카드 버튼 + 툴바 일괄 버튼 + 변환 모달

**파일:** `web/index.html`, `web/style.css`, `web/app.js`

**index.html:**
- `#browse-toolbar` 안(정렬 select 우측) 또는 별도 버튼으로 `<button id="convert-all-btn" class="convert-all-btn" hidden>모든 TS 변환</button>` 추가.
- 새 모달:
  ```html
  <div id="convert-modal" class="modal" hidden aria-modal="true" role="dialog">
    <div class="modal-content">
      <h2>TS → MP4 변환</h2>
      <p class="modal-hint">아래 파일을 MP4로 변환합니다. 원본 TS는 그대로 유지됩니다.</p>
      <ul id="convert-file-list" class="convert-file-list"></ul>
      <label class="convert-option">
        <input type="checkbox" id="convert-delete-original">
        변환 후 원본 TS 삭제
      </label>
      <div id="convert-rows" class="convert-rows"></div>
      <div id="convert-summary" class="convert-summary" hidden></div>
      <div class="modal-actions">
        <button type="button" id="convert-start-btn" class="primary">시작</button>
        <button type="button" id="convert-close-btn">닫기</button>
      </div>
    </div>
  </div>
  ```
- `<script src="/app.js?v=14">` — 버전 bump.

**style.css:**
- `.convert-all-btn { /* sort select 옆 정렬 */ padding: 5px 10px; background: var(--accent); color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 0.85rem; }`
- `.convert-all-btn[hidden] { display: none; }`
- `.convert-btn` (카드 아이콘 버튼) — 기존 `.rename-btn`/`.delete-btn`과 동일 레이아웃, 다른 아이콘(🎞 또는 "MP4").
- `.convert-file-list { /* 모달 내부 리스트 */ }`
- `.convert-row { /* per-file 진행 행 */ }` — URL import `.url-row` 스타일 재활용.
- `.convert-row.pending`, `.convert-row.active`, `.convert-row.done`, `.convert-row.error` 색상 구분.

**app.js 변경:**
1. **DOM refs 추가:** `convertAllBtn`, `convertModal`, `convertFileList`, `convertDeleteOriginal`, `convertRows`, `convertSummary`, `convertStartBtn`, `convertCloseBtn`.
2. **`buildVideoGrid`**: `.ts` 파일(대소문자 무시)이면 카드에 `.convert-btn` 렌더. 클릭 핸들러: `openConvertModal([entry.path])`.
3. **`renderView` 끝부분**: visible entries 중 `.ts` 파일 개수 N 계산 → N>0이면 `convertAllBtn.hidden=false`, 라벨 `모든 TS 변환 (${N})`. 0이면 `hidden=true`.
4. **`convertAllBtn` click**: visible에서 `.ts` 파일 경로 배열 → `openConvertModal(paths)`.
5. **`openConvertModal(paths)`:**
   - 모달 열기, `convertFileList`에 파일명 목록 렌더.
   - `convertRows` 비움. `convertSummary` hidden.
   - 체크박스 uncheck.
   - 상태: `convertBusy = false`, 현재 `paths` 저장.
6. **`closeConvertModal()`:** 실행 중이면 fetch `AbortController.abort()` 호출 후 닫기. 닫힌 뒤 `succeeded > 0`이면 `loadBrowse()` 재호출(기존 URL import 패턴과 동일).
7. **`submitConvert()`:**
   - `convertStartBtn.disabled = true`, `convertBusy = true`.
   - 각 path에 대해 `convert-row` 생성(pending 상태, 진행 바 0%).
   - `fetch('/api/convert', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({paths, delete_original: convertDeleteOriginal.checked}), signal: abortController.signal })`.
   - 응답이 4xx → JSON 에러 파싱 후 alert, 모달 유지.
   - SSE 파싱: URL import의 `readSSE(response, handleEvent)` 헬퍼 재사용. 각 이벤트:
     - `start` → 행 상태 `active`, `total` 저장(진행률 계산용).
     - `progress` → 행 progress bar 업데이트 (`received / total * 100`).
     - `done` → 행 상태 `done`, 진행 바 100%, size 표시, `warnings.includes("delete_original_failed")`이면 노란 경고.
     - `error` → 행 상태 `error`, 한국어 라벨 표시.
     - `summary` → `convertSummary`에 "X개 성공, Y개 실패" 표시. `convertBusy = false`, 버튼 재활성화.
8. **에러 코드 라벨:**
   ```js
   const CONVERT_ERROR_LABELS = {
     invalid_path: '잘못된 경로',
     not_found: '파일 없음',
     not_a_file: '파일이 아님',
     not_ts: 'TS 파일이 아님',
     already_exists: '같은 이름의 MP4 존재',
     ffmpeg_missing: 'ffmpeg 미설치',
     ffmpeg_error: '변환 실패',
     convert_timeout: '타임아웃',
     write_error: '저장 실패',
     canceled: '취소됨',
   };
   ```
9. **SSE 헬퍼 재사용:** 기존 URL import가 사용하는 ReadableStream + TextDecoder + line split → `phase` 디스패치 로직을 `readSSE(response, handler)` 함수로 통합(아직 모듈로 분리 안 된 경우 app.js 내 신규 함수).

**완료 기준:**
- 동영상 카드 중 `.ts` 파일(대소문자 무시)에 변환 버튼 렌더. 비-TS 카드에는 없음.
- 폴더에 TS 1개 이상 + 현재 filter/sort 통과 시 툴바에 `모든 TS 변환 (N)` 버튼 렌더. 0개일 때 숨김.
- 모달 "시작" → 각 행 진행 바 실시간 업데이트 → 성공 행 녹색, 실패 행 빨강.
- 닫기 시 `succeeded > 0`이면 `loadBrowse()` 호출로 새 `.mp4` 반영.
- 필터가 `type=video` + 검색어 상태에서도 visible TS만 대상.

**검증:**
- 수동 브라우저 확인 (C4에서 통합).
- Go 테스트 회귀: `go test ./... -count=1`.

**파일 수:** 3 (수정). 크기: **M**.

---

### C4 — E2E 수동 검증 (체크포인트)

**준비:**
- `docker compose -p file_server up -d --build`
- `http://localhost:8080` 접속
- 테스트 TS 파일 2-3개 업로드(또는 미리 `/data/movies/`에 배치)

**시나리오:**
1. TS 1개 폴더 → 동영상 카드에 변환 버튼 보임. 클릭 → 모달에 해당 파일 1개 표시 → "시작" → 진행 바 100% → 모달에 "1개 성공" → 닫기 → 카드에 새 `.mp4`, 원본 `.ts` **유지**. 새 `.mp4` 재생 시 **seek 동작** 확인.
2. 같은 TS 재시도 → `같은 이름의 MP4 존재` 에러 → 모달 유지.
3. `.mp4` 삭제 후 체크박스 "원본 삭제" ON → 시작 → 완료 후 `.ts` + `.thumb/foo.ts.jpg` + `.dur` 삭제 확인. 새 `.mp4`는 썸네일/duration lazy 생성(페이지 재조회 시 표시).
4. TS 3개 폴더 → 툴바 "모든 TS 변환 (3)" 버튼 → 모달에 3개 → 시작 → 순차 진행 (각 행 100%) → "3개 성공".
5. 4번 상태에서 변환 중 모달 "닫기" → `AbortController.abort()` → 서버 로그 `canceled` → `/data/movies`에 `.convert-*.mp4` 임시 파일 **없음**.
6. 손상 TS(`head -c 1024 good.ts > bad.ts`) 변환 → `변환 실패(ffmpeg_error)` 행 빨강.
7. 툴바 타입 "동영상" + 검색 "sample" → visible에 해당하는 TS만 일괄 변환 대상 포함 확인.
8. 모바일 폭(<600px): 변환 버튼 wrap, 모달 스크롤 정상.
9. 기존 기능 회귀: 업로드, rename, 삭제, URL import, HLS import, 드래그 이동, sort/filter 모두 정상.

**완료 기준:** 9개 케이스 모두 기대 동작 확인.

---

### Out of scope (Phase 15)
- 재인코딩(remux 실패는 `ffmpeg_error` 반환)
- MKV/AVI/MOV → MP4 변환 (범위 외)
- `.cache/streams/` 리먹싱 캐시 재활용 (hash 키 매칭 복잡도 대비 이득 작음)
- 변환 큐 영속화 (서버 재시작 시 진행 중 변환 폐기)
- 동시 ffmpeg 병렬 실행 (배치는 항상 순차)
- 체크박스 다중 선택 UI (§2.5.2 툴바 확장과 별개 scope)
- 변환 결과 저장 위치 변경 (항상 원본 폴더)
- 원본 `.ts`를 `.mp4`로 덮어쓰기 (목표 충돌은 항상 409)

### 위험 / 롤백

| 위험 | 영향 | 완화 |
|------|------|------|
| TS가 비-H.264/AAC (MPEG-2 비디오 등) → `-c copy`로 MP4 muxer 실패 | 중 | `ffmpeg_error`로 명확히 반환, 원본 유지. 사용자 UI에 "변환 실패" 라벨. |
| ffmpeg Docker 이미지 누락 | 중 | `ErrFFmpegMissing` sentinel + `ffmpeg_missing` 코드. 기존 Docker 이미지에는 이미 포함(Phase 5 T12). |
| 배치 중 한 ffmpeg 프로세스가 응답 없음 | 중 | 파일당 10분 timeout + ctx cancel → `cmd.Cancel`로 SIGKILL. |
| 임시 파일 누적 | 낮음 | `CreateTemp` 랜덤 suffix로 충돌 없음. 정상 경로는 ffmpeg 완료 직후 rename/remove. 비정상 종료 시 `.convert-*.mp4` 잔존은 재기동 시 수동 청소(관리자). |
| 동시 동일 src 요청 | 낮음 | per-path mutex(`lockConvertKey`). 두번째 요청은 락 대기 후 `already_exists` 히트. |
| 사용자가 변환 중 원본 `.ts` rename/삭제 | 낮음 | ffmpeg가 이미 src 파일 fd를 열고 있어 Linux에서 문제 없음. 변환 결과는 원본 이름 기반 `.mp4` — rename 후라면 stale. 단일 사용자 모델에서 acceptable. |
| 변환 완료 후 썸네일/duration 사이드카 초기 조회 지연 | 낮음 | 기존 lazy 메커니즘(§2.3.1, §2.3.2)이 browse 재조회 시 1회 probe → 허용. |

**롤백:** Phase 15 모든 커밋 revert → 기능 제거. 이미 생성된 `.mp4` 파일은 디스크에 남지만 UI에서 일반 MP4로 브라우즈/삭제 가능, 데이터 손실 없음.

---

## Phase 16 — 움짤 필터 (`feature/clip-filter`) — spec [`SPEC.md §2.5.3`](../SPEC.md)

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

---

## Phase 17 — 다운로드 설정 UI (`feature/download-settings`) — spec [`SPEC.md §2.7`](../SPEC.md)

URL Import와 HLS Import의 2 GiB / 10분 하드코드를 제거하고 UI에서 조정 가능한 서버 전역 설정으로 승격. 기본값은 10 GiB / 30분, 범위는 1 MiB ~ 1 TiB / 1 ~ 240분. Content-Length 누락 응답도 이제 허용 (런타임 누적 카운터로 보호).

**의존성:**
```
S1 (internal/settings 패키지: Store + Snapshot/Update + 경계 검증 + atomic write)
  └─► S2 (urlfetch 하드코드 상수 제거: Fetch(…, maxBytes) 시그니처, missing_content_length 제거, client Timeout 제거)
          └─► S3 (/api/settings 핸들러: GET + PATCH + 에러 매핑)
                  └─► S4 (프론트엔드: 헤더 ⚙ + 모달 + MiB/분 input + GiB helper)
                          └─► S5 (수동 검증 + 문서 정리)
```

### S1 — `internal/settings` 패키지

**파일:** `internal/settings/settings.go`, `internal/settings/settings_test.go`

**변경 포인트:**
- `Settings{URLImportMaxBytes int64, URLImportTimeoutSeconds int}` + JSON 태그 `url_import_max_bytes` / `url_import_timeout_seconds` (SPEC §2.7).
- 상수: `DefaultMaxBytes = 10 * 1024³`, `DefaultTimeoutSeconds = 1800`, `MinMaxBytes = 1<<20`, `MaxMaxBytes = 1<<40`, `MinTimeoutSeconds = 60`, `MaxTimeoutSeconds = 14400`.
- `Validate(Settings) error` — 범위 밖이면 `*RangeError{Field: "url_import_max_bytes"|"url_import_timeout_seconds"}`.
- `Store`: `sync.RWMutex` + `current Settings` + `path string`. `New(dataDir)` 가 `<dataDir>/.config/settings.json` 로드 (실패 시 기본값 + 경고 로그, 디스크 건드리지 않음). `Snapshot()` / `Update(Settings)` 가 read/write lock.
- `writeFile`: JSON marshal → `.settings-*.json` temp → fsync → rename (atomic).
- Update는 Validate 실패 → 에러, write 실패 → 캐시 변경 없음 (disk-cache drift 방지).

**완료 기준:** `go test ./internal/settings` 통과 — 8개 케이스 (Default, Validate 8 subcase, Missing file, Corrupt JSON, Out-of-range on disk, RoundTrip, RejectsOutOfRange, AtomicWriteLeavesNoTmp).

### S2 — `urlfetch` 하드코드 상수 제거

**파일:** `internal/urlfetch/fetch.go`, `internal/urlfetch/hls.go`, `internal/urlfetch/fetch_test.go`, `internal/urlfetch/fetch_hls_test.go`, `internal/urlfetch/hls_remux_test.go`, `internal/urlfetch/helpers_test.go`, `internal/handler/handler.go`, `internal/handler/import_url.go`, `cmd/server/main.go`, 모든 `*_test.go`의 `Register(...)` call sites

**변경 포인트:**
- `fetch.go`:
  - `MaxBytes`, `TotalTimeout` 상수 제거.
  - `NewClient()` — `Timeout` 필드 제거 (ctx timeout으로 대체).
  - `Fetch(ctx, client, rawURL, destDir, relDir, maxBytes int64, cb)` — 6번째 인자 추가.
  - `resp.ContentLength < 0` → **제거** (SPEC §2.7 "Content-Length 누락 허용"). 
  - `resp.ContentLength > maxBytes` + `io.LimitReader(..., maxBytes+1)` + 런타임 `n > maxBytes` — 모두 파라미터 기반.
- `hls.go`: `fetchHLS(..., maxBytes, cb)` + `runHLSRemux(..., maxBytes)` 전달. 주석의 `TotalTimeout` 참조도 "per-URL timeout" 표현으로 갱신.
- `handler/handler.go`: `Handler.settings *settings.Store` 필드 추가. `Register(mux, dataDir, webDir, store *settings.Store)` — nil 허용 (test harness용). `settingsSnapshot()` 헬퍼가 nil일 때 `settings.Default()` 반환.
- `handler/import_url.go`: 핸들러 진입 시 `snap := h.settingsSnapshot()` → per-batch snapshot. `fetchOneSSE(..., maxBytes int64, perURLTimeout time.Duration)` + `context.WithTimeout(ctx, perURLTimeout)` + `urlfetch.Fetch(..., maxBytes, cb)`.
- `cmd/server/main.go`: `settings.New(dataDir)` 실패는 `log.Fatal`. 성공 store를 `handler.Register(mux, ..., store)`에 전달.
- 테스트 고치기:
  - `urlfetch`: `helpers_test.go`에 `testMaxBytes = 4<<30` 상수. `fetch_test.go`는 별도 파일(`package urlfetch_test`)이라 동일 상수 재선언.
  - `TestFetch_NoContentLength`: `missing_content_length` 기대 → **성공 기대**로 재작성.
  - `TestFetch_ContentLengthTooLarge`: 1 KiB cap 주입으로 축소 (2 GiB 실데이터 불필요).
  - 신규 `TestFetch_NoContentLength_RuntimeCap`: CL 누락 + body > cap → 런타임 `too_large` + 임시 파일 정리.
  - 모든 기존 `Register(mux, root, root)` → `Register(mux, root, root, nil)` (sed 벌크 업데이트).
  - 모든 `Fetch(..., cb)` → `Fetch(..., testMaxBytes, cb)` (sed with `, (nil|cb|&urlfetch\.Callbacks\{\})\)$` 패턴).

**완료 기준:** `go build ./... && go test ./...` 전체 통과. `MaxBytes`·`TotalTimeout`·`missing_content_length` grep 매치 **0**.

### S3 — `/api/settings` 핸들러

**파일:** `internal/handler/settings.go`, `internal/handler/settings_test.go`, `internal/handler/handler.go` (라우트 등록)

**변경 포인트:**
- `handleSettings` 메서드 스위치: GET → `getSettings`, PATCH → `patchSettings`, 나머지 405.
- `getSettings`: `settingsSnapshot()` JSON으로 응답 (store nil이면 Default 값).
- `patchSettings`:
  - `h.settings == nil` → 500 `settings disabled` (테스트 harness 케이스).
  - `json.Decoder.DisallowUnknownFields()` — 오타 필드 조기 거부.
  - `store.Update(body)` → `errors.As(err, &RangeError)` 이면 400 `{error: "out_of_range", field: "..."}`, 다른 실패는 500 `write_failed`.
  - 성공 시 `store.Snapshot()` 에코백.
- `Register`에 `mux.HandleFunc("/api/settings", h.handleSettings)` 추가.

**완료 기준:** `go test ./internal/handler -run TestSettings` — 7개 subtest + 4 out-of-range subcase + 3 method-not-allowed 전부 통과.

### S4 — 프론트엔드 설정 UI

**파일:** `web/index.html`, `web/style.css`, `web/app.js`

**변경 포인트:**
- `index.html`:
  - 헤더에 `<button id="settings-btn" class="settings-btn" aria-label="설정">⚙</button>` (새 폴더 버튼 옆).
  - `#settings-modal` div — `<input type="number">` 2개 (MiB, 분), helper span, error p, 저장/취소 버튼.
  - URL 모달 hint를 "2GB/파일" → "용량/타임아웃은 ⚙ 설정에서 조정"으로 교체.
  - `<script src="/app.js?v=16">` — v=15 → v=16.
- `style.css`:
  - `.settings-btn` — new-folder-btn 스타일 미러 (border + hover accent).
  - `.settings-field`, `.settings-label`, `.settings-field input[type="number"]` (width 100%), `.settings-hint` (12px, min-height 1em — helper 표시 시 layout shift 방지).
- `app.js` 신규 블록 (Init 앞):
  - DOM refs 8개 (btn, modal, 2 input, hint, error, 2 action btn).
  - 상수 `SETTINGS_MAX_MIB_MIN/MAX = 1 / 1048576`, `SETTINGS_TIMEOUT_MIN/MAX = 1 / 240`. 서버 경계 미러.
  - `SETTINGS_FIELD_LABELS` 에 field → 한국어 라벨 매핑.
  - `openSettingsModal()`: 필드 초기화 + `fetch('/api/settings')` → byte→MiB/sec→min 환산 후 input 채움.
  - `closeSettingsModal()`.
  - `updateSettingsMaxHint()`: input 변경 시 GiB 환산 hint. `<1 GiB` 면 MiB 그대로, 이상이면 소숫점 1자리 GiB (10 이상은 정수).
  - `submitSettings()`: 클라이언트 검증 → PATCH payload 구성 (`mib * 1024²` / `min * 60`) → 에러 body의 `{error,field}`로 한국어 메시지.
  - 키보드: Escape → close, Enter → submit.
  - Click outside → close.

**완료 기준:** 브라우저에서 ⚙ → 모달 열림 → 현재 값 표시 → 편집 → 저장 → 재로드 시 반영 확인. 범위 밖 입력 시 한국어 에러 메시지. S5에서 체크리스트로 재확인.

### S5 — 수동 검증

**체크포인트:** `docker compose -p file_server up -d --build` 후 `http://localhost:8080`에서:
1. 첫 방문: `⚙` → 모달 오픈 → **기본값 표시** (10240 MiB, 30 분, helper "≈ 10.0 GiB").
2. 값 변경 저장: 20480 / 60 → 저장 → 모달 재오픈 시 새 값 유지 + `/data/.config/settings.json` 존재.
3. 경계 밖: 0 입력 → 모달 내 에러 "1~1048576 MiB 범위"; 300 입력 (타임아웃) → "1~240 분 범위".
4. 서버 경계 강제: curl로 `-d '{"url_import_max_bytes": -1, "url_import_timeout_seconds": 60}'` → `{"error":"out_of_range","field":"url_import_max_bytes"}`.
5. **Content-Length 누락 허용 회귀**: 작은 파일을 chunked로 서빙하는 origin(예: `python3 -m http.server`는 CL 붙음, `curl http.server` 는 붙음 — 실제로는 `Flask`/`gunicorn`/`nginx chunked` 등 필요) → URL import 성공 확인.
6. **cap 런타임 enforcement**: cap을 1 MiB로 줄이고 2 MiB 이미지 URL import → SSE `error: "too_large"` + `.thumb/`에 사이드카 없음 + `.urlimport-*.tmp` 없음.
7. 진행 중 import 도중 cap 변경 (PATCH) → 진행 중 요청은 **원래** cap 유지 (스냅샷 정책).
8. 타임아웃: cap 대부분 그대로, 타임아웃을 1분으로 줄이고 느린 origin으로 import → `download_timeout`.
9. 설정 파일 손상: 서버 중단 → `/data/.config/settings.json`을 `{bad json`으로 덮어씀 → 서버 재시작 → `⚙` 열면 **기본값**이 채워짐 (손상 파일은 디스크에 그대로 존재, 서버 로그에 warning 1줄).
10. 브라우저 회귀: 기존 기능(업로드, 삭제, rename, 움짤 필터, 드래그 이동, TS 변환) 각 1회 스모크 테스트.

**완료 기준:** 10개 전부 기대 동작. 실패 있으면 S5 재작업.

### Out of scope (Phase 17)
- 유저별 설정(single-tenant 전제 — 인증 없음).
- Per-URL cap/timeout 오버라이드(단일 전역 값만).
- 설정 UI에서 다른 knob (업로드 크기, 변환 타임아웃 등) 추가 노출.
- 설정 변경 이력/rollback UI.
- 설정 감사 로그(operator는 서버 로그 + git 기반 `settings.json` diff로 충분).

### 위험 / 롤백
- **위험: 진행 중 요청 스냅샷 정책** — 사용자가 cap을 늘리면서 동시에 큰 파일 import 기대 → 진행 중인 import는 원래 작은 cap을 유지하므로 `too_large`로 실패. 스펙대로의 동작이지만 UX 혼란 가능. 완화: "설정 변경은 다음 import부터 적용됩니다" hint를 모달에 추가 고려.
- **위험: settings.json 권한 이슈** — Docker volume의 `/data` 가 non-root로 mount 된 경우 `.config` 생성 실패. `New(dataDir)` 가 `log.Fatal`로 응답하므로 서버 시작 실패. `Dockerfile`은 이미 root로 run하므로 영향 없지만 host bind-mount 시 주의.
- **롤백:** Phase 17 커밋 revert → Phase 16 상태 복귀 + `.config/settings.json` 파일은 남지만 서버가 읽지 않음 (무해). 데이터 손실 없음.

---

## Phase 19 — URL Import 백그라운드 진행 (`feature/url-import-background`) — spec [`spec-url-import-background.md`](./spec-url-import-background.md)

URL import 모달 닫기 시 fetch를 abort하던 동작을 뒤집어, 모달은 **뷰**로만 동작하게 한다. 탭이 열려 있는 한 다운로드는 백그라운드에서 계속되고, 헤더 미니 배지로 진행 상황을 노출한다. 진행 중에 새 배치를 제출하면 서버 전역 `sync.Mutex`로 직렬화되고, 대기 중 배치는 `queued` SSE 이벤트 1회로 표시된다.

### 의존성 그래프

```
B1 (서버: importMu + queued SSE 이벤트 + 단위 테스트)
  │   └─ 독립: 클라이언트 변경 없이 머지해도 기존 UI 동일 동작 (queued 이벤트는 무시됨)
  │
  ▼
B2 (클라이언트 상태 모델: urlSubmitting/urlAbort → urlBatches[] 리팩토링)
  │   └─ 외부 동작 변경 없음 — 단일 배치 흐름 그대로. 회귀 방지용 순수 리팩토링.
  │
  ▼
B3 (close 시 abort 제거 + 미니 배지 + 완료 감지)
  │   └─ 첫 사용자 가시적 가치: "모달 닫아도 계속 받음". 단일 배치 한정.
  │
  ▼
B4 (재오픈 UX + 새 배치 추가 + queued 이벤트 처리)
  │   └─ 다중 배치 지원. B1의 서버 queued 이벤트 활용. 배치 구분 UI.
  │
  ▼
B5 (E2E 수동 검증 + SPEC.md §2.6 본문 갱신)
```

**수직 슬라이스 전략:** B1은 서버만, B2는 순수 client 리팩토링, B3부터 실 동작 변경. 각 단계가 독립 커밋 + 이전 단계 회귀 없음이 되게 자른다.

---

### B1 — 서버: `importMu` + `queued` SSE 이벤트

**파일:** `internal/handler/handler.go`, `internal/handler/import_url.go`, `internal/handler/import_url_test.go`

**변경 포인트:**
- `Handler` 구조체에 `importMu sync.Mutex` 필드 추가 (기존 `streamLocks`/`convertLocks`와 같은 줄맞춤).
- `handleImportURL`에서 `w.WriteHeader(http.StatusOK)` 직후, 기존 `writeMu`/`emit` 선언 이후, `settingsSnapshot()` 호출 **이전** 지점에:
  - `emit(sseQueued{Phase: "queued"})` 1회 방출.
  - mutex acquire. 단, `sync.Mutex.Lock()`은 ctx 취소에 반응 못하므로 **`chan struct{}` size-1 세마포어 패턴**으로 교체 (`Handler.importSem chan struct{}`) — init `make(chan struct{}, 1)` (Register 또는 zero-value 대체 초기화). acquire는 `select { case importSem <- struct{}{}: ...; case <-r.Context().Done(): return }`, release는 `<-importSem`.
  - **주의:** `sync.Mutex` 대신 세마포어를 쓰는 이유는 context-aware wait. Handler 필드명과 type을 고정하고, Register에서 `importSem: make(chan struct{}, 1)` 초기화.
- 신규 이벤트 타입:
  ```go
  type sseQueued struct {
      Phase string `json:"phase"` // "queued"
  }
  ```
  기존 `sseStart/sseProgress/sseDone/sseError/sseSummary`와 같은 파일. SPEC §5.1 용 이벤트 스키마 문서화는 B5에서.
- `fetchOneSSE`는 변경 없음 — acquire는 배치 레벨.
- 동시에 client가 연결을 끊으면 (ctx cancel) acquire 루프에서 바로 `return` — `emit(summary)`도 없이 종료 (기존 `r.Context().Err() != nil` 조기 리턴과 동치).

**테스트 (`import_url_test.go`):**
- **TestImportURL_Queued_EventEmittedOnce**: 단일 배치에서 `queued` 1회 + `start` 뒤에도 `queued` 추가 없음 확인.
- **TestImportURL_Serialization_TwoBatches**: 2개 POST 동시 실행 (goroutine + `sync.WaitGroup`). 첫 POST가 mutex 잡은 상태에서 두 번째가 `queued` 수신 후 block 확인 — 테스트 origin이 첫 요청을 `<-releaseCh` 까지 잡아둠. 두 번째 요청은 `queued` 이벤트 직후 read deadline 200ms → 이벤트 없음 확인 → releaseCh close → 첫 요청 완료 후 두 번째에서 `start` 수신 확인.
- **TestImportURL_Queued_CanceledWhileWaiting**: 첫 POST 중, 두 번째 POST에 pre-cancelled context. 두 번째는 `queued` 이벤트만 받고 mutex 미획득 상태로 조기 리턴 확인 — origin hit count는 첫 요청의 것만.
- 기존 테스트 모두 통과 — 특히 `TestImportURL_SSE_Headers`, `TestImportURL_SSE_SingleImage_StartDoneSummary`, `TestImportURL_SSE_ClientCancelled_StopsBatch` 회귀 체크.

**완료 기준:** `go test ./internal/handler -run TestImportURL -v` — 기존 9개 + 신규 3개 모두 통과. `go build ./...` 통과.

**체크포인트:** B1 단독 머지 가능. 프론트엔드는 `queued` phase 이벤트를 모르므로 `handleSSEEvent`의 `switch`에서 default로 조용히 무시됨 (기존 `app.js` 1,600줄 파일에서 `switch (ev.phase)` 구조 확인 후 안전 검증).

---

### B2 — 클라이언트 상태 모델 리팩토링 (`urlBatches[]`)

**파일:** `web/app.js`, `web/index.html` (version bump)

**변경 포인트:**
- 기존 전역:
  ```js
  let urlSubmitting = false;
  let urlAnySucceeded = false;
  let urlAbort = null;
  ```
  →
  ```js
  const urlBatches = [];  // [{ id, abort, rowEls: Map<index,HTMLElement>, succeeded: int, failed: int, total: int, done: bool }]
  let urlBatchSeq = 0;
  ```
  `urlSubmitting`은 `urlBatches.some(b => !b.done)` 로 파생. `urlAnySucceeded`는 `urlBatches.some(b => b.succeeded > 0 && b.done)` 로 파생. 헬퍼 함수 `anyBatchActive()` / `anyBatchSucceeded()`.
- `submitURLImport`:
  - 진입 시 새 `batch` 객체 생성: `{ id: ++urlBatchSeq, abort: new AbortController(), rowEls: new Map(), succeeded: 0, failed: 0, total: urls.length, done: false }`.
  - `urls.forEach((u, i) => ensureURLRow(batch, i, u))` — `ensureURLRow`를 batch-aware로 변경: `urlRows.querySelector([data-batch="${batch.id}"][data-index="${i}"])`.
  - fetch의 `signal: batch.abort.signal`.
  - `consumeSSE`에 `onEvent = ev => handleSSEEvent(batch, ev)` 전달.
  - finally 블록에서 `batch.done = true`.
- `handleSSEEvent(batch, ev)`:
  - 기존 `urlRows.querySelector([data-index="${ev.index}"])` → `batch.rowEls.get(ev.index)` 로 룩업. `start` 시 Map 등록.
  - `done` 시 `batch.succeeded++`, `error` 시 `batch.failed++`.
  - `summary` 처리는 현재 batch 한정 — 전역 summary 엘리먼트는 B4에서 다중 배치용으로 재설계. B2에서는 기존 단일 배치 가정 유지 (마지막 배치의 summary만 표시).
- `closeURLModal` 시 abort 호출 지점 — **B2에서는 유지** (동작 변경 없음). 기존 `if (urlSubmitting && urlAbort) { urlAbort.abort(); }` 를 `urlBatches.forEach(b => !b.done && b.abort.abort())` 로 대체만.
- `index.html`: `<script src="/app.js?v=18">` → `v=19`.

**회귀 방지 검증 (로컬 브라우저):**
- 단일 URL 성공 → row 진행 → done → modal summary 표시 → close → browse 재조회.
- 혼합 배치 (2 성공 + 1 에러) → summary "2 성공 / 1 실패".
- Close 중도 → abort → summary 없음, close 동작.
- HLS URL → indeterminate bar → 완료.

**완료 기준:** 브라우저 수동 4개 케이스 모두 기존과 동일 동작. `git diff web/app.js`에서 동작 로직은 변경 없음, 상태 저장 방식만 전환.

**체크포인트:** B2 단독 머지 가능. 사용자 가시 변경 없음 — 회귀 안 나면 성공.

---

### B3 — close 시 abort 제거 + 미니 배지 + 완료 감지

**파일:** `web/app.js`, `web/index.html`, `web/style.css`

**변경 포인트:**
- `closeURLModal()`:
  ```js
  function closeURLModal() {
    urlModal.classList.add('hidden');
    updateURLBadge();
    // abort() 호출 제거!
  }
  ```
- 신규 헬퍼 `updateURLBadge()`:
  - 조건: `anyBatchActive() && urlModal.classList.contains('hidden')` → 배지 보이기.
  - 내용: `URL ↓ ${완료합계}/${전체합계}` + 실패 있으면 `⚠` 접미.
  - 클릭 시 `openURLModal()` 재호출.
- 배치 완료 감지 (`handleSSEEvent`의 `summary` 분기):
  - `batch.done = true` 설정 후 `maybeFinalize()` 호출.
  - `maybeFinalize()`: `urlBatches.every(b => b.done)` 이면 →
    - `anyBatchSucceeded()` 이면 `browse(currentPath, false)` 1회.
    - 모달이 숨김 상태면 배지 제거 (에러만 있는 경우 3초 후 제거).
    - **배치 목록은 리셋하지 않음** — 다음 `openURLModal` 에서 초기 판정에 사용.
- `openURLModal()`:
  - 모든 배치가 `done` 이면 `urlBatches.length = 0` + row 영역 초기화 (기존 행동).
  - 아직 진행 중이면 row/상태 유지 + textarea만 빈 상태로 + confirm 라벨 B4에서 전환 (B3에서는 기존 "가져오기" 그대로).
- `index.html`:
  - 헤더 `<button id="settings-btn">` 바로 앞에 `<button id="url-badge" class="url-badge hidden" type="button" aria-label="진행 중인 URL 가져오기"></button>` 추가.
  - `<script src="/app.js?v=19">` → `v=20`.
- `style.css`:
  - `.url-badge` — 높이 24~28px, border-radius: 9999px, 작은 pill. hidden 상태 display: none.
  - `.url-badge.has-error` — 색 톤 변경 (경고 색).

**테스트 (브라우저 수동):**
1. 단일 URL 배치 중 모달 close → 배지 표시 → 모달 재오픈 → row 진행 유지 → 완료 → 배지 사라짐 + browse 재조회.
2. Close 후 탭 새로고침 → 서버 context cancel → 서버 로그에서 확인 → 브라우저 재접속 시 배지 없음.
3. 모달 연 상태에서 완료 → 기존 summary 흐름 유지.

**완료 기준:** 위 3 시나리오 통과. `anyBatchActive`/`maybeFinalize` 를 기반으로 single-source-of-truth 구조가 확립됨.

**체크포인트:** B3 머지 시점에서 단일 배치 사용자는 이미 이득 (close ≠ 취소).

---

### B4 — 재오픈 UX + 새 배치 추가 + `queued` 처리

**파일:** `web/app.js`, `web/index.html`, `web/style.css`

**변경 포인트:**
- `openURLModal()`:
  - 진행 중 배치 있음 → confirm 버튼 라벨 "새 배치 추가", textarea는 빈 상태, 기존 row 유지.
  - 배치 없음 → 라벨 "가져오기", row 초기화 (B3과 동일).
- `submitURLImport()`:
  - 진행 중 배치가 있으면 새 row는 **기존 row 아래에 append**. batch separator(얇은 divider 또는 "배치 N" 라벨) 삽입.
  - 새 batch를 `urlBatches` 에 push. 병렬 fetch — 각 배치가 독립 AbortController.
- `handleSSEEvent` 에 `queued` phase 추가:
  ```js
  case 'queued': {
    // 배치의 모든 row를 "대기 중 (큐잉)" 상태로
    for (const row of batch.rowEls.values()) {
      setRowStatus(row, 'status-pending', '대기 중 (순서 대기)');
    }
    break;
  }
  ```
  `start` 이벤트가 오면 자연스럽게 `status-downloading` 으로 덮어씀.
- `updateURLBadge`: 배지 aggregate 합계 로직 — `urlBatches.reduce((a, b) => a + b.succeeded + b.failed, 0) / urlBatches.reduce((a, b) => a + b.total, 0)`.
- `maybeFinalize`: 단일 → 다중 변경은 B3에서 완료. B4는 queued 처리만.
- 새 배치 시작 시 **`urlError`/`urlSummary` 엘리먼트 초기화** — 기존 전역 summary 엘리먼트는 여러 배치 동시엔 의미 없으므로 표시를 변경:
  - `urlSummary`는 "모든 배치 완료 시 전체 성공/실패 합산" 으로 재해석.
  - `maybeFinalize`에서 최종 집계 표시.
- Row DOM:
  - `<div class="url-row" data-batch="1" data-index="0">` — CSS selector 갱신.
  - 배치 경계 `<div class="url-batch-divider">배치 N</div>` (첫 배치는 넣지 않음 — 선택사항).
- `index.html`: `<script src="/app.js?v=20">` → `v=21`.
- `style.css`:
  - `.url-batch-divider` — 얇은 구분선 + 작은 배지 라벨.
  - `.url-row.status-pending` 기존 스타일 재사용 (queued 이벤트도 pending 상태 활용).

**테스트 (브라우저 수동):**
1. 배치 A 진행 중 → 모달 재오픈 → confirm 라벨 "새 배치 추가" 확인 → 새 URL 입력 → 추가 → row가 아래에 append + "대기 중 (순서 대기)" 상태.
2. 배치 A 완료 → 배치 B가 `start`로 상태 전환 → 완료.
3. 배치 A 진행 중 + 배치 B 대기 중 → 모달 close → 배지가 aggregate 진행률 표시.
4. 배치 A + B 모두 완료 → summary 합산 표시 → browse 재조회.
5. 서버 측 `queued` 이벤트 서버 로그 + 브라우저 DevTools SSE 탭에서 수신 확인.

**완료 기준:** 위 5 시나리오 통과. 동시 3개 배치까지는 브라우저 HTTP 연결 한도(호스트당 6) 내에서 정상 동작해야 함.

**체크포인트:** B4 머지 시점에 spec의 모든 기능 완료.

---

### B5 — E2E 수동 검증 + SPEC.md §2.6 갱신

**파일:** `SPEC.md`, `tasks/todo.md`

**수동 검증 체크리스트:**
1. 단일 배치 close → 배지 → 재오픈 → 완료 → browse 재조회. (B3 회귀)
2. 멀티 배치 A + B, 직렬 처리 확인 (서버 mutex). B5에서는 실제 HLS + 일반 mixed.
3. 배치 A 중 탭 새로고침 → 서버 context cancel → 임시 파일 정리 확인 (`.urlimport-*.tmp` 없음).
4. 배치 A 진행 중 설정 PATCH (`url_import_max_bytes` 하향) → 배치 A는 원래 값 유지, 배치 B는 새 값 적용 (스냅샷 정책).
5. HLS 배치 A + HLS 배치 B → 둘 다 indeterminate bar → 직렬 처리.
6. 배치 A의 일부 URL 실패 → 배지가 `⚠` 상태 → 모든 배치 완료 후 3초 뒤 배지 사라짐.
7. 배치 A + B 진행 중 배치 B만 에러로 모두 실패 → summary에서 합산 집계 정확.
8. Docker 컨테이너로도 동일 확인 — `docker compose up --build`.
9. 기존 다른 기능(업로드, 삭제, rename, 움짤 필터, 드래그 이동, TS 변환, 설정 UI) 스모크.
10. 모바일 뷰 — 배지 위치가 헤더에 자연스럽게 배치되는지 + 클릭 동작.

**SPEC.md §2.6 갱신 내역:**
- "모달 close = abort" 문구 제거, "탭 유지형 백그라운드 진행" 설명 추가.
- §5.1 SSE 이벤트 스키마 테이블에 `queued` 1종 추가 (페이로드 `{"phase":"queued"}`).
- §5.1.1 progress throttle 문단 유지.
- 배치 직렬화 동작 언급 (서버 전역 mutex로 per-request 순차 처리).

**완료 기준:** 10개 모두 통과 + SPEC.md 반영 + `tasks/todo.md` 체크 → `spec-url-import-background.md`는 상단에 "status: merged into SPEC §2.6" 노트 추가 후 보존.

---

### Out of scope (Phase 19)
- 탭 닫힘/브라우저 종료 시에도 서버가 끝까지 다운로드 (서버 측 잡 레지스트리 필요 — 별도 phase).
- HTTP Range resume (중단된 다운로드 이어받기).
- 개별 URL 취소 버튼 (배치 단위 취소는 탭 닫기 + 새로고침이 대체).
- 우선순위 큐잉 (FIFO로 충분, 단일 사용자).
- 배치 간 의존성/연쇄 실행.
- sessionStorage로 탭 새로고침 복구.

### 위험 / 롤백
- **위험: SSE 연결이 idle proxy timeout에 걸림** — `queued` 후 서버가 mutex 대기 중 침묵하면 중간 프록시(nginx 등)가 연결을 끊을 수 있음. 단일 사용자 + 로컬 네트워크 전제로는 영향 없음. Docker compose 구성에 proxy 없음 — 문제 시 `queued` 이벤트를 주기적으로 keep-alive(`: heartbeat\n\n`)로 보내는 보강 여지.
- **위험: 배치 수가 많을 때 브라우저 연결 고갈** — 동시 7개+ 배치를 띄우면 브라우저가 일부를 queue. 서버 mutex가 아닌 브라우저 레벨 큐잉이 발생하지만 기능은 동작. 단일 사용자 + 한 번에 1~2개 배치 예상이면 실전 문제 없음.
- **위험: `queued` 이벤트를 몰라서 무한 대기로 보이는 구 클라이언트** — B1만 머지하고 B2+ 를 미루는 경우. 현재 `handleSSEEvent` switch에 default case 없으므로 `queued` 는 조용히 무시되고, mutex가 acquire되면 `start` 이벤트가 오므로 사용자 체감 변화 없음 (약간의 지연만). 위험 낮음.
- **롤백:** B1~B5 커밋 순차 revert로 Phase 18 상태 복귀. 서버/클라이언트가 서로 독립적이라 부분 revert도 가능.

---

## Phase 20 — URL Import 잡 영속성 (`feature/url-import-persistence`) — spec [`spec-url-import-persistence.md`](./spec-url-import-persistence.md)

새로고침/탭 닫고 재오픈해도 진행 중 다운로드가 안 끊기게 한다. 핵심 변화: 잡 lifecycle을 **request lifecycle에서 분리**. 인메모리 잡 레지스트리(`internal/importjob`)가 잡의 진실. handler는 등록 후 첫 subscriber 역할만 하고, 다른 탭/새로고침은 GET 엔드포인트로 snapshot + live stream 구독. 사용자는 개별 URL/배치 단위 취소 + 종료 잡 dismiss 가능.

### 의존성 그래프

```
J1 (registry 모듈: Job + Registry + Subscribe/Publish/Cancel/Remove + 단위 테스트)
  │   └─ 독립: Handler가 사용 안 하는 동안은 외부 영향 없음.
  │
  ▼
J2 (graceful shutdown: serverCtx + signal handler)
  │   └─ 외부 동작 변경 없음. 회귀 검증만. J3 이후 잡 cancel 흐름의 기반.
  │
  ▼
J3 (handleImportURL registry 통합 + handler ctx ≠ job ctx + register 이벤트)
  │   └─ **첫 사용자 가시 변경**: 모달 닫기 + 새로고침해도 잡 계속. 진행 상황 복원은 J4까지 대기.
  │
  ▼
J4 (GET /jobs + GET /jobs/{id}/events + 클라이언트 bootstrap + subscribe)
  │   └─ 새로고침/재오픈 시 진행 상황 자동 복원. 다중 탭 fan-out.
  │
  ▼
J5 (cancel + dismiss API + per-URL ctx + UI)
  │   └─ 명시적 컨트롤. 개별 URL / 배치 / 종료 dismiss / 모두 지우기.
  │
  ▼
J6 (E2E 수동 8개 + SPEC.md §2.6 / §5.1 본문 갱신)
```

**수직 슬라이스 전략:** J1·J2는 격리, J3는 첫 가시 변경(클라이언트 호환), J4부터 클라이언트 동반. 각 단계 독립 커밋 + 회귀 없음.

**Spec 단순화 결정:** snapshot replay에서 history(이벤트 로그) 별도 보존 안 함. **`JobSnapshot` 자체가 진실** — `URLState.Received`로 progress 누적, `URLState.Status`로 start/done/error/cancelled lifecycle, `Job.Summary`로 종료 카운트. 새 subscriber는 snapshot 1회 + 라이브 stream만 받는다. spec §10 Q2 결론.

---

### J1 — `internal/importjob` 모듈

**파일:** `internal/importjob/job.go`, `internal/importjob/registry.go`, `internal/importjob/registry_test.go`

**타입 (`job.go`):**
```go
type Status string
const (
    StatusQueued    Status = "queued"
    StatusRunning   Status = "running"
    StatusCompleted Status = "completed"
    StatusFailed    Status = "failed"
    StatusCancelled Status = "cancelled"
)

type URLState struct {
    URL      string   `json:"url"`
    Name     string   `json:"name,omitempty"`
    Type     string   `json:"type,omitempty"`
    Status   string   `json:"status"` // pending | running | done | error | cancelled
    Received int64    `json:"received"`
    Total    int64    `json:"total,omitempty"`
    Warnings []string `json:"warnings,omitempty"`
    Error    string   `json:"error,omitempty"`
}

type Summary struct {
    Succeeded int `json:"succeeded"`
    Failed    int `json:"failed"`
    Cancelled int `json:"cancelled"`
}

type Event struct {
    Phase string          `json:"phase"`
    Data  json.RawMessage `json:"-"` // 클라이언트로 전달될 SSE payload (이미 marshalled)
}

type Job struct {
    ID        string
    DestPath  string    // dataDir-relative, slash 경로
    CreatedAt time.Time

    ctx       context.Context
    cancel    context.CancelFunc

    mu          sync.Mutex
    status      Status
    urls        []URLState
    summary     *Summary
    subs        map[uint64]chan Event
    nextSubID   uint64
    urlCancels  map[int]context.CancelFunc // index → per-URL cancel
}
```

**메서드 (`job.go`):**
- `(j *Job) Snapshot() JobSnapshot` — mu Lock, deep copy 후 반환.
- `(j *Job) Subscribe() (<-chan Event, func() unsubscribe)` — 신규 채널(buffer 64) 등록 + 해제 함수.
- `(j *Job) Publish(ev Event)` — 모든 sub에 non-blocking send (full이면 drop). 또한 mu 안에서 URLState 업데이트는 caller가 별도 헬퍼로.
- `(j *Job) UpdateURL(idx int, fn func(*URLState))` — mu 안에서 mutation.
- `(j *Job) SetStatus(s Status)`, `(j *Job) SetSummary(s Summary)`.
- `(j *Job) Cancel()` — j.cancel 호출 (전체 ctx).
- `(j *Job) RegisterURLCancel(idx int, cancel context.CancelFunc)` / `UnregisterURLCancel(idx int)`.
- `(j *Job) CancelURL(idx int) bool` — urlCancels[idx] 호출, ok 반환.
- `(j *Job) Status() Status`, `(j *Job) IsActive() bool` — convenience.

**Registry (`registry.go`):**
```go
const MaxQueuedJobs = 100

var ErrTooManyJobs = errors.New("too many queued jobs")
var ErrJobNotFound = errors.New("job not found")
var ErrJobActive = errors.New("job is still active")

type Registry struct {
    mu        sync.RWMutex
    jobs      map[string]*Job
    parentCtx context.Context
}

func New(parentCtx context.Context) *Registry
func (r *Registry) Create(destPath string, urls []string) (*Job, error)
    // ID 생성 (crypto/rand 5 byte → base32lower 8자), 활성+queued > MaxQueuedJobs면 ErrTooManyJobs.
    // ctx, cancel := context.WithCancel(r.parentCtx). 잡 등록.
func (r *Registry) Get(id string) (*Job, bool)
func (r *Registry) List() (active, finished []*Job)
    // active = queued|running, finished = completed|failed|cancelled. createdAt asc.
func (r *Registry) Remove(id string) error
    // 활성 잡이면 ErrJobActive. 종료면 삭제 + 모든 sub에 removed 이벤트 broadcast.
func (r *Registry) RemoveFinished() int
    // 종료 잡 일괄 제거 + 각각 broadcast.
func (r *Registry) CancelAll()
    // graceful shutdown 시 모든 활성 잡 cancel.
```

**테스트 (`registry_test.go`):**
- `TestRegistry_Create_AssignsID` — ID 형식 `imp_[a-z2-7]{8}` 매칭.
- `TestRegistry_Create_RejectsWhenFull` — Mock으로 MaxQueuedJobs 한계 검증 (테스트용 작은 cap 노출 또는 실제 100 채우기).
- `TestJob_Subscribe_BroadcastsToAll` — sub 3개 등록, Publish 1회 → 모두 수신.
- `TestJob_Subscribe_SlowConsumerDropped` — buffer 가득 차도 다른 sub은 수신.
- `TestJob_Unsubscribe_StopsDelivery` — 해제 후 Publish 안 옴.
- `TestJob_Cancel_PropagatesContext` — j.ctx.Done() 발화 + status = cancelled (caller가 SetStatus).
- `TestJob_CancelURL_OnlyAffectsTarget` — index N의 cancel 호출됨, 다른 index는 영향 없음.
- `TestRegistry_Remove_RejectsActive` — running 상태 잡 → ErrJobActive.
- `TestRegistry_RemoveFinished_LeavesActive` — 활성/종료 섞인 상태에서 종료만 제거.
- `TestRegistry_CancelAll_AffectsAllActive` — 모든 active 잡 ctx done.

**완료 기준:** `go test ./internal/importjob -v` 모두 통과. `go build ./...` 통과. 외부 패키지에서 import 가능한 상태이지만 아직 사용처 없음.

**체크포인트:** 단독 머지 가능. 외부 영향 없음.

---

### J2 — graceful shutdown (serverCtx)

**파일:** `cmd/server/main.go`, `internal/handler/handler.go`

**변경 포인트:**
- `cmd/server/main.go`:
  - `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` 로 `serverCtx` 생성.
  - HTTP server `ListenAndServe`를 별도 goroutine, main은 `<-serverCtx.Done()` 대기.
  - 종료 시: `httpServer.Shutdown(shutCtx5s)` → handler.Close() (registry는 J3 이후 추가).
- `internal/handler/handler.go`:
  - `Handler` 구조체에 `serverCtx context.Context` 필드 추가.
  - `Register` 시그니처에 `ctx context.Context` 인자 추가 — 호출 사이트는 `cmd/server/main.go` 1곳.
  - 시그니처 변경 영향: `internal/handler/*_test.go`의 `Register` 호출 모두 업데이트. nil ctx 허용 위해 `if ctx == nil { ctx = context.Background() }` 가드.

**테스트:**
- 기존 모든 핸들러 테스트 회귀 통과. 새 단위 테스트 없음.
- 수동: `go run ./cmd/server` → Ctrl+C → "server shut down gracefully" 로그 확인 (또는 필요 시 slog 한 줄 추가).

**완료 기준:** `go test ./...` 통과. `go build ./...` 통과. 수동 SIGINT 종료 검증.

**체크포인트:** 단독 머지 가능. 외부 동작 변경 없음 — registry 통합은 J3에서.

---

### J3 — `handleImportURL` registry 통합 + handler ctx 분리

**파일:** `internal/handler/handler.go`, `internal/handler/import_url.go`, `internal/handler/import_url_test.go`

**변경 포인트:**
- `handler.go`:
  - `Handler` 구조체에 `registry *importjob.Registry` 필드.
  - `Register`에서 `registry: importjob.New(serverCtx)` 초기화.
  - `Close()`에서 `h.registry.CancelAll()` 호출 (graceful shutdown 시 진행 잡 cancel).
- `import_url.go`:
  - `handleImportURL` 흐름 재구성:
    1. 기존 path/body 검증 그대로.
    2. `urls := normalizeURLs(...)` 후 `job, err := h.registry.Create(rel, urls)` — `ErrTooManyJobs`면 429.
    3. 응답 헤더 + flusher 준비 (기존).
    4. `events, unsubscribe := job.Subscribe(); defer unsubscribe()`.
    5. handler goroutine은 `events` 채널을 drain해서 `writeSSEEvent`로 SSE 직접 write. handler context done 시 종료 (잡은 unaffected).
    6. **별도 worker goroutine** 시작: 배치 처리 (queued emit, importSem acquire, fetch loop, summary). 모든 emit은 `job.Publish(...)` 호출. importSem acquire 대기는 `job.ctx`로 (handler ctx 아님 — 잡은 클라이언트 disconnect 후에도 살아남음).
    7. handler goroutine은 events 다 drain 후 (worker가 close)) 또는 r.Context().Done() 시 리턴.
  - **register 이벤트** — worker 시작 직전에 `job.Publish({"phase":"register","jobId":job.ID})` 1회.
  - per-URL ctx: `fetchOneSSE` 진입 시 `urlCtx, cancel := context.WithCancel(job.ctx)` + `job.RegisterURLCancel(idx, cancel)`, defer로 unregister + cancel. (cancel API는 J5에서 활용, J3에서는 등록만.)
  - **emit 함수가 두 가지 일** 단순화: 모든 이벤트는 `job.UpdateURL(idx, ...)` (URLState 갱신) + `job.Publish(ev)` (broadcast). handler subscriber가 SSE write를 책임.
  - 잡 status 전이: Create 직후 queued → importSem acquire 직후 SetStatus(running) → fetch loop 끝 후 summary 산정 → SetStatus(completed/failed/cancelled).
  - 큐잉 cap: J1의 `MaxQueuedJobs=100` 사용. 초과 시 429 + `{"error":"too_many_jobs"}`.
- `import_url_test.go`:
  - 기존 테스트 회귀 — emit 경로 변경되어도 SSE 응답 스키마는 동일해야 함.
  - **신규 `TestImportURL_Register_FirstEvent`** — POST 응답 첫 SSE가 `register` phase + jobId 형식.
  - **신규 `TestImportURL_HandlerDisconnect_JobContinues`** — 모의 클라이언트가 첫 progress 받자마자 ctx cancel → 일정 시간 후 `registry.Get(jobId)`로 잡이 여전히 active인지 + 결국 completed/failed로 종료되는지 (mock origin이 진행).
  - **신규 `TestImportURL_TooManyJobs`** — 큐잉 100개 채우고 101번째 → 429 + `too_many_jobs`.

**완료 기준:** `go test ./internal/handler -run TestImportURL -v` 모두 통과. `go build ./...` 통과.

**체크포인트:** **첫 사용자 가시 변경**. 모달 닫기 + 새로고침해도 잡 계속 (서버 로그로 확인). 단, 새로고침 후 클라이언트는 잡을 발견할 수단 없음 — UI는 빈 상태로 시작 (J4에서 복원). 클라이언트는 `register` phase 무시 (현재 switch에 default 없음, 해롭지 않음).

---

### J4 — `GET /jobs` + `GET /jobs/{id}/events` + 클라이언트 bootstrap

**파일:** `internal/handler/import_url_jobs.go` (신규), `internal/handler/import_url_jobs_test.go` (신규), `internal/handler/handler.go`(라우팅), `web/app.js`, `web/index.html`(version bump)

**서버:**
- `import_url_jobs.go`:
  - `handleListJobs(w, r)` — GET only. `registry.List()` → `{active, finished}` JSON. 각 잡은 `JobSnapshot` 모양 (URLs, Summary, Status, CreatedAt, DestPath, ID).
  - `handleSubscribeJob(w, r)` — GET only. URL path에서 jobId 추출. `registry.Get(id)`, 없으면 404.
    - SSE 헤더 set + 첫 이벤트 `{"phase":"snapshot","job":<JobSnapshot>}` emit.
    - 종료 상태 잡이면 snapshot emit 후 close.
    - 활성 잡이면 `job.Subscribe()`로 채널 받아 r.Context().Done() 또는 채널 close까지 SSE write.
  - 라우팅 (handler.go):
    - `mux.HandleFunc("/api/import-url/jobs", h.handleListJobs)` (정확 매치).
    - `mux.HandleFunc("/api/import-url/jobs/", h.handleJobsRouter)` — path suffix 분기 (`/{id}` GET → reject (snapshot은 list에 있음), `/{id}/events` GET → subscribe). cancel/dismiss는 J5에서 추가.
- `import_url_jobs_test.go`:
  - `TestListJobs_Empty` — 잡 없음 → `{active:[], finished:[]}`.
  - `TestListJobs_ActiveAndFinished` — registry에 직접 잡 주입 후 분류 확인.
  - `TestSubscribeJob_NotFound` — 404.
  - `TestSubscribeJob_FinishedReturnsSnapshotAndCloses` — finished 잡 → snapshot 1회 후 connection close.
  - `TestSubscribeJob_ActiveReceivesLiveEvents` — 진행 중 잡에 외부에서 Publish → snapshot + 후속 이벤트 수신.

**클라이언트 (`app.js`):**
- 신규 함수 `bootstrapURLJobs()`:
  - 페이지 로드 직후 `fetch('/api/import-url/jobs')` → 활성/종료 잡들.
  - 활성 잡 → `urlBatches`에 push (jobId 포함) + URLState 기반 row DOM 복원 (모달 안 열어도 데이터는 유지) + `subscribeToJob(jobId)` 시작.
  - 종료 잡 → `urlBatches`에 push (이미 done=true) + row 복원 (모달 열면 보임).
  - `updateURLBadge()` 호출.
  - app.js 진입 지점(현재 `init()` 또는 동등 위치)에서 1회 호출.
- 신규 함수 `subscribeToJob(jobId)`:
  - `new EventSource('/api/import-url/jobs/' + jobId + '/events')`.
  - onmessage 핸들러: 기존 `handleSSEEvent(batch, ev)` 재사용 (스키마 호환). `snapshot` phase는 무시 (이미 bootstrap에서 처리) 또는 row 갱신.
  - 끊김 시 EventSource 자동 재연결.
- `submitURLImport()` 개정:
  - 응답 첫 이벤트 `register`에서 jobId 추출 → `batch.jobId = ev.jobId`. (POST 응답 자체가 첫 subscriber 역할.)
- 새 phase handler:
  - `register` — batch에 jobId 저장 (UI 변경 없음).
  - `snapshot` — bootstrap 시 이미 처리, 라이브 subscriber에서는 row 일괄 갱신 (멱등).
- `index.html`: `<script src="/app.js?v=21">` → `v=22`.

**테스트 (수동):**
1. URL 다운로드 시작 → 모달 닫음 → 새로고침 → 배지에 진행률 + 모달 클릭하면 row 진행 표시.
2. 탭 두 개 → A에서 시작 → B 새로고침 → 같은 진행 보임.
3. 종료된 잡들 + 활성 잡 1개 → 새로고침 → 모달 열면 종료 잡 + 활성 잡 row 모두 보임.

**완료 기준:** 핸들러 테스트 5개 통과. 수동 시나리오 3개 통과.

**체크포인트:** 새로고침/재오픈 흐름 완성. 단, 취소/dismiss UI 없음 (J5).

---

### J5 — cancel + dismiss (API + UI)

**파일:** `internal/handler/import_url_jobs.go`, `internal/handler/import_url_jobs_test.go`, `web/app.js`, `web/index.html`, `web/style.css`

**서버:**
- 라우터 분기 추가 (`handleJobsRouter`):
  - `POST /api/import-url/jobs/{id}/cancel` (?index=N optional) → `handleCancelJob`.
  - `DELETE /api/import-url/jobs/{id}` → `handleDeleteJob`.
  - `DELETE /api/import-url/jobs?status=finished` → `handleDeleteFinishedJobs`.
- `handleCancelJob`:
  - jobId 추출, 잡 lookup, 없으면 404.
  - `index` 쿼리 파라미터 파싱:
    - 없음 → `job.Cancel()` 호출 → ctx done. fetch loop가 자연스레 cancelled로 종료. (worker goroutine이 미시작 URL은 cancelled로 emit + summary emit + status 전이.)
    - 있음 → `job.CancelURL(idx)` → false면 400 (이미 종료된 URL or 미존재 idx). true면 fetchOneSSE의 ctx done → urlfetch가 자연스레 종료. emit은 worker가 cancelled error code로.
  - 활성 URL 없는 종료 잡에 cancel → 409.
  - 응답 204.
- `handleDeleteJob`:
  - 활성 잡 → 409 + `{"error":"job_active"}`.
  - 종료 잡 → `registry.Remove(id)` + broadcast `{"phase":"removed","jobId":id}`.
  - 응답 204.
- `handleDeleteFinishedJobs`:
  - `?status=finished` 쿼리 검증 (다른 값은 400). `registry.RemoveFinished()` → `{"removed": N}`.
- `import_url.go` worker 변경:
  - fetch loop 진입 전 `urlCtx, cancel := context.WithCancel(job.ctx)`, `job.RegisterURLCancel(idx, cancel)`.
  - `urlfetch.Fetch(urlCtx, ...)` — ctx done이면 cancelled 처리:
    - `urlCtx.Err() != nil && job.ctx.Err() == nil` → 개별 cancel.
    - `job.ctx.Err() != nil` → 배치 cancel.
    - URLState.Status = "cancelled", emit `{"phase":"error","index":N,"error":"cancelled"}` (또는 신규 `cancelled` phase — 호환 위해 error+code "cancelled" 권장).
  - 잡 종료 status 결정: succeeded≥1 → completed, 그 외 cancelled가 있으면 cancelled, 아니면 failed.

- 핸들러 테스트:
  - `TestCancelJob_Batch` — 진행 중 잡 cancel → status cancelled, summary broadcast.
  - `TestCancelJob_PerURL` — 5개 URL 중 index 1 cancel → 그 URL만 cancelled, 잡은 계속, 종료 status는 completed (다른 succeeded 있으면).
  - `TestCancelJob_NotFound` / `TestCancelJob_AlreadyDone` → 404 / 409.
  - `TestDeleteJob_Active` → 409.
  - `TestDeleteJob_Finished` → 204 + 후속 List에서 사라짐.
  - `TestDeleteFinishedJobs` → 종료 잡들만 일괄 제거.

**클라이언트:**
- Row UI:
  - 활성 URL row 우측에 ✕ 버튼 (취소) — `data-job-id` + `data-index`.
  - 종료 row(succeeded/failed/cancelled) 우측에 X 버튼 (dismiss는 잡 단위, 그래서 row 단위 dismiss는 안 함 — 잡 헤더에서). row 단위는 표시만.
- 배치 헤더:
  - 활성 배치: "전체 취소" 버튼.
  - 종료 배치: "닫기" 버튼 (잡 dismiss).
- 모달 footer:
  - "완료 항목 모두 지우기" 버튼 → `DELETE /api/import-url/jobs?status=finished`.
- `removed` phase handler — 해당 잡의 row + divider DOM 제거 + `urlBatches`에서 splice.
- `cancelled` 표시 — error code "cancelled"는 `URL_ERROR_LABELS`에 "취소됨"으로 매핑.
- `index.html`: `<script src="/app.js?v=22">` → `v=23`. 모달 footer "모두 지우기" 버튼 추가.
- `style.css`: row cancel/dismiss 버튼, 모두 지우기 버튼.

**테스트 (수동):**
1. 5개 URL 진행 중 2번째만 ✕ → 그것만 cancelled, 나머지 진행, summary "3 성공 / 0 실패 / 1 취소".
2. 진행 중 "전체 취소" → 모든 미종료 cancelled, 부분파일 정리.
3. 완료 배치 "닫기" → row 사라짐, 양쪽 탭 동기화.
4. "모두 지우기" → 종료 잡 일괄 제거.
5. 활성 잡 dismiss 시도 → UI에서 차단되거나 409 표시.

**완료 기준:** 핸들러 테스트 6개 통과. 수동 5개 통과.

**체크포인트:** spec 모든 기능 완료.

---

### J6 — E2E + SPEC.md 갱신

**파일:** `SPEC.md`, `tasks/spec-url-import-persistence.md`, `tasks/todo.md`

**수동 E2E 8개 시나리오** (spec §8.수동 시나리오와 동일):
1. 큰 URL 다운로드 시작 → 새로고침 → 배지 복원 + 진행 바 끊김 없이 갱신.
2. 탭 두 개 → A 시작 → B 새로고침 → 진행 표시.
3. URL 5개 중 2번째 개별 취소 → 2번만 cancelled, 3~5 정상 진행 + 부분파일 정리.
4. 다운로드 중 "전체 취소" → 모든 URL 즉시 중단, summary 표시, 부분파일 정리.
5. 종료 배치 dismiss → 양쪽 탭 row 제거.
6. 서버 재시작 (Ctrl+C → 재실행) → 새로고침 → 배지 없음, 임시파일 정리됨.
7. HLS 배치 동일 흐름 (시작 → 새로고침 → 진행 복원 → 취소).
8. 큐잉 100개 가득 → 101번째 POST → 429 + 사용자에게 에러 메시지.

**SPEC.md §2.6 / §5.1 갱신 내역:**
- §2.6 본문에 "잡 lifecycle은 클라이언트 세션과 분리, 새로고침/탭 재오픈 후에도 진행" 명시.
- §2.6에 잡 ID, 새로고침 복원, 다중 탭 fan-out, 취소 단위(개별 URL/배치), dismiss/전체 정리 규칙.
- §2.6 명시: 서버 재시작 시 잡 손실 (디스크 영속 안 함), graceful shutdown 시 임시파일 정리.
- §5.1에 신규 엔드포인트 4개 추가:
  - `GET /api/import-url/jobs` — active/finished snapshot.
  - `GET /api/import-url/jobs/{id}/events` — snapshot + live SSE.
  - `POST /api/import-url/jobs/{id}/cancel?index=N` — 개별/배치 cancel.
  - `DELETE /api/import-url/jobs/{id}` 및 `DELETE /api/import-url/jobs?status=finished`.
- §5.1 SSE 이벤트 스키마 표에 `register`, `snapshot`, `removed` 3종 추가.
- `MaxQueuedJobs=100` 한도 명시.

**spec-url-import-persistence.md** 상단 Status를 `merged into SPEC §2.6` 로 갱신.

**완료 기준:** 8개 모두 통과 + SPEC.md 반영 + `tasks/todo.md` 체크.

---

### Out of scope (Phase 20)
- 디스크 영속 잡 큐 (서버 재시작 후 자동 재개) — 별도 phase.
- HTTP Range resume (끊긴 바이트 이어받기).
- 다중 탭 실시간 push (탭 B에서 새 잡 발견은 페이지 로드 시점에만).
- 사용자 인증 / 멀티 사용자.
- 잡 history 자동 TTL.

### 위험 / 롤백
- **위험: handler ctx와 job ctx 분리 후 첫 subscriber(POST 응답) goroutine 누수** — handler가 일찍 return해도 worker는 job.ctx로 살아있어야 한다. handler subscriber 채널은 unsubscribe로 정리, worker는 별도 goroutine에서 job.ctx까지 살아있다가 종료 시 publish summary + status 전이 후 자연 종료. 테스트로 검증 (`TestImportURL_HandlerDisconnect_JobContinues`).
- **위험: subscriber buffer 가득 시 lifecycle 이벤트 drop** — start/done/error/summary가 dropped되면 클라이언트 상태 불일치. buffer 64로 충분하지만, 안전장치로 lifecycle 이벤트는 drop 시 sub detach + close (클라이언트가 EventSource 재연결로 snapshot 다시 받음).
- **위험: graceful shutdown 시 ffmpeg HLS 자식 프로세스 좀비** — registry.CancelAll() → job.ctx done → urlfetch/hls의 ctx 감지 → ffmpeg.Process.Kill() 또는 SIGTERM. 기존 `runHLSRemux`의 ctx cancel 경로 검증 필요 (재사용 가능해야 함).
- **위험: 클라이언트가 `register`/`snapshot`/`removed` phase 모르는 구버전** — J3만 머지 시 `register`는 default case 없으므로 무시됨. J4 머지 후엔 `snapshot`/`removed`도 동일. switch에 명시 default(no-op) 추가해 forward-compatibility 확보.
- **롤백:** J1~J6 커밋 순차 revert로 Phase 19 상태 복귀. J1~J2는 격리되어 있어 부분 revert 가능. J3는 emit 경로 재구성이라 클라이언트 호환만 유지되면 단독 revert 안전.

---

## Phase 24 — 폴더 이동 + 사이드바 폴더 작업 정리 + 0.0.1 릴리즈 (`feature/folder-move-and-release-v0.0.1`) — spec [`SPEC.md §2.1.2 / §2.1.3 / §10`](../SPEC.md)

**목표:** `media.MoveFile`이 폴더를 거부하던 갭을 닫는다. `PATCH /api/folder` body 분기(`{name}`/`{to}`)로 폴더 이동을 추가하고, 사이드바 트리 노드에 🗑 버튼·DnD를 활성화하며, 메인 툴바 "새 폴더" 버튼을 사이드바 헤더로 이전한다. README에 0.0.1 릴리즈 노트.

### 의존성 그래프

```
F1 백엔드 코어 (media.MoveDir + 단위 테스트)
   │
   └─► F2 핸들러 분기 (handleFolder PATCH body 분기 + 통합 테스트)
          │
          └─► [체크포인트 ①: curl로 백엔드 단독 검증]
                 │
                 ├─► F3 사이드바 헤더 정리 (새 폴더 버튼 이전 + 트리 🗑)
                 │     └─ 백엔드 의존: 기존 POST/DELETE /api/folder만 사용 (F1/F2 없이도 동작)
                 │
                 └─► F4 폴더 DnD (트리 dragstart + 메인 표 폴더 dragstart + drop 라우팅)
                        ↑ F2의 PATCH /api/folder {to} 의존
                        │
                        └─► [체크포인트 ②: 브라우저 E2E 수동 (10케이스)]
                               │
                               └─► F5 README + 0.0.1 릴리즈 정리
                                      │
                                      └─► [체크포인트 ③: SPEC §10 모든 항목 체크]
```

**병렬화:** F3와 F4는 같은 모듈(`web/`)을 만져 충돌 가능. 동일 PR/브랜치에서 F3 → F4 순차 진행 권장. F1·F2는 독립 커밋으로 분리 가능.

---

### F1 — `media.MoveDir` 신설 + 단위 테스트

**파일:** `internal/media/move.go`, `internal/media/move_test.go`

**신규 export:**
- `func MoveDir(srcAbs, destDir string) (string, error)`
- `var ErrSrcNotDir = errors.New("source is not a directory")`
- `var ErrDestExists = errors.New("destination already exists")`
- `var ErrCircular = errors.New("destination is inside source")`
- `var ErrCrossDevice = errors.New("cross-device folder move not supported")`

**구현 핵심:**
1. `os.Stat(srcAbs)` → 미존재면 `ErrSrcNotFound`(기존 재사용), 디렉토리 아니면 `ErrSrcNotDir`
2. `os.Stat(destDir)` → 미존재면 `ErrDestNotFound`(기존), 디렉토리 아니면 `ErrDestNotDir`(기존)
3. 자기 자신·자손 검사: `srcClean := filepath.Clean(srcAbs); destClean := filepath.Clean(destDir)`
   - `destClean == srcClean` → `ErrCircular`
   - `strings.HasPrefix(destClean, srcClean+string(filepath.Separator))` → `ErrCircular`
   - prefix 가짜양성 방지를 위해 separator 경계 명시
4. 결과 경로 `dstPath := filepath.Join(destDir, filepath.Base(srcAbs))`
5. `os.Stat(dstPath)`가 nil 에러(존재) → `ErrDestExists` (자동 suffix 없음 — `MoveFile`과 다른 정책)
6. `os.Rename(srcAbs, dstPath)` — 성공이면 `dstPath` 반환
7. EXDEV(`errors.Is(err, syscall.EXDEV)`)면 `ErrCrossDevice` (재귀 copy 폴백 없음)
8. 사이드카 별도 처리 없음 — 폴더 rename과 동일 (자식 모두 함께 이동)

**단위 테스트 (`move_test.go` 추가, 6개):**
- `TestMoveDir_Success` — `t.TempDir()`에 src/dst 만들고 src/foo.txt + src/.thumb/foo.txt.jpg 생성, MoveDir → dst/<basename>/foo.txt + dst/<basename>/.thumb/foo.txt.jpg 확인
- `TestMoveDir_DestExists` — dst에 동일 base name 폴더(또는 파일) 사전 생성 → `ErrDestExists`
- `TestMoveDir_Circular_Self` — destDir == srcAbs → `ErrCircular`
- `TestMoveDir_Circular_Descendant` — destDir이 srcAbs의 자손 → `ErrCircular`
- `TestMoveDir_PrefixFalsePositive` — `/tmp/a` → `/tmp/ab` 정상 (자손 아님)
- `TestMoveDir_NotADir` / `TestMoveDir_DestNotFound` — sentinel 매핑 정확

**완료 기준:**
- 위 6 케이스 모두 pass
- `go vet ./internal/media` / `go test ./internal/media` 통과

**검증:** `go test -run TestMoveDir ./internal/media -v`

---

### F2 — `handleFolder` PATCH body 분기 + 통합 테스트 + 체크포인트 ①

**파일:** `internal/handler/files.go`, `internal/handler/files_test.go`

**변경 핵심:**
- `handleFolder` PATCH 케이스를 `patchFolder` dispatcher로 교체 — `patchFile`(`files.go:133`) 패턴 재사용:
  - `io.ReadAll(r.Body)` → probe `{name, to}` → 둘 다이거나 둘 다 없으면 400
  - `r.Body = io.NopCloser(bytes.NewReader(bodyBytes))`로 복원 후 `moveFolder` 또는 `renameFolder` 디스패치
- `moveFolder` 신설:
  - `media.SafePath(h.dataDir, rel)` → srcAbs
  - 루트 가드: `srcAbs == filepath.Clean(h.dataDir)` → `400 cannot move root`
  - body decode → `media.SafePath(h.dataDir, body.To)` → destAbs
  - 동일 부모 가드: `filepath.Dir(srcAbs) == filepath.Clean(destAbs)` → `400 same directory`
  - `media.MoveDir(srcAbs, destAbs)` → 에러 매핑:
    - `ErrSrcNotFound` → 404 `not found`
    - `ErrSrcNotDir` → 400 `not a directory`
    - `ErrDestNotFound` / `ErrDestNotDir` / `ErrCircular` → 400 `invalid destination`
    - `ErrDestExists` → 409 `already exists`
    - `ErrCrossDevice` → 500 `cross_device`
    - 기타 → 500 `move failed`
  - 응답: `{"path": "/<dst rel>", "name": "<basename>"}`

**통합 테스트 (`files_test.go` 추가, ~10개):**
- `TestPatchFolder_Move_Success` — `/a/sub` → destDir `/b` → `/b/sub`로 이동, 하위 파일·`.thumb/` 모두 따라감 확인
- `TestPatchFolder_Move_BothFields` — body `{name, to}` 동시 → 400 `specify either name or to, not both`
- `TestPatchFolder_Move_MissingFields` — body `{}` → 400 `missing name or to`
- `TestPatchFolder_Move_RootRejected` — path=`/` → 400 `cannot move root`
- `TestPatchFolder_Move_DestNotDir` — to가 파일을 가리킴 → 400 `invalid destination`
- `TestPatchFolder_Move_DestNotFound` — to가 미존재 → 400 `invalid destination`
- `TestPatchFolder_Move_Circular` — to가 src의 자손 → 400 `invalid destination`
- `TestPatchFolder_Move_SameDir` — to가 src의 부모와 동일 → 400 `same directory`
- `TestPatchFolder_Move_Conflict` — destDir에 동일 base name 폴더 사전 존재 → 409 `already exists`
- `TestPatchFolder_Move_Traversal` — to에 `..` → 400 `invalid path`
- (기존 `TestRenameFolder*` 회귀 통과 확인)

**완료 기준:**
- 위 케이스 모두 pass
- 기존 `TestRenameFolder*` / `TestDeleteFolder*` 회귀 0
- `go test ./internal/handler -run TestPatchFolder -v`

**체크포인트 ① (백엔드 curl 단독 검증):**
1. `go run ./cmd/server` (별도 터미널, `DATA_DIR=./tmp-data`)
2. `mkdir -p ./tmp-data/a/sub ./tmp-data/b && echo hi > ./tmp-data/a/sub/x.txt`
3. `curl -X PATCH 'http://localhost:8080/api/folder?path=/a/sub' -d '{"to":"/b"}' -H 'Content-Type: application/json'` → 200 + `{"path":"/b/sub","name":"sub"}`
4. `ls ./tmp-data/b/sub/x.txt` 존재 확인
5. 충돌 케이스: src 다시 만들고 같은 요청 → 409 `already exists`
6. 자손 케이스: `curl ...?path=/b/sub -d '{"to":"/b/sub"}'` → 400 `invalid destination`

---

### F3 — 사이드바 헤더 정리 (새 폴더 버튼 이전 + 트리 노드 🗑)

**파일:** `web/index.html`, `web/style.css`, `web/tree.js`, `web/main.js`

**변경 포인트:**
- `index.html`
  - `<header>`에서 `<button id="new-folder-btn">+ 새 폴더</button>` (line 16) **삭제**
  - `<aside id="sidebar">` 내부 트리 위에 헤더 영역 추가:
    - `<div class="sidebar-header"><button id="new-folder-btn" class="new-folder-btn" type="button">+ 새 폴더</button></div>`
  - `<script type="module" src="/main.js?v=29">` → `v=30`
- `style.css`
  - `.sidebar-header` 패딩·구분선·sticky-with-tree 동작과의 조화 확인
  - 기존 `header > .new-folder-btn` 위치/마진 규칙 정리
- `tree.js`
  - `wireTree(deps)` 시그니처에 `deleteFolder` 추가 (`_deleteFolder` 모듈 변수)
  - `buildTreeNode`에서 `renameBtn` 옆에 `deleteBtn` (✎ 옆에 🗑) 추가
  - `deleteBtn` click → `e.stopPropagation()` + `_deleteFolder(node.path)`
- `main.js`
  - `wireTree({ browse, attachDropHandlers, openRenameModal, deleteFolder })` 주입
  - `import { ..., deleteFolder } from './fileOps.js'` (이미 export 됨)

**완료 기준:**
- 사이드바 헤더에 "+ 새 폴더" 버튼 표시. 클릭 시 기존 모달, currentPath 기준 생성.
- 메인 툴바에 새 폴더 버튼 없음.
- 사이드바 트리 노드에 ✎ 옆에 🗑 표시. 클릭 시 `confirm()` → DELETE → 트리·browse 재조회.
- 모바일(<600px) 드로어: 헤더가 드로어 안으로 들어와 자연 동작.
- 트리 sticky-until-bottom 동작 회귀 없음 (`web_sticky_e2e_test.go` 기준).

**검증:**
- 수동: 데스크탑/모바일 양쪽에서 폴더 생성 + 사이드바 트리 🗑 + 메인 표 폴더 행 🗑 회귀 모두 동작.
- 자동: `go test ./...` (chromedp E2E가 sticky 회귀 검출).

---

### F4 — 폴더 DnD (트리 dragstart + 메인 표 폴더 행 dragstart + drop 라우팅 + 자기 자손 거부)

**파일:** `web/tree.js`, `web/browse.js`, `web/fileOps.js`, `web/state.js`, `web/index.html` (script 버전 bump), `web/style.css`

**변경 포인트:**
- `state.js`
  - 필요 시 `dragSrcIsDir` flag + setter 추가 (또는 payload `isDir` 필드만으로 처리해도 무방)
- `tree.js` (`buildTreeNode`)
  - `row.draggable = true` + `dragstart` / `dragend` 핸들러 부착
  - `dataTransfer.setData(DND_MIME, JSON.stringify({src: node.path, paths: [node.path], isDir: true}))`
  - `text/plain` fallback (Firefox dragstart 요구사항)
- `browse.js` (`buildTable`)
  - `if (!entry.is_dir) attachDragHandlers(tr, entry);` → 조건 제거하여 폴더 행도 draggable
- `fileOps.js`
  - `attachDragHandlers(el, entry)` 내부에서 `entry.is_dir`를 보고 payload에 `isDir: true` 포함
  - `canDropMoveTo(destPath)` 자기 자손 거부 추가 — `destPath === p` 또는 `destPath.startsWith(p + '/')`이면 false
  - drop 핸들러: `payload.isDir`이면 `moveFolder(payload.src, destPath)`, 아니면 기존 `moveFiles(paths, destPath)`
  - `moveFolder(srcPath, destDir)` 신설:
    - `PATCH /api/folder?path=...` body `{to: destDir}`
    - 실패 시 `alert('폴더 이동 실패: ' + err.error)`
    - 성공 시 `rewritePathAfterFolderRename(srcPath, newPath, currentPath)`로 navigate 결정 + `_loadTree()`
- `index.html` / `style.css`
  - 드래그 중 트리 노드 `.tree-node-row.dragging` 시각 피드백
  - drop target은 기존 `.drop-target` 클래스 그대로 재사용
  - `<script ... v=30>` → `v=31`

**완료 기준:**
- 사이드바 트리 노드 A → 트리 노드 B drop → A가 B 안으로 이동. 트리·browse 재조회.
- 트리 노드 → breadcrumb 다른 경로 drop → 동작.
- 메인 리스트 표 폴더 행 → 트리 노드 drop → 동작.
- 자기/자손 destDir로 drag → `dropEffect: 'none'` + drop 거부.
- currentPath가 이동 대상 폴더 자신/자손이면 새 경로로 자동 navigate.
- 기존 파일 DnD 회귀 0.
- 폴더는 selectedPaths에 들어가지 않음 (`bindEntrySelection` 가드 확인 — 필요 시 `entry.is_dir` 분기 추가).

**검증:** 수동 (체크포인트 ② 10케이스).

**체크포인트 ② (브라우저 E2E 수동, 10케이스):**
1. `docker compose -p file_server up -d --build` 후 `http://localhost:8080`
2. 사이드바 트리 노드 A → 트리 노드 B drop → A가 B 안으로 이동. 트리·browse 갱신.
3. 트리 노드 → breadcrumb의 `/movies` drop → 동작.
4. 메인 리스트 표 폴더 행 → 트리 노드 drop → 동작.
5. 트리 노드를 자기 자신 위로 drag → `dropEffect: 'none'`, drop 거부.
6. 트리 노드를 자기 자손 위로 drag → 동일 거부.
7. 동일 부모 destDir로 drag → 거부.
8. 충돌(destDir에 동일 이름 폴더 존재) → alert "이미 같은 이름이 있습니다" 류.
9. currentPath가 `/a/sub`인 상태에서 `/a/sub` → `/b` 이동 → URL이 `/b/sub`로 자동 갱신 + browse 재조회.
10. 회귀: 단일 파일 DnD, 다중 파일 DnD, 사이드바 ✎ rename, 사이드바 🗑 delete, 새 폴더 생성, URL import, 변환 — 모두 정상.

---

### F5 — README 갱신 + 0.0.1 릴리즈 정리

**파일:** `README.md`

**변경 포인트:**
- features 목록에 폴더 작업 4종(생성·이름변경·삭제·이동) 명시. 사이드바 트리 운영 동선 한 줄.
- 0.0.1 릴리즈 노트 섹션 추가 — Phase 24 변경 요약 + breaking change 없음 명시.
- 기존 "폴더 생성/삭제" 설명이 메인 툴바 기준이면 사이드바 헤더로 갱신.

**완료 기준:**
- README features 목록이 SPEC §2.1 체크리스트와 일치.
- 0.0.1 릴리즈 노트 → Phase 24의 4가지 변경 모두 언급.
- 기존 사용 방법 설명이 새 UI와 일치(스크린샷은 별도 작업으로 분리).

**체크포인트 ③ (SPEC §10 모든 항목 체크):**
- [ ] 폴더 이동 백엔드 (F1+F2)
- [ ] 폴더 이동 UI (F4)
- [ ] 사이드바 트리 노드 🗑 삭제 버튼 (F3)
- [ ] 새 폴더 버튼 위치 이동 (F3)
- [ ] README 갱신 (F5)

---

### Out of scope (Phase 24)
- 사이드바 트리 노드별 + 버튼으로 임의 위치 폴더 생성
- 컨텍스트 메뉴 (우클릭) UI
- 다중 폴더 선택 이동
- cross-volume 폴더 이동 (EXDEV 재귀 copy 폴백)
- `/api/version` 엔드포인트, GitHub release 자동화
- README 스크린샷 갱신

### 위험 / 롤백
- **위험: drop 핸들러 자기 자손 검사가 prefix만 보면 `/a` → `/abc` 가짜양성** — `destPath.startsWith(p + '/')`로 separator 명시(서버 `MoveDir`과 동일 규칙). 수동 시나리오 6번에서 확인.
- **위험: 폴더 multi-select이 우연히 활성화되면 폴더 다중 이동 발생** — `bindEntrySelection`의 `is_dir` 분기 확인. 없으면 폴더는 selectedPaths에 들어가지 않도록 가드.
- **위험: 새 폴더 버튼 이전으로 모바일 드로어 닫힌 상태에선 새 폴더 생성 불가** — 사용자가 햄버거를 먼저 열어야 함. 의도된 동선이라 acceptable. README 한 줄 명시.
- **위험: `rewritePathAfterFolderRename` 재사용 시 의미 차이** — 폴더 rename은 `/a/old → /a/new`, 폴더 move는 `/a/sub → /b/sub`. 헬퍼가 srcOldPath/destNewPath의 prefix 치환만 하면 둘 다 안전. `web/util.js` 구현 확인 후 재사용 검증.
- **롤백:** Phase 24 머지 단위로 revert → Phase 23 상태 복귀. SPEC.md §2.1.2/§2.1.3/§10도 동시 revert.

---

## Phase 25 — PNG → JPG 변환 (`feature/png-to-jpg`) — spec [`SPEC.md §2.8`](../SPEC.md)

PNG 업로드 시 자동 JPEG 변환 + 기존 PNG 파일을 명시적으로 변환하는 두 진입점. 기존 `disintegration/imaging` 라이브러리만 재사용하여 신규 의존성 없음. 알파 채널은 흰 배경 합성으로 처리(JPEG 구조적 한계). settings 토글로 자동 변환 ON/OFF.

**의존성:**
```
PJ1 (internal/imageconv 패키지 — ConvertPNGToJPG + 흰 배경 합성 + atomic write + 단위 테스트)
  ├─► PJ3 (handleUpload PNG 자동 변환 분기 + 응답 스키마 확장 + 폴백 로직 + 통합 테스트)
  │      ↑ PJ2
  └─► PJ4 (POST /api/convert-image 동기 핸들러 + 라우트 등록 + 통합 테스트)
              └─► PJ5 (frontend — 카드 버튼 · 툴바 일괄 · 모달 · 자동 변환 응답 처리 + 수동 E2E)
PJ2 (settings AutoConvertPNGToJPG 필드 + GET/PATCH 통과 + UI 체크박스)
  └─► PJ3
```

PJ1과 PJ2는 leaf로 병렬 가능. PJ3는 PJ1+PJ2가 모두 끝나야 시작. PJ4는 PJ1만 의존. PJ5는 PJ3+PJ4 모두 의존(자동 변환 응답 UI 처리 포함).

### PJ1 — `internal/imageconv` 패키지

**파일:** `internal/imageconv/imageconv.go`, `internal/imageconv/imageconv_test.go`

**변경 포인트:**
- 단일 export `ConvertPNGToJPG(srcPath, destPath string, quality int) error`. 호출 측은 확장자 검증을 끝낸 상태로 호출 — 패키지는 path 검증을 수행하지 않는다(handler 책임).
- 내부 흐름:
  1. `imaging.Open(srcPath)` 로 PNG 디코드 (반환은 `*image.NRGBA` — 알파 보존).
  2. `bounds := src.Bounds()` 로 동일 크기 `*image.RGBA` 생성.
  3. `draw.Draw(dst, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)` — 흰색 fill.
  4. `draw.Draw(dst, bounds, src, bounds.Min, draw.Over)` — 합성. 불투명 픽셀은 그대로, 알파는 흰색과 over.
  5. atomic write — `os.CreateTemp(filepath.Dir(destPath), ".imageconv-*.jpg")` 로 임시 파일 → `jpeg.Encode(tmp, dst, &jpeg.Options{Quality: quality})` → close → `os.Rename(tmp.Name(), destPath)`.
  6. 모든 에러 경로(decode/encode/write/rename 실패)에서 임시 파일은 `os.Remove`로 정리 — `defer`로 cleanup이 한 번만 실행되도록 변수 플래그(`renamed`) 사용.
- `quality` 인자: handler에서 항상 90 전달. 0 또는 음수면 `image/jpeg` 스펙대로 75를 쓰지만, 본 패키지는 **0 ≤ q ≤ 100 검증**을 추가해 `fmt.Errorf("imageconv: quality out of range: %d", q)` 반환. 잘못 사용된 경우 조기 발견.
- **import:** `github.com/disintegration/imaging`(이미 go.mod에 있음), `image`, `image/color`, `image/draw`, `image/jpeg`, `os`, `path/filepath`.
- **무엇을 하지 않는지(명시):** SafePath 검증 X, 확장자 검증 X, 입력 파일 잠금/원자성 X, 사이드카 처리 X — 모두 호출자(handler) 책임.

**완료 기준:** `go test ./internal/imageconv` 통과 — 8개 케이스:
1. RGB PNG (알파 없음) → JPEG 디코드 가능 + dimensions 일치
2. RGBA PNG (반투명/완전투명 픽셀 포함) → 알파였던 위치 RGB가 흰색에 가까움(검사용 픽셀 샘플)
3. 손상 PNG (헤더 truncate) → decode 에러 + temp 파일 미잔존
4. src 미존재 → `os.ErrNotExist` 계열 에러 wrap
5. dest 디렉토리 미존재 → `os.CreateTemp` 에러 wrap + temp 미생성
6. quality 음수/100 초과 → 인자 검증 에러 (decode 시도 안 함)
7. atomic 검증: 정상 종료 후 `.imageconv-*.jpg` glob 매치 0개
8. 출력 확장자는 호출자 결정: `destPath`가 `.jpeg`이든 `.jpg`이든 패키지는 그대로 사용

### PJ2 — `settings` 확장 + UI 체크박스

**파일:** `internal/settings/settings.go`, `internal/settings/settings_test.go`, `internal/handler/settings_test.go`, `web/index.html`, `web/style.css`, `web/settings.js`, `web/main.js` (버전 bump)

**변경 포인트:**
- `settings.go`:
  - `Settings` 구조체에 `AutoConvertPNGToJPG bool \`json:"auto_convert_png_to_jpg"\`` 추가.
  - `Default()` 가 `AutoConvertPNGToJPG: true` 반환.
  - `Validate(Settings)` 는 boolean이라 추가 검증 불필요(zero value `false`도 유효한 OFF 상태).
  - **레거시 키 누락 처리** — JSON 디코드는 키 부재 시 zero value `false`가 되므로 SPEC §2.7 기본값(true)과 어긋난다. `New()` 에서 1차 디코드를 `map[string]json.RawMessage` 로 받아 `auto_convert_png_to_jpg` 키 존재 여부를 검사 → 부재 시 `loaded.AutoConvertPNGToJPG = true` 강제 적용. 다음 PATCH 때 디스크에 정식 저장됨. (대안 — `Settings`에 별도 presence flags 도입 — 복잡도 상승. 1차 디코드 패턴이 가벼움.)
- `settings_test.go` 갱신:
  - `TestDefault` 에 `AutoConvertPNGToJPG == true` 어서션 추가.
  - 기존 `TestRoundTrip` 에 `AutoConvertPNGToJPG: false` 케이스 추가(true→false→true 라운드트립).
  - 신규 `TestNew_LegacyMissingKey`: 디스크에 `{"url_import_max_bytes":..., "url_import_timeout_seconds":...}` (auto_convert 키 없음) 직접 작성 → `New()` 후 `Snapshot().AutoConvertPNGToJPG == true`.
- `handler/settings_test.go`:
  - 기존 GET/PATCH round-trip 테스트가 새 필드를 자동으로 직렬화하는지 확인. 필요 시 expected JSON에 `auto_convert_png_to_jpg: true` 추가.
  - 신규 `TestSettings_PATCH_BooleanTypeMismatch`: `{"auto_convert_png_to_jpg": "yes"}` → `400 invalid request` (JSON unmarshal 자체가 실패).
- `web/index.html` `#settings-modal` 마크업: 기존 두 필드 아래에 `.settings-field` 추가
  ```html
  <div class="settings-field">
    <label class="settings-label">
      <input type="checkbox" id="settings-auto-png">
      PNG 업로드 시 JPG로 자동 변환
    </label>
    <p class="settings-hint">알파 채널은 흰 배경에 합성됩니다 (quality 90).</p>
  </div>
  ```
- `web/style.css` 추가:
  - `.settings-field input[type="checkbox"]` margin-right 8px, vertical-align middle.
  - 체크박스 라벨은 cursor pointer.
- `web/settings.js`:
  - DOM ref 1개 추가 (`settingsAutoPNGEl`).
  - `openSettingsModal()` 의 GET 응답 처리에 `el.checked = !!data.auto_convert_png_to_jpg` 추가.
  - `submitSettings()` 의 PATCH payload에 `auto_convert_png_to_jpg: settingsAutoPNGEl.checked` 추가.
- `web/main.js` 또는 `index.html` `<script>` 태그 버전 v=N → v=N+1 bump.

**완료 기준:** `go test ./internal/settings ./internal/handler -run TestSettings` 통과. 브라우저에서 ⚙ → 모달 → 체크박스 토글 → 저장 → 모달 재오픈 시 상태 유지. settings.json 디스크에 새 키 존재.

### PJ3 — `handleUpload` 자동 PNG → JPG 변환

**파일:** `internal/handler/files.go`, `internal/handler/files_test.go`, `web/fileOps.js` 또는 응답 처리 모듈, `web/main.js`

**변경 포인트:**
- `files.go` `handleUpload`:
  - 기존 `createUniqueFile(destPath)` 직전 분기 추가:
    ```
    snap := h.settingsSnapshot()
    isPNG := strings.EqualFold(filepath.Ext(part.FileName()), ".png")
    autoConvert := isPNG && snap.AutoConvertPNGToJPG
    ```
  - `autoConvert == false` (현재 흐름 유지):
    - `createUniqueFile(destPath)` → `io.Copy` → 응답에 `converted: false, warnings: []` 추가.
  - `autoConvert == true`:
    1. `tmpPNG, err := os.CreateTemp(destDir, ".pngconvert-*.png")` (browse 자동 숨김 — `.`-prefix).
    2. `io.Copy(tmpPNG, part)` → close (defer cleanup wrapper로 모든 경로에서 잔존 안 하도록).
    3. 목표 경로 계산: `jpgBase := strings.TrimSuffix(filepath.Base(destPath), filepath.Ext(destPath)) + ".jpg"`; `jpgPath := filepath.Join(destDir, jpgBase)`.
    4. 임시 jpg 경로(`tmpJPGPath := tmpPNG.Name() + ".jpg"`)를 결정 → `imageconv.ConvertPNGToJPG(tmpPNG.Name(), tmpJPGPath, 90)` 호출. `imageconv` 의 atomic write가 `tmpJPGPath` 를 안전하게 만들고, 핸들러는 이후 `tmpJPGPath` → `jpgPath`(unique 처리) 로 한 번 더 rename.
    5. 변환 성공:
       - `os.Remove(tmpPNG.Name())`.
       - `finalPath, suffixApplied, err := renameToUniqueDest(tmpJPGPath, jpgPath)` (신규 헬퍼 — `O_CREATE|O_EXCL` 패턴으로 `_1`, `_2` 후보 생성 후 `os.Rename`).
       - 응답: `path: finalPath`, `name: filepath.Base(finalPath)`, `size: <stat>`, `type: image`, `converted: true`, `warnings`: `suffixApplied ? ["renamed"] : []`.
       - 썸네일 풀 제출은 `finalPath` 대상으로.
    6. 변환 실패 (decode/encode/write 어느 단계든):
       - `os.Remove(tmpJPGPath)` (있으면).
       - 폴백: `tmpPNG.Name()` 을 원래 `destPath` 로 rename — `renameToUniqueDest(tmpPNG.Name(), destPath)` 재사용. suffix 발생 시 `warnings: ["renamed", "convert_failed"]`.
       - `slog.Warn("png auto-convert failed, falling back to original", "src", destPath, "err", err)`.
       - 응답: `converted: false`, `warnings: ["convert_failed"]` (suffix 있으면 추가).
- 신규 헬퍼 `renameToUniqueDest(srcPath, destPath string) (finalPath string, suffixed bool, err error)`:
  - destPath 가 비어있으면 즉시 시도, EEXIST 시 `_1.<ext>`, `_2.<ext>` 등 createUniqueFile 패턴으로 후보 생성.
  - 각 시도는 `O_CREATE|O_EXCL` 로 빈 파일 만든 뒤 `os.Rename(srcPath, candidate)` — atomic + race-free.
  - 위치: `files.go` (createUniqueFile 인접).
- 응답 스키마 확장 (`uploadResponse` 또는 inline map):
  - `converted bool` + `warnings []string` 필드 추가. Warnings nil이면 `[]`로 직렬화 (SPEC §5에 빈 배열로 명시 — `omitempty` 사용 안 함).
- `files_test.go` 신규 테스트 케이스 7개:
  1. `auto_convert=true` + RGB PNG 업로드 → 응답 `converted:true`, 디스크에 `.jpg`만, `.png`/`.pngconvert-*` 미잔존, 썸네일 비동기 생성 트리거됨
  2. `auto_convert=true` + RGBA PNG 업로드 → 알파 위치가 흰색으로 합성된 JPEG (디코드 후 픽셀 샘플 검사)
  3. `auto_convert=false` + PNG 업로드 → 원본 `.png` 그대로 저장, `converted:false`
  4. `auto_convert=true` + 손상 PNG (디코드 실패하는 fixture) → 폴백, 원본 `.png` 저장, `warnings:["convert_failed"]`, 응답 코드 201
  5. `auto_convert=true` + `foo.jpg` 사전 존재 + `foo.png` 업로드 → 결과 `foo_1.jpg`, `warnings:["renamed"]`
  6. `auto_convert=true` + 비-PNG (jpg/mp4) 업로드 → 영향 없음, `converted:false`, `warnings:[]`
  7. `auto_convert=true` + 대문자 `.PNG` 업로드 → 변환됨, 출력 `.jpg`(소문자)
- `web/fileOps.js` (또는 업로드 응답을 처리하는 모듈) 응답 파서에 `converted`/`warnings` 처리 추가:
  - 기존 success 알림에 `converted: true` 면 "PNG가 JPG로 변환되어 저장됨" 한 줄 inline 메시지(toast 또는 업로드 영역 아래 하이라이트 1.5초).
  - `warnings.includes('convert_failed')` 면 "PNG 변환 실패, 원본으로 저장됨" 경고 색.
  - `warnings.includes('renamed')` 면 "파일명이 자동 변경되었습니다" — 기존 URL import 라벨 재사용.
- main.js / index.html 버전 bump.

**완료 기준:** `go test ./internal/handler -run TestUpload` 신규 7케이스 + 기존 회귀 통과. 브라우저에서 PNG drag&drop → JPG 카드 노출 + 한국어 inline 메시지 표시.

### PJ4 — `POST /api/convert-image` 동기 핸들러

**파일:** `internal/handler/convert_image.go` (신규), `internal/handler/convert_image_test.go` (신규), `internal/handler/handler.go` (라우트). PJ3에서 만든 `renameToUniqueDest` 헬퍼는 본 핸들러에서는 미사용(충돌 시 `already_exists` 반환이라 unique 회피 불필요).

**변경 포인트:**
- `convert_image.go`:
  - 상수 `maxConvertImagePaths = 50`, `imageConvertFileTimeout = 30 * time.Second`, `imageJPEGQuality = 90`.
  - 요청/응답 타입:
    ```go
    type convertImageRequest struct {
        Paths          []string `json:"paths"`
        DeleteOriginal bool     `json:"delete_original"`
    }
    type convertImageResult struct {
        Index    int      `json:"index"`
        Path     string   `json:"path"`
        Output   string   `json:"output,omitempty"`
        Name     string   `json:"name,omitempty"`
        Size     int64    `json:"size,omitempty"`
        Warnings []string `json:"warnings,omitempty"`
        Error    string   `json:"error,omitempty"`
    }
    type convertImageResponse struct {
        Succeeded int                  `json:"succeeded"`
        Failed    int                  `json:"failed"`
        Results   []convertImageResult `json:"results"`
    }
    ```
  - `handleConvertImage(w, r)`:
    - POST 외 → 405 method not allowed.
    - JSON decode → 실패 400 invalid request.
    - 길이 0 → 400 no paths, > 50 → 400 too many paths.
    - 각 path 순차 처리:
      1. `media.SafePath(h.dataDir, p)` → 실패 시 result.Error = `"invalid_path"`.
      2. `os.Stat` → 미존재 `"not_found"`, 디렉토리 `"not_a_file"`.
      3. 확장자 `strings.EqualFold(filepath.Ext(abs), ".png") == false` → `"not_png"`.
      4. 목표 경로 `dst := strings.TrimSuffix(abs, ext) + ".jpg"` (소문자 고정).
      5. `os.Stat(dst) == nil` → `"already_exists"` (자동 suffix 없음 — SPEC §2.8.2).
      6. `fctx, cancel := context.WithTimeout(r.Context(), imageConvertFileTimeout)`; `defer cancel()`. 별도 goroutine에서 `imageconv.ConvertPNGToJPG(abs, dst, imageJPEGQuality)` 호출 → channel로 결과 수신. `select { case <-fctx.Done(): error="canceled"|"convert_timeout" case res := <-done: ... }` 분기는 `errors.Is(fctx.Err(), context.DeadlineExceeded)` 으로 결정. ctx done이 먼저 와도 goroutine은 끝까지 실행되며 결과 임시 파일은 `imageconv` 자체 cleanup이 처리.
      7. 성공: `delete_original == true` 면 원본 PNG + `.thumb/{name}.png.jpg` 삭제 (best-effort, 실패 시 `result.Warnings = append(..., "delete_original_failed")`). 사이드카는 이미지에 `.dur` 없음 — `.jpg` 1개만 처리.
      8. result 채움: `Output`, `Name`, `Size` (Stat dst).
    - 각 항목 결과에 따라 `succeeded` / `failed` 누적.
    - 응답 `200 OK` + JSON.
- `handler.Register` 라우트 1줄 추가:
  ```go
  mux.HandleFunc("/api/convert-image", requireSameOrigin(h.handleConvertImage))
  ```
- `convert_image_test.go` — 12개 케이스:
  1. 정상 PNG 1개 → 200 + succeeded:1 + `.jpg` 디스크 + 원본 유지
  2. `delete_original:true` → 원본 + 사이드카 삭제 확인
  3. 배열 2개 → 결과 2건, 둘 다 `.jpg` 존재
  4. 충돌: `foo.jpg` 사전 존재 → `error: "already_exists"` + 임시 파일 미생성
  5. 부분 실패: `[good.png, bad.txt]` → 0번 done + 1번 `not_png`
  6. `delete_original_failed` 경고: 사이드카만 read-only 디렉토리에 두고 `delete_original:true` → 변환 성공 + warning
  7. 빈 배열 → 400 no paths
  8. 51개 → 400 too many paths
  9. 잘못된 JSON → 400 invalid request
  10. GET → 405 method not allowed
  11. traversal: `["../../etc/passwd"]` → 항목 `invalid_path`
  12. 손상 PNG → 항목 `decode_failed` + 임시 파일 정리

**체크포인트 ① (PJ4 종료):** curl 단독 검증 5단계
1. `curl -X POST localhost:8080/api/convert-image -H 'Origin: http://localhost:8080' -d '{"paths":["test.png"],"delete_original":false}'` → JSON 응답 + 디스크에 test.jpg
2. 충돌: 같은 호출 반복 → `already_exists`
3. delete_original=true → 원본 사라짐
4. traversal: `"../../etc/passwd"` → `invalid_path`
5. 405: `curl -X GET ...` → method not allowed

**완료 기준:** `go test ./internal/handler -run TestConvertImage` 12 케이스 + 회귀 전부 통과. 위 5단계 curl 모두 기대 동작.

### PJ5 — frontend UI + 수동 E2E

**파일:** `web/convertImage.js` (신규), `web/browse.js`, `web/main.js`, `web/index.html`, `web/style.css`

**변경 포인트:**
- `web/convertImage.js` 신규 모듈 (Phase 23 모듈화 패턴 일관):
  - export `openConvertImageModal(paths: string[], onComplete: () => void)`, `setConvertImageDeps({...})` (콜백 주입 — `loadBrowse`).
  - 모달 마크업은 `index.html` 의 신규 `#convert-image-modal` (TS 변환 모달 스타일 미러). 본문: "{N}개의 PNG를 JPG로 변환하시겠습니까?" + `<input type="checkbox">` "변환 후 원본 PNG 삭제".
  - `submitConvertImage()`: `fetch('/api/convert-image', {method:'POST', body:JSON.stringify({paths, delete_original})})` → 응답 파싱 → 성공 카운트 + 실패 항목 한국어 메시지(`CONVERT_IMAGE_ERROR_LABELS`) → 알림 → `loadBrowse()` 트리거.
  - `CONVERT_IMAGE_ERROR_LABELS` (한국어):
    - `invalid_path: '경로 오류'`
    - `not_found: '파일 없음'`
    - `not_a_file: '폴더는 변환 불가'`
    - `not_png: 'PNG 파일이 아님'`
    - `already_exists: '대상 JPG 이미 존재'`
    - `decode_failed: 'PNG 디코드 실패'`
    - `encode_failed: 'JPEG 인코드 실패'`
    - `write_failed: '저장 실패'`
    - `convert_timeout: '변환 시간 초과'`
    - `canceled: '취소됨'`
- `web/browse.js`:
  - `buildImageGrid` 에서 카드 생성 시 `entry.mime === 'image/png'` 면 "JPG로 변환" 버튼 추가 (rename/delete 버튼과 동일 layout, `.png-convert-btn` 클래스). 클릭 → `openConvertImageModal([entry.path], …)` 호출.
  - `renderView` (또는 툴바 갱신 함수)에서 visible PNG 개수 계산 → ≥1 이면 `#convert-png-all-btn` 표시 + "모든 PNG 변환 (N)". 클릭 → `openConvertImageModal(visiblePNGs.map(e => e.path), …)`.
- `web/main.js`:
  - `convertImage` 모듈 import + `setConvertImageDeps({ loadBrowse: _browse })` 주입.
  - `browse.js` 가 expose하는 콜백 receiver에 `openConvertImageModal` 주입.
- `web/index.html`:
  - 툴바에 `<button id="convert-png-all-btn" hidden>` 추가 (TS 일괄 변환 버튼 옆 또는 그룹 내).
  - `#convert-image-modal` 마크업 (확인 메시지 + 체크박스 + 버튼 2개).
  - `<script>` 버전 bump.
- `web/style.css`:
  - `.png-convert-btn` (entry-button 패턴 — rename/delete와 동일 hover/focus).
  - `#convert-image-modal` (TS 모달 스타일 재사용 — `.modal` 베이스 활용).
  - `#convert-png-all-btn` 가 `#convert-all-btn`(TS) 와 시각적으로 구분되게 — 색이나 아이콘 차이.

**수동 E2E 시나리오 (12개):**
1. ⚙ → 모달에서 "PNG 자동 변환" 체크박스 표시 (기본 ON).
2. 토글 OFF → 저장 → 새로고침 후 OFF 유지.
3. PNG drag&drop 업로드 (자동 변환 ON) → 카드는 `.jpg`, inline 메시지 "PNG가 JPG로 변환됨".
4. PNG drag&drop 업로드 (자동 변환 OFF) → 카드는 `.png` 그대로.
5. 알파 PNG 업로드 → JPG 다운로드 후 다른 뷰어에서 흰 배경 합성 확인.
6. 손상 PNG 업로드 → `.png` 저장 + 경고 메시지 "변환 실패, 원본 저장".
7. `foo.jpg` 사전 존재 + `foo.png` 업로드 → `foo_1.jpg` 생성 + 자동 rename 메시지.
8. PNG 카드 "JPG로 변환" 버튼 → 모달 → 확인 → 카드가 `.jpg`로 갱신.
9. PNG 카드 변환 + "원본 PNG 삭제" 체크 → 변환 후 원본 카드 사라짐.
10. 폴더 PNG 3개 + JPG 2개 → 툴바 "모든 PNG 변환 (3)" 노출, 클릭 → 3개 변환 → 알림 "성공 3 / 실패 0".
11. 충돌: `foo.jpg` + `foo.png` 동시 존재 + 수동 변환 → 결과 알림 "1건 실패 (대상 JPG 이미 존재)".
12. 모바일 브라우저(<600px) — 카드 버튼·툴바 버튼·모달 정상 표시.

**완료 기준:** 12개 시나리오 모두 기대 동작. 콘솔 에러 0. 회귀: 기존 업로드/rename/delete/TS 변환 각 1회 스모크.

### Out of scope (Phase 25)
- JPEG 외 출력 포맷(WEBP, AVIF) 변환.
- PNG 외 입력 포맷(BMP, TIFF, HEIC) 변환.
- JPEG quality 사용자 조절(90 고정).
- 알파 채널 보존 옵션(흰 배경 합성 강제).
- EXIF/메타데이터 보존.
- URL import 결과의 자동 변환.
- 자동 변환 시 원본 PNG 별도 저장(원본을 원하면 자동 변환 OFF).
- progress 이벤트 / SSE.
- 동시 변환(배열은 항상 순차).
- 변환 큐 / 잡 영속화.

### 위험 / 롤백
- **위험: settings 마이그레이션** — 기존 `settings.json` 에 `auto_convert_png_to_jpg` 키가 없을 때 zero value `false`가 되면 사용자 의도와 어긋난다(SPEC 기본값은 true). PJ2 의 `New()` 후 누락 키를 true로 강제하는 로직이 필수. 검증: `TestNew_LegacyMissingKey` 가 잡는다.
- **위험: 자동 변환 폴백 시 응답 코드** — 변환 실패해도 업로드는 201로 반환해야 데이터 손실 인식이 안 생긴다. `warnings` 에서만 신호. PJ3 통합 테스트 4번 케이스가 잡는다.
- **위험: 임시 파일 누출** — `.pngconvert-*.png` / `.imageconv-*.jpg` / `.pngconvert-*.jpg` 가 destDir에 남으면 browse는 dot-prefix로 숨기지만 디스크 공간을 차지한다. PJ1 테스트 7번(atomic glob 검증) + PJ3 테스트 1번(미잔존 검증).
- **위험: 알파 합성 색이 사용자 기대와 다름** — 검정 배경 PNG 위에 흰 폰트는 흰 배경 합성 시 가시성 무너진다. 본 SPEC는 흰 고정이 명시이므로 사용자 책임이지만, 필요 시 후속에 배경 색 옵션 도입 검토.
- **위험: 디스크 풀 / 권한** — atomic write 중간 실패는 `imageconv` 에러로 전파 → handler가 `convert_failed` warning 으로 폴백 또는 `write_failed` error로 응답. 임시 파일은 cleanup.
- **위험: 큰 PNG (수십 MB) 디코드 메모리** — `imaging.Open` 은 전체 이미지를 메모리에 올린다. 단일 사용자 가정 + 일반 사진 크기에서 문제 없음. 4K 스크린샷도 ~50MB RAM. 별도 cap 도입 안 함.
- **롤백:** Phase 25 머지 단위로 revert → Phase 24 상태 복귀. settings.json 의 `auto_convert_png_to_jpg` 키는 남지만 서버가 무시(미지의 필드는 `DisallowUnknownFields`로 PATCH 거부 — 실제로는 GET/디스크 로드는 허용이라 무해). 기존 사용자 PNG 파일은 변환 안 됨(자동 변환은 신규 업로드부터만 적용된 상태였음). 데이터 손실 없음.

## Phase 27 — Rubber-band 영역 선택 (`feature/drag-select`)

**Spec:** [SPEC.md §2.5.4](../SPEC.md) (Rubber-band 영역 선택)

빈 영역에서 시작한 마우스 드래그로 사각형을 그려 그 안의 visible 카드를 일괄 선택한다. Phase 22의 `selectedPaths` 인프라를 그대로 활용 — 별도 selection 상태 미도입. 백엔드 변경 없음.

**의존 그래프**

```
  state.js (selectedPaths)         ─┐
  browse.js (.thumb-card / tr)     ─┼→ 신규 web/dragSelect.js ─→ DOM overlay
  main.js (wire)                   ─┘
```

selection 변경 → `setSelected` → `renderView` → 카드 `.selected` 클래스 갱신 (기존 흐름 그대로).

**범위**
- 변경 파일: 신규 `web/dragSelect.js`, 수정 `web/main.js`(wire), `web/style.css`(overlay 스타일), `web/index.html`(버전 bump).
- 변경 없음: 백엔드 전부 / browse.js / state.js / fileOps.js (드래그 이동 로직 무영향).

### DS-1 — SPEC + plan + todo (선행 커밋)

**파일:** `SPEC.md`, `tasks/plan.md`, `tasks/todo.md`

- SPEC.md §2.5.4 신설(활성 조건/상호작용/modifier/대상/시각/Non-goals/서버 변경 없음) + §2.5 글머리 한 줄 추가.
- tasks/plan.md Phase 27 섹션 신규.
- tasks/todo.md Phase 27 entry 신규.

**완료 기준:** 문서만 변경, `go test ./... && go vet ./...` 회귀 통과.

### DS-2 — `web/dragSelect.js` 구현

**파일:** 신규 `web/dragSelect.js`, 수정 `web/main.js`, `web/style.css`, `web/index.html`

- `wireDragSelect()` export — main 영역 mousedown 핸들러 등록.
- 빈 영역 판정: `e.target.closest('.thumb-card, tr, button, a, input, label, .lightbox, .modal-overlay, .audio-player') == null`.
- 5px movement threshold 후 overlay 생성. 단순 클릭(<5px)은 selection 미변경.
- visible 카드 위치(`getBoundingClientRect()`)는 mousedown 시점에 **1회 캐시** → mousemove 동안 재계산 안 함 (성능, 200카드도 안전).
- intersect 판정: 사각형 vs 카드 rect overlap (모서리 닿음 포함).
- modifier 분기: `e.ctrlKey || e.metaKey || e.shiftKey` → additive (기존 selection 유지 + 추가), 아니면 기존 selection 클리어 후 사각형 결과 적용.
- mousedown 시점 selection 스냅샷(`new Set(selectedPaths)`) 보관 → ESC 시 복원.
- mouseup/cancel 시 cleanup (overlay 제거, document 임시 listener 해제, 텍스트 선택 차단 클래스 제거).
- viewport 너비 `≤ 600px`이면 wireDragSelect 진입 즉시 return — 모바일 비활성.
- main.js에 `wireDragSelect()` 호출 추가.
- `style.css` `.drag-select-overlay` 규칙 (position: absolute, 반투명 accent, pointer-events: none, z-index < 모달).
- index.html main.js 버전 bump (Phase 26 반영 상태에 맞춰 v=37).

**완료 기준:**
- 빈 영역 좌클릭 → 5px 미만은 click으로 처리, 5px 초과는 사각형 + selection 갱신.
- 카드 위에서 시작하면 rubber-band 미발생 (기존 폴더 이동 DnD 그대로).
- 우/중 클릭 무시. 모바일 viewport 무동작.
- ESC로 드래그 시작 시점 selection 복원.
- 폴더 카드는 사각형이 덮어도 미선택. 필터로 가려진 항목 미선택.

**검증:**
- `go test ./... && go vet ./...` (백엔드 무영향 — 무조건 pass).
- 코드 리뷰: visible 카드 rect 캐시는 mousedown 후 mouseup까지 변하지 않는다는 가정(드래그 도중 browse 재로드 트리거 없음).

### DS-3 — Docker rebuild + 수동 E2E

**`docker compose up -d --build`** 후 시나리오 7개:
1. 빈 영역 클릭+짧게 드래그(<5px) → selection 변경 없이 단순 click 처리.
2. 빈 영역 드래그 → 반투명 사각형, 안 카드 실시간 선택.
3. 카드 위에서 드래그 시작 → 기존 폴더 이동 DnD (rubber-band 미발생).
4. Ctrl+드래그 → 기존 selection 유지 + 사각형 항목 추가.
5. Shift+드래그 → Ctrl과 동일.
6. 드래그 중 ESC → overlay 사라지고 시작 시점 selection 복원.
7. 모바일 viewport(<600px) → 동작 안 함 (기존 체크박스만).

회귀: 카드 단일 클릭(lightbox/play), 카드 드래그(폴더 이동), PNG 일괄 변환 selection 연동, 텍스트 선택 가능 영역(파일명 등) 무영향.

**체크포인트:** 7개 통과 + 회귀 4건 스모크. 통과 시 develop 머지.

### Out of scope (Phase 27)
- 모바일/터치 long-press + drag.
- 사이드바 트리에서 영역 선택.
- 키보드 화살표 + Shift 범위 선택.
- 드래그 중 viewport 자동 스크롤(사각형이 viewport 끝에 닿을 때 자동 따라감).
- 사각형 줄어들 때 toggle off (additive only).

### 위험 / 롤백
- **위험: 클릭 vs 짧은 드래그 충돌** — 5px movement threshold로 분기. threshold 미만이면 selection 미변경.
- **위험: HTML5 dragstart와 충돌** — mousedown이 카드에서 시작하면 HTML5 dragstart 발화하고 우리 handler는 빈 영역 판정에서 빠짐. 충돌 없음.
- **위험: 성능 (대량 카드)** — 200 카드 × 60fps = 12k rect 계산. mousedown 시점 1회 캐시로 회피, mousemove는 사각형 ↔ 캐시 비교만 O(N).
- **위험: 캐시 stale** — 드래그 중 카드 추가/제거가 일어나는 케이스(lazy load, 외부 갱신). visible 카드는 이미 mousedown 시점에 모두 렌더된 상태이고 드래그 중 browse 재로드 트리거가 없으므로 안전.
- **위험: ESC 복원 정확성** — mousedown 시점 `new Set(selectedPaths)` 스냅샷으로 보장.
- **롤백:** `web/dragSelect.js` 삭제 + main.js wire 라인 제거 + CSS overlay 규칙 제거 + index.html 버전 되돌리기. 다른 모듈 무영향.

