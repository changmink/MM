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
