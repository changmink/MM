# Multimedia Server — Specification

## 1. Objective

개인용 로컬 네트워크 멀티미디어 서버. 이미지 섬네일 생성, 음악/동영상 스트리밍, 파일 업로드를 제공한다. 인증 없이 사용하며 Docker로 배포한다.

**Target users:** 개인 (단일 사용자, 로컬 네트워크)  
**Deployment:** Docker + named volume (미디어 파일 영속 저장)

---

## 2. Core Features & Acceptance Criteria

### 2.1 파일 관리
- [ ] 파일 업로드 (multipart/form-data, 최대 파일 크기 제한 없음)
- [ ] Docker volume에 마운트된 디렉토리(`/data`)에 파일 저장
- [ ] 디렉토리 트리 탐색 (폴더 구조 그대로 노출)
- [ ] 파일 삭제
- [ ] 폴더 생성 (현재 탐색 경로 기준, 이름 입력 모달)
- [ ] 폴더 삭제 (재귀 삭제 — 하위 파일/폴더 + `.thumb/` 디렉토리 포함)

### 2.2 이미지
- **지원 포맷:** JPG, PNG, WEBP, GIF
- [ ] 업로드 시 섬네일 자동 생성 (200×200px, JPEG)
- [ ] 섬네일은 원본과 동일 경로에 `.thumb/` 디렉토리에 저장
- [ ] 원본 이미지 서빙

### 2.3 동영상 스트리밍
- **지원 포맷:** MP4, MKV, AVI (원본 스트리밍), TS (ffmpeg 트랜스코딩)
- [ ] HTTP Range 요청 지원 (seek 가능)
- [ ] MP4/MKV/AVI: 트랜스코딩 없이 원본 파일 스트리밍
- [ ] TS: ffmpeg로 실시간 MP4 트랜스코딩 후 스트리밍 (`Content-Type: video/mp4`)
- [ ] MIME 타입 자동 감지

### 2.3.1 동영상 섬네일
- **지원 포맷:** MP4, MKV, AVI, TS (전체)
- [ ] `GET /api/thumb?path=` 에서 동영상 파일도 섬네일 반환 (기존 이미지와 동일 엔드포인트)
- [ ] ffmpeg로 프레임 추출 → 200×200px JPEG (이미지 섬네일과 동일 크기)
- [ ] 섬네일은 원본과 동일 경로의 `.thumb/` 디렉토리에 저장 (캐시)
- [ ] **프레임 추출 전략 (순서대로 시도):**
  1. 영상 길이의 50% 시점 추출
  2. 추출된 프레임이 모두 검정(모든 픽셀 R+G+B < 10) 또는 모두 흰색(모든 픽셀 R+G+B > 745)이면 25% 시점 재시도
  3. 25%도 무효이면 75% 시점 재시도
  4. 모두 실패하면 `internal/thumb/placeholder.jpg` (빌드 시 embed) 반환
- [ ] ffmpeg 실패(파일 손상, 지원 코덱 없음 등) 시 placeholder 반환 (5xx 에러 아님)
- [ ] on-demand 생성: 캐시 파일이 없을 때만 ffmpeg 실행, 이후 캐시 서빙
- [ ] `browse` API: 동영상 파일도 `.thumb/{name}.jpg` 존재 여부로 `thumb_available` 계산

### 2.3.2 동영상 길이 (duration) 표시
- [ ] 동영상 썸네일 카드 우하단에 재생 시간 오버레이 표시 (반투명 검정 배경 + 흰 글씨)
- [ ] **포맷 (YouTube 스타일):** 1시간 미만 `M:SS` (예: `4:32`), 1시간 이상 `H:MM:SS` (예: `1:23:45`)
  - 초는 항상 0 패딩, 분은 시간이 있을 때만 0 패딩 (`4:05`, `1:02:09`)
- [ ] **저장 위치 (사이드카 파일):** `.thumb/{name}.jpg.dur` — duration(초, float)을 평문 텍스트로 저장 (예: `273.456`)
  - 썸네일 JPEG 생성과 동시에 ffprobe가 이미 구한 값을 기록 (추가 ffprobe 호출 없음)
- [ ] **기존 캐시 호환:** `.thumb/{name}.jpg`은 있지만 `.dur`는 없는 경우 → `browse` 응답 시 on-demand ffprobe 1회 실행하여 `.dur` 생성 후 캐시, 실패 시 null 반환 (썸네일은 그대로 서빙)
- [ ] **placeholder 사용 시:** duration을 구할 수 없으면 사이드카 파일 생성하지 않음 → API 응답에서 `duration_sec: null` → UI는 오버레이 숨김
- [ ] **browse API 확장:** 동영상 entry에 `duration_sec: float | null` 필드 추가 (다른 타입은 항상 null)
- [ ] **UI 렌더링 (`buildVideoGrid`):**
  - `duration_sec`이 null 또는 0 이하이면 오버레이 숨김
  - 포맷팅은 클라이언트(`app.js`)에서 수행
  - 폴더 삭제 시 `.thumb/` 전체 삭제로 사이드카도 함께 정리됨 (기존 동작 그대로)

