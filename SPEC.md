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
- [ ] 파일/폴더 이름 변경 (파일은 확장자 고정; 이미지/동영상은 썸네일·duration 사이드카 함께 rename)
- [ ] 폴더 생성 (현재 탐색 경로 기준, 이름 입력 모달)
- [ ] 폴더 삭제 (재귀 삭제 — 하위 파일/폴더 + `.thumb/` 디렉토리 포함)

### 2.1.1 이름 변경 (Rename) 상세
- **대상:** 파일 및 폴더 (데이터 루트 디렉토리 자체는 제외)
- **파일 이름 규칙:**
  - 확장자는 변경 불가 — 원본 확장자 유지 (MIME/타입 일관성 보장)
  - 사용자 입력에 확장자가 포함되어 있어도 서버는 base name만 사용하고 원본 확장자를 재부착
  - UI 모달은 확장자를 제외한 base name만 input에 표시·편집
  - **Dotfile carveout:** 원본이 `.gitignore`처럼 선행 점이 있고 다른 점이 없는 이름이면 확장자가 없는 것으로 취급 (rename 시 원하지 않는 suffix 부착 방지). 서버·JS 클라이언트 일관.
  - **Case-only rename:** 대소문자만 다른 rename(`a.txt` → `A.txt`)은 대소문자 무시 파일시스템에서도 동작 (기존 파일 존재 검사 skip + OS가 atomic하게 처리)
- **폴더 이름 규칙:** 확장자 개념 없음. `validateName`과 동일 (빈 문자열/`.`/`..`/`/`/`\\` 불가, 최대 255자; 파일은 `base + origExt`가 255자 초과 시에도 400)
- **scope:** 동일 부모 디렉토리 내에서만 rename. 경로 이동·디렉토리 간 이동은 별도 기능 (out of scope).
- **충돌 처리:** 같은 이름이 이미 존재하면 `409 Conflict` 반환. 자동 `_1` suffix 없음 (rename은 사용자의 명시적 의도).
- **동일 이름 입력:** 새 이름이 기존 이름과 동일(확장자 포함 비교)하면 `400 {"error": "name unchanged"}` 반환.
- **사이드카 파일 동기화 (이미지/동영상 파일 rename 시):**
  - `.thumb/{oldname}.jpg` → `.thumb/{newname}.jpg`
  - `.thumb/{oldname}.jpg.dur` → `.thumb/{newname}.jpg.dur` (동영상만)
  - 사이드카가 없으면 skip (오류 아님)
  - 사이드카 rename 실패는 로그만 남기고 200 반환 — 썸네일은 다음 조회 시 on-demand 재생성됨 (기존 lazy 메커니즘)
