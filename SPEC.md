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
- [ ] **URL에서 가져오기 모달**: 업로드 버튼 옆 버튼 → textarea(줄바꿈 구분 URL) → "가져오기" → 완료 시 결과 토스트 (성공 N개 / 실패 M개 + 실패 URL 목록)

### 2.6 URL 기반 이미지 가져오기 (URL Import)

이미지 URL을 입력하면 서버가 다운로드와 동시에 디스크에 스트리밍 저장한다. 이번 단계는 **이미지만** 지원하며, 추후 동영상/음악으로 확장 고려.

- [ ] 여러 URL 한 번에 입력 가능 (줄바꿈 구분, 빈 줄/공백 무시)
- [ ] 동기 batch 처리: 서버가 모든 URL 처리 완료 후 한 번에 응답 (succeeded/failed 분리)
- [ ] 부분 실패 허용: 성공한 URL만 저장, 실패 URL은 응답의 `failed` 배열에 사유와 함께 반환 (전체 롤백 X)
- [ ] **저장 위치**: 클라이언트가 보낸 `path` 쿼리 파라미터 경로(현재 browse 경로)
- [ ] **파일명 결정**:
  1. URL 마지막 경로 세그먼트 추출 (예: `https://x.com/a/foo.jpg` → `foo.jpg`)
  2. URL에 확장자 없거나 비표준이면 응답 `Content-Type` 헤더에서 결정 (`image/jpeg` → `.jpg`, `image/png` → `.png`, `image/webp` → `.webp`, `image/gif` → `.gif`)
  3. URL 확장자와 `Content-Type`이 충돌하면 **`Content-Type` 우선** (확장자를 응답 기준으로 교체)
  4. 파일명 sanitize: `/`, `\`, `..`, 컨트롤 문자 제거. 빈 이름 → `image` + 확장자
  5. **충돌 시 자동 리네임**: `foo.jpg` 존재 → `foo_1.jpg`, `foo_2.jpg` ... (기존 `handleUpload`의 `createUniqueFile` 패턴 재사용; race로 재시도 시 매번 다시 검사). 충돌 발생 시 응답 `warnings`에 `"renamed"` 추가
- [ ] **다운로드 흐름**:
  1. URL 스킴 검증: `http`/`https`만 허용 (`file:`, `data:`, `javascript:` 등 거부)
  2. HTTP는 허용하되 응답 `warnings`에 `"insecure_http"` 추가 (다운로드는 진행)
  3. HTTPS는 표준 TLS 인증서 검증 (자체서명 거부)
  4. 리다이렉트 최대 5회 추적 (스킴은 매 hop마다 재검증)
  5. 요청 시 `Authorization` 등 인증 헤더 자동 첨부 안 함
  6. 응답 헤더 검증:
     - `Content-Type`이 `image/*` 아니면 거부 (`error: "unsupported_content_type"`)
     - `Content-Length` 헤더 **없으면 거부** (`error: "missing_content_length"`)
     - `Content-Length` > 50MB이면 다운로드 시작 전 거부 (`error: "too_large"`)
  7. 임시 파일에 스트리밍 저장 (다운로드와 디스크 쓰기 동시 = "동시에"의 의미)
  8. 다운로드 중 누적 바이트가 50MB 초과하면 즉시 중단 + 임시 파일 삭제 (`error: "too_large"`)
  9. 검증 통과 시 임시 파일 → 최종 경로로 atomic rename
  10. 기존 업로드 흐름과 동일하게 `.thumb/{name}.jpg` 섬네일 비동기 생성
- [ ] **타임아웃**: 연결 10초 + 전체 다운로드 60초 (개별 URL 단위)
- [ ] **SSRF 정책**: 약하게 — 사설 IP(127.0.0.1, 10.x, 172.16-31.x, 192.168.x, 169.254.x, ::1, fc00::/7, fe80::/10) 차단 안 함 (LAN 미디어 서버 자기 호출 등 정상 케이스 허용)
- [ ] **인증/쿠키**: 자동 첨부 절대 안 함 (인증 필요한 URL은 실패 처리)

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
| POST | `/api/import-url?path=` | URL 목록에서 이미지 다운로드 → 저장 (batch) |
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

#### POST /api/import-url
- 쿼리: `path` (저장할 디렉토리, `/data` 기준 상대 경로)
- Body:
```json
{
  "urls": [
    "https://example.com/cat.jpg",
    "https://example.com/dog.png"
  ]
}
```
- 응답 `200 OK` (부분 실패도 200, 개별 결과는 배열로 분리):
```json
{
  "succeeded": [
    {
      "url": "https://example.com/cat.jpg",
      "path": "/photos/cat.jpg",
      "name": "cat.jpg",
      "size": 204800,
      "type": "image",
      "warnings": []
    }
  ],
  "failed": [
    {
      "url": "http://example.com/dog.png",
      "error": "missing_content_length"
    }
  ]
}
```
- `warnings` 가능 값:
  - `"insecure_http"` — HTTP(비암호화) URL 사용
  - `"renamed"` — 파일명 충돌로 자동 리네임 발생 (최종명은 `name`/`path`에 반영)
  - `"extension_replaced"` — URL 확장자와 `Content-Type` 불일치로 확장자 교체
- `error` 가능 값:
  - `"invalid_scheme"` — `http`/`https` 외 스킴
  - `"invalid_url"` — URL 파싱 실패
  - `"connect_timeout"` — 연결 10초 초과
  - `"download_timeout"` — 전체 60초 초과
  - `"too_many_redirects"` — 5회 초과
  - `"tls_error"` — TLS 인증서 검증 실패
  - `"http_error"` — 4xx/5xx 응답
  - `"unsupported_content_type"` — `image/*` 아님
  - `"missing_content_length"` — `Content-Length` 헤더 없음
  - `"too_large"` — 50MB 초과
  - `"network_error"` — 기타 네트워크 실패
  - `"write_error"` — 디스크 저장 실패
- 4xx 케이스 (요청 자체 거부):
  - `400 {"error": "invalid path"}` — path traversal
  - `400 {"error": "no urls"}` — 빈 배열
  - `400 {"error": "too many urls"}` — 한 번에 50개 초과
  - `404 {"error": "path not found"}` — 저장 디렉토리 미존재

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
- 단위 테스트 (URL import):
  - 파일명 추출/sanitize (확장자 있음/없음, `..`/컨트롤 문자, 빈 이름)
  - 확장자 결정 (URL 우선 → Content-Type 폴백 → 충돌 시 Content-Type 우선)
  - 충돌 자동 리네임 (`foo.jpg` → `foo (1).jpg`, race 시 재시도)
  - URL 스킴 검증 (`http`/`https`만 통과)
- 통합 테스트: HTTP 핸들러 (`net/http/httptest` 사용)
- 통합 테스트 (동영상 섬네일): ffmpeg 없는 환경에서 placeholder 반환 확인
- 통합 테스트 (duration): browse 응답에 동영상 entry의 `duration_sec` 포함 확인 (사이드카 있을 때 / 없을 때)
- 통합 테스트 (URL import): `httptest.Server`로 모의 origin 띄워서 검증
  - 정상 이미지 다운로드 → 파일 저장 + 응답에 `succeeded` 포함
  - `Content-Length` 누락 → `missing_content_length` 실패
  - 51MB 응답 → `too_large` 실패 + 임시 파일 정리 확인
  - `Content-Type: text/html` → `unsupported_content_type` 실패
  - HTTP URL → 성공 + `warnings: ["insecure_http"]`
  - URL 확장자(`.jpg`)와 Content-Type(`image/png`) 불일치 → `.png`로 저장 + `warnings: ["extension_replaced"]`
  - 부분 실패: 3개 URL 중 일부 실패 → `succeeded` + `failed` 양쪽 채워짐
  - 리다이렉트 6회 → `too_many_redirects`
- 수동 테스트: 브라우저에서 업로드→섬네일→스트리밍 전체 흐름 확인 (썸네일 우하단 시간 오버레이 확인)
- 수동 테스트 (URL import): 모달에서 URL 여러 개 입력 → 결과 토스트 확인 (성공/실패 카운트)

---

## 9. Boundaries

**항상 할 것 (Always)**
- Range 요청 지원 (스트리밍 seek 필수)
- 업로드 파일은 `/data` 볼륨 내부에만 저장 (path traversal 방지)
- 섬네일은 비동기로 생성 (업로드 응답 차단 안 함)
- URL import: HTTPS TLS 인증서 검증, `Content-Length` 사전 검증, 다운로드 누적 50MB 캡, `Content-Type: image/*` 검증, 임시 파일 → atomic rename, 파일명 sanitize

**하지 않을 것 (Never)**
- TS 이외 포맷 트랜스코딩 (MP4/MKV/AVI는 원본 그대로 스트리밍)
- 사용자 인증/권한 관리
- 외부 CDN이나 클라우드 스토리지 연동
- URL import: `Authorization`/쿠키 등 인증 헤더 자동 첨부, `http`/`https` 외 스킴 허용, 50MB 초과 다운로드, 비이미지 응답 저장, `Content-Length` 없는 응답 저장