### 2.4 음악 스트리밍
- **지원 포맷:** MP3, FLAC, AAC, OGG, WAV, M4A
- [ ] HTTP Range 요청 지원
- [ ] 원본 파일 스트리밍

### 2.5 프론트엔드 UI (Vanilla HTML/CSS/JS)
- [ ] 파일/폴더 브라우저 (리스트 뷰)
- [ ] 이미지 갤러리 (섬네일 그리드 → 클릭 시 원본 뷰어)
- [ ] 동영상 플레이어 (HTML5 `<video>` 태그)
- [ ] 음악 플레이어 (HTML5 `<audio>` 태그, 재생목록)
- [ ] 파일 업로드 UI (드래그 앤 드롭 + 버튼)
- [ ] 반응형 레이아웃 (모바일 브라우저 지원)
- [ ] 폴더 생성 모달 (이름 입력 → 현재 경로에 생성)
- [ ] 폴더 삭제 확인 모달 (재귀 삭제 경고 문구 포함)

---

## 3. Tech Stack

| Layer | Choice | Reason |
|-------|--------|--------|
| Backend | Go (net/http stdlib) | 성능, 단일 바이너리 |
| Image processing | `github.com/disintegration/imaging` | 순수 Go, CGo 불필요 |
| Transcoding | ffmpeg (alpine apk) | TS → MP4 실시간 트랜스코딩 |
| Frontend | Vanilla HTML + CSS + JS | 의존성 없음 |
| Container | Docker + Docker Compose | 배포 단순화 |
| Storage | Docker named volume → `/data` | 영속성 |
| Metadata DB | SQLite (`modernc.org/sqlite`) | 파일 메타데이터 캐싱 |

---

## 4. Project Structure

```
file_server/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── handler/
│   │   ├── files.go       # 파일 업로드/삭제/탐색
│   │   ├── stream.go      # 동영상/음악 Range 스트리밍
│   │   ├── image.go       # 이미지 서빙 + 섬네일
│   │   └── browse.go      # 디렉토리 탐색 API
│   ├── thumb/
│   │   └── thumb.go       # 섬네일 생성 로직
│   └── db/
│       └── db.go          # SQLite 메타데이터 (파일명, 경로, 타입, 섬네일 경로)
├── web/
│   ├── index.html
│   ├── style.css
│   └── app.js
├── Dockerfile
├── docker-compose.yml
└── SPEC.md
```

---