- **폴더 rename:** 폴더 내부의 `.thumb/` 디렉토리는 부모 폴더 rename과 함께 자동으로 따라감 (OS `rename` 한 번). 추가 처리 불필요.
- **UI 트리거:** 각 entry 카드에 rename 버튼 (연필 아이콘), 기존 delete 버튼과 동일한 레이아웃에 추가
- **UI 피드백:** 성공 시 `loadBrowse()`로 현재 경로 재조회. 409/400 에러는 모달 내부 메시지로 표시하고 모달 유지.

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
| PATCH | `/api/file?path=` | 파일 이름 변경 (확장자 고정) |
| POST | `/api/folder?path=` | 폴더 생성 |
| PATCH | `/api/folder?path=` | 폴더 이름 변경 |
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
      "mime": "video/mp4",
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
      "mime": "image/jpeg",
      "size": 204800,
      "mod_time": "2024-01-14T08:00:00Z",
      "is_dir": false,
      "thumb_available": true,
      "duration_sec": null
    },
    {
      "name": "subfolder",
      "path": "/movies/subfolder",
      "type": "dir",
      "mime": "",
      "size": 0,
      "mod_time": "2024-01-13T00:00:00Z",
      "is_dir": true,
      "thumb_available": false,
      "duration_sec": null
    }
  ]
}
```
- `type`: `"image"` | `"video"` | `"audio"` | `"dir"` | `"other"`
- `mime`: 확장자 기반 MIME 타입 (디렉토리/미지원 타입은 `""`)
- `thumb_available`: `.thumb/{name}.jpg` 파일 존재 여부
- `duration_sec`: 동영상 파일의 재생 시간(초, float). 동영상이 아니거나 ffprobe 실패/사용 불가 시 `null` (썸네일은 정상 서빙)
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

#### PATCH /api/file
- Body: `{"name": "new-base-name"}` (확장자 제외한 base name; 서버가 원본 확장자 재부착)
  - 사용자 입력에 확장자가 포함되어 있으면 서버가 strip 후 원본 확장자 사용
- 성공: `200 OK`
  ```json
  {
    "path": "/movies/new-base-name.mp4",
    "name": "new-base-name.mp4"
  }
  ```
- 미존재: `404 {"error": "not found"}`
- path가 디렉토리를 가리킴: `400 {"error": "not a file"}`
- 유효하지 않은 이름 (빈 문자열, `/`·`\\` 포함, `.` or `..`, 길이 초과): `400 {"error": "invalid name"}`
- 새 이름 = 기존 이름: `400 {"error": "name unchanged"}`
- 충돌 (동일 디렉토리 내 동일 이름 존재): `409 {"error": "already exists"}`
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

#### PATCH /api/folder
- Body: `{"name": "new-folder-name"}`
- 성공: `200 OK`
  ```json
  {
    "path": "/movies/new-folder-name",
    "name": "new-folder-name"
  }
  ```
- 미존재: `404 {"error": "not found"}`
- path가 파일을 가리킴: `400 {"error": "not a directory"}`
- 루트 rename 시도 (path가 빈 문자열 또는 `/`): `400 {"error": "cannot rename root"}`
- 유효하지 않은 이름: `400 {"error": "invalid name"}`
- 새 이름 = 기존 이름: `400 {"error": "name unchanged"}`
- 충돌: `409 {"error": "already exists"}`
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
- 단위 테스트 (rename): 이름 검증 (확장자 strip 로직, 빈 문자열, `/`·`\\`, `.`/`..`, 길이 초과), 확장자 재부착 로직
- 통합 테스트: HTTP 핸들러 (`net/http/httptest` 사용)
- 통합 테스트 (동영상 섬네일): ffmpeg 없는 환경에서 placeholder 반환 확인
- 통합 테스트 (duration): browse 응답에 동영상 entry의 `duration_sec` 포함 확인 (사이드카 있을 때 / 없을 때)
- 통합 테스트 (rename):
  - `PATCH /api/file` 성공 시 파일 + `.thumb/{name}.jpg` + `.thumb/{name}.jpg.dur` 모두 신규 이름으로 이동 확인
  - 사이드카가 일부만 있을 때(`.jpg`만 있고 `.dur` 없음) 에러 없이 200 반환
  - 확장자 포함 입력(`new.mp4`)이 원본 확장자(`.mkv`)를 덮어쓰지 않음 확인
  - 409 Conflict: 동일 디렉토리 내 기존 파일명으로 rename 시도
  - 400 name unchanged: 새 이름이 기존 이름과 동일할 때
  - `PATCH /api/folder` 성공 시 하위 내용(`.thumb/` 포함)이 새 경로에 그대로 존재 확인
  - Path traversal 방지 (`name`에 `/`·`\\` 포함 시 400)
- 수동 테스트: 브라우저에서 업로드→섬네일→스트리밍 전체 흐름 확인 (썸네일 우하단 시간 오버레이 확인). Rename 후 썸네일·duration 오버레이가 유지되는지 확인.

---

## 9. Boundaries

**항상 할 것 (Always)**
- Range 요청 지원 (스트리밍 seek 필수)
- 업로드 파일은 `/data` 볼륨 내부에만 저장 (path traversal 방지)
- 섬네일은 비동기로 생성 (업로드 응답 차단 안 함)
- Rename 시 `media.SafePath`로 원본·대상 경로 모두 검증 (path traversal 방지)
- Rename은 동일 부모 디렉토리 내에서만 허용 (경로 이동 금지)
- File rename은 `os.Link` + `os.Remove`로 atomic EEXIST 보장 (TOCTOU 방지)

**하지 않을 것 (Never)**
- TS 이외 포맷 트랜스코딩 (MP4/MKV/AVI는 원본 그대로 스트리밍)
- 사용자 인증/권한 관리
- 외부 CDN이나 클라우드 스토리지 연동
- Rename 시 확장자 변경 허용 (MIME/타입 감지 일관성 유지)
- Rename 시 자동 suffix 부여 (`_1`, `_2` 등) — 충돌은 항상 409로 거부

**Known limitations**
- Folder rename은 `os.Stat` + `os.Rename` 순서로, 두 콜 사이에 동일 이름 폴더가 생성되면 race 발생 가능. 단일 사용자 배포 대상이므로 acceptable.