## 5. API Design

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/browse?path=` | 디렉토리 목록 조회 |
| GET | `/api/stream?path=` | 파일 스트리밍 (Range 지원) |
| GET | `/api/thumb?path=` | 섬네일 이미지 반환 |
| POST | `/api/upload?path=` | 파일 업로드 |
| DELETE | `/api/file?path=` | 파일 삭제 |
| POST | `/api/folder?path=` | 폴더 생성 |
| DELETE | `/api/folder?path=` | 폴더 재귀 삭제 (하위 내용 + `.thumb/` 포함) |
| GET | `/` | 프론트엔드 SPA |

### 5.1 응답 스키마

#### GET /api/browse
```json
{
  "path": "/movies",
  "entries": [
    {
      "name": "film.mp4",
      "path": "/movies/film.mp4",
      "type": "video",
      "size": 1234567,
      "mod_time": "2024-01-15T10:30:00Z",
      "is_dir": false,
      "thumb_available": false,
      "duration_sec": 273.456
    },
    {
      "name": "photo.jpg",
      "path": "/movies/photo.jpg",
      "type": "image",
      "size": 204800,
      "mod_time": "2024-01-14T08:00:00Z",
      "is_dir": false,
      "thumb_available": true
    },
    {
      "name": "subfolder",
      "path": "/movies/subfolder",
      "type": "dir",
      "size": 0,
      "mod_time": "2024-01-13T00:00:00Z",
      "is_dir": true,
      "thumb_available": false
    }
  ]
}
```
- `type`: `"image"` | `"video"` | `"audio"` | `"dir"` | `"other"`
- `thumb_available`: `.thumb/{name}.jpg` 파일 존재 여부
- `duration_sec`: 동영상 파일의 재생 시간(초, float). 동영상이 아니거나 ffprobe 실패 시 `null`
- 에러: `{"error": "message"}` + HTTP 상태 코드

#### POST /api/upload
```json
// 성공 201
{
  "path": "/movies/film.mp4",
  "name": "film.mp4",
  "size": 1234567,
  "type": "video"
}
// 에러 400/500
{"error": "message"}
```

#### DELETE /api/file
- 성공: `204 No Content` (body 없음)
- 미존재: `404 {"error": "not found"}`
- traversal: `400 {"error": "invalid path"}`

#### POST /api/folder
- Body: `{"name": "new-folder"}` (현재 `path` 파라미터 경로 아래에 생성)
- 성공: `201 Created`, `{"path": "/movies/new-folder"}`
- 이미 존재: `409 {"error": "already exists"}`
- 유효하지 않은 이름 (빈 문자열, `/` 포함, `.` or `..`): `400 {"error": "invalid name"}`
- traversal: `400 {"error": "invalid path"}`

#### DELETE /api/folder
- 재귀 삭제: 폴더 내 모든 파일·하위폴더·`.thumb/` 디렉토리 포함
- 성공: `204 No Content`
- 미존재: `404 {"error": "not found"}`
- path가 파일을 가리킴: `400 {"error": "not a directory"}`
- traversal: `400 {"error": "invalid path"}`

#### GET /api/stream
- 성공: `200 OK` 또는 `206 Partial Content` (Range 요청 시)
- `Content-Type`: 확장자 기반 MIME 타입
- `Accept-Ranges: bytes` 헤더 항상 포함
- `.ts` 파일: ffmpeg 파이프로 실시간 트랜스코딩, `Content-Type: video/mp4` 반환 (Range 미지원)
- 미존재: `404`

#### GET /api/thumb
- 성공: `200 OK`, `Content-Type: image/jpeg`
- 이미지 파일: `imaging` 라이브러리로 섬네일 생성 (기존)
- 동영상 파일 (MP4, MKV, AVI, TS): ffmpeg로 프레임 추출
  - 프레임 추출 순서: 50% → (전체 흑/백이면) 25% → 75% → placeholder
  - ffmpeg 실패 시 placeholder 반환 (`200 OK`, placeholder JPEG)
- 이미지/동영상 외 파일: `400 {"error": "unsupported file type"}`
- 파일 미존재: `404`
- **Placeholder:** `internal/thumb/placeholder.jpg` (빌드 바이너리에 embed)

### 5.2 MIME 타입 맵

| 확장자 | MIME 타입 |
|--------|-----------|
| `.mp4` | `video/mp4` |
| `.mkv` | `video/x-matroska` |
| `.avi` | `video/x-msvideo` |
| `.ts` | `video/mp2t` |
| `.mp3` | `audio/mpeg` |
| `.flac` | `audio/flac` |
| `.aac` | `audio/aac` |
| `.ogg` | `audio/ogg` |
| `.wav` | `audio/wav` |
| `.m4a` | `audio/mp4` |
| `.jpg`, `.jpeg` | `image/jpeg` |
| `.png` | `image/png` |
| `.webp` | `image/webp` |
| `.gif` | `image/gif` |

---

## 5.3 SQLite 스키마

```sql
-- 파일 메타데이터 캐시
CREATE TABLE IF NOT EXISTS files (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    path        TEXT    NOT NULL UNIQUE,  -- /data 기준 상대 경로
    name        TEXT    NOT NULL,
    type        TEXT    NOT NULL,         -- image|video|audio|other
    size        INTEGER NOT NULL,
    mod_time    DATETIME NOT NULL,
    thumb_path  TEXT,                     -- NULL이면 섬네일 없음
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_files_path ON files(path);
CREATE INDEX IF NOT EXISTS idx_files_type ON files(type);
```

> **Note:** 초기 구현에서는 SQLite를 사용하지 않고 파일시스템을 직접 읽는다.
> 파일 수가 많아져 성능 문제가 생길 때 도입 예정.

---

## 6. Docker Setup

```yaml
# docker-compose.yml 개요
services:
  server:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - media:/data
volumes:
  media:
```

- 미디어 파일: `/data/media/`
- SQLite DB: `/data/db.sqlite`
- 섬네일: `/data/media/**/.thumb/`

---

## 7. Code Style

- `gofmt` + `golangci-lint` 준수
- 패키지별 단일 책임 원칙
- 에러는 반환하여 핸들러에서 HTTP 상태 코드로 변환
- 주석은 WHY가 비자명한 경우에만 작성

---

## 8. Testing Strategy

- 단위 테스트: 섬네일 생성, MIME 타입 감지, Range 파싱
- 단위 테스트 (동영상 섬네일): `thumb.IsBlankFrame` 함수 — 전체 흑/백 판정 로직
- 단위 테스트 (duration): 사이드카 read/write round-trip, `formatDuration` JS 함수 (`4:32`, `1:02:09`, 0/null 케이스)
- 통합 테스트: HTTP 핸들러 (`net/http/httptest` 사용)
- 통합 테스트 (동영상 섬네일): ffmpeg 없는 환경에서 placeholder 반환 확인
- 통합 테스트 (duration): browse 응답에 동영상 entry의 `duration_sec` 포함 확인 (사이드카 있을 때 / 없을 때)
- 수동 테스트: 브라우저에서 업로드→섬네일→스트리밍 전체 흐름 확인 (썸네일 우하단 시간 오버레이 확인)

---

## 9. Boundaries

**항상 할 것 (Always)**
- Range 요청 지원 (스트리밍 seek 필수)
- 업로드 파일은 `/data` 볼륨 내부에만 저장 (path traversal 방지)
- 섬네일은 비동기로 생성 (업로드 응답 차단 안 함)

**하지 않을 것 (Never)**
- TS 이외 포맷 트랜스코딩 (MP4/MKV/AVI는 원본 그대로 스트리밍)
- 사용자 인증/권한 관리
- 외부 CDN이나 클라우드 스토리지 연동
