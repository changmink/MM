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
- [ ] **URL에서 가져오기 모달**: 업로드 버튼 옆 버튼 → textarea(줄바꿈 구분 URL) → "가져오기" → 각 URL별 실시간 프로그래스 바 표시 (다운로드 중 % / 완료 / 실패 상태) → 전체 완료 시 성공·실패 카운트 요약

### 2.6 URL 기반 미디어 가져오기 (URL Import)

미디어 URL을 입력하면 서버가 다운로드와 동시에 디스크에 스트리밍 저장한다. 이미지·동영상·음악 모두 지원.

- [ ] 여러 URL 한 번에 입력 가능 (줄바꿈 구분, 빈 줄/공백 무시)
- [ ] **실시간 progress 스트리밍**: 서버가 `text/event-stream` (SSE) 응답으로 URL별 진행 이벤트를 순차 전송 → 클라이언트가 URL별 프로그래스 바 업데이트
- [ ] 순차 batch 처리: 서버는 URL을 순서대로 하나씩 다운로드 (동시성 1), 각 단계를 SSE 이벤트로 내보냄
- [ ] 부분 실패 허용: 성공한 URL만 저장, 실패 URL은 해당 `error` 이벤트로 사유 전달 (전체 롤백 X)
- [ ] **저장 위치**: 클라이언트가 보낸 `path` 쿼리 파라미터 경로(현재 browse 경로)
- [ ] **지원 Content-Type** (응답 헤더 기준):
  - **Image**: `image/jpeg`, `image/png`, `image/webp`, `image/gif`
  - **Video**: `video/mp4`, `video/x-matroska`, `video/x-msvideo`, `video/mp2t`
  - **Audio**: `audio/mpeg`, `audio/flac`, `audio/aac`, `audio/ogg`, `audio/wav`, `audio/mp4`
  - **HLS 플레이리스트** (ffmpeg 리먹싱 후 `.mp4`로 저장): `application/vnd.apple.mpegurl`, `application/x-mpegurl`, 레거시 `audio/mpegurl`, `audio/x-mpegurl` — 상세는 §2.6.1
- [ ] **파일명 결정**:
  1. URL 마지막 경로 세그먼트 추출 (예: `https://x.com/a/foo.mp4` → `foo.mp4`)
  2. URL에 확장자 없거나 비표준이면 응답 `Content-Type` 헤더에서 결정 (`image/jpeg` → `.jpg`, `video/mp4` → `.mp4`, `audio/mpeg` → `.mp3`, `video/x-matroska` → `.mkv`, `audio/mp4` → `.m4a`, …)
  3. URL 확장자와 `Content-Type`이 충돌하면 **`Content-Type` 우선** (확장자를 응답 기준으로 교체)
  4. 파일명 sanitize: `/`, `\`, `..`, 컨트롤 문자 제거. 빈 이름 → 타입별 기본값 (`image`/`video`/`audio`) + 확장자
  5. **충돌 시 자동 리네임**: `foo.mp4` 존재 → `foo_1.mp4`, `foo_2.mp4` ... (기존 `createUniqueFile` 패턴 재사용). 충돌 시 `warnings`에 `"renamed"` 추가
- [ ] **다운로드 흐름**:
  1. URL 스킴 검증: `http`/`https`만 허용 (`file:`, `data:`, `javascript:` 등 거부)
  2. HTTP는 허용하되 `warnings`에 `"insecure_http"` 추가 (다운로드는 진행)
  3. HTTPS는 표준 TLS 인증서 검증 (자체서명 거부)
  4. 리다이렉트 최대 5회 추적 (스킴은 매 hop마다 재검증)
  5. 요청 시 `Authorization` 등 인증 헤더 자동 첨부 안 함
  6. 응답 헤더 검증:
     - `Content-Type`이 위 허용 목록에 없으면 거부 (`error: "unsupported_content_type"`)
     - **HLS 분기** (§2.6.1): Content-Type이 HLS 플레이리스트이거나, URL 경로가 `.m3u8`로 끝나고 Content-Type이 `text/plain` / `application/octet-stream` / 파싱 불가 → HLS 흐름으로 이탈하고 이하 §2.6 검증은 건너뜀
     - `Content-Length` 헤더 **없으면 거부** (`error: "missing_content_length"`) — HLS 제외
     - `Content-Length` > **2 GiB** (2 × 1024³ = 2147483648 B)이면 다운로드 시작 전 거부 (`error: "too_large"`)
  7. 임시 파일에 스트리밍 저장
  8. 다운로드 중 누적 바이트가 2 GiB 초과하면 즉시 중단 + 임시 파일 삭제 (`error: "too_large"`)
  9. 검증 통과 시 임시 파일 → 최종 경로로 atomic rename
  10. 이미지·동영상 성공 시 `.thumb/{name}.jpg` 섬네일 비동기 생성 (음악은 생략)
- [ ] **진행 이벤트 (SSE)**: URL당 최소 `start` → `done` 또는 `start` → `error`. 큰 파일은 중간에 `progress` 이벤트를 주기적으로 방출 (§5.1.1 참고)
- [ ] **타임아웃**: 연결 10초 + 전체 다운로드 **10분** (개별 URL 단위). 초과 시 `error: "download_timeout"`
- [ ] **SSRF 정책**: 약하게 — 사설 IP(127.0.0.1, 10.x, 172.16-31.x, 192.168.x, 169.254.x, ::1, fc00::/7, fe80::/10) 차단 안 함 (LAN 미디어 서버 자기 호출 등 정상 케이스 허용)
- [ ] **인증/쿠키**: 자동 첨부 절대 안 함 (인증 필요한 URL은 실패 처리)

### 2.6.1 HLS 스트림 다운로드

HLS(`.m3u8`) 플레이리스트는 여러 개의 `.ts`/`.m4s` 세그먼트를 참조하는 색인 파일이라, 개별 세그먼트에는 Content-Length가 있어도 **스트림 전체 크기를 미리 알 수 없다.** 일반 다운로드 경로(`Content-Length` 사전 검증) 대신 ffmpeg 리먹싱 경로를 거쳐 단일 MP4 파일로 저장한다.

- [ ] **감지 조건** (둘 중 하나 만족 시 HLS 분기):
  1. 응답 `Content-Type` (media type만, 파라미터 무시, 대소문자 무시)이 `application/vnd.apple.mpegurl`, `application/x-mpegurl`, `audio/mpegurl`, 또는 `audio/x-mpegurl`
  2. URL 경로가 `.m3u8`(대소문자 무시)로 끝나고 `Content-Type`이 `text/plain`, `application/octet-stream`, 빈 값, 또는 파싱 실패 (CDN 오인식 폴백)
- [ ] **마스터 플레이리스트 처리:**
  - 초기 HTTP 응답 본문을 **최대 1 MiB**까지 읽어 플레이리스트 파싱 (초과 시 `error: "hls_playlist_too_large"`)
  - `#EXT-X-STREAM-INF:`가 하나 이상 있으면 master playlist로 간주
  - 각 variant의 `BANDWIDTH` 속성 비교 → **최고값** variant 선택 (동률은 먼저 선언된 것)
  - `BANDWIDTH` 누락 variant는 0으로 간주 (후순위)
  - variant URL이 상대 경로면 master URL 기준 resolve
  - `#EXT-X-STREAM-INF`가 없으면 이미 media playlist — 원본 URL을 그대로 사용
- [ ] **다운로드 흐름:**
  1. HLS 감지 → 초기 응답 본문을 메모리로 읽고 즉시 연결 close
  2. ffmpeg 프로세스 spawn: `ffmpeg -hide_banner -loglevel error -protocol_whitelist "http,https,tls,tcp,crypto" -i <variant_url> -c copy -bsf:a aac_adtstoasc -f mp4 -movflags +faststart <tmpPath>`
     - 임시 파일 경로는 기존 `.urlimport-*.tmp` 패턴 재사용
     - stderr는 버퍼링하여 실패 시 로그로 남김 (응답 본문으로는 노출 안 함)
  3. 별도 goroutine에서 500 ms 주기로 임시 파일 `os.Stat` → 현재 크기 계산 → `progress` 이벤트 방출 (기존 1 MiB / 250 ms throttling 규칙 그대로 적용)
  4. 임시 파일 크기가 2 GiB 초과 시 ffmpeg 프로세스 kill + 임시 파일 삭제 → `error: "too_large"`
  5. `TotalTimeout`(10분) 또는 요청 context 취소 시 ffmpeg 프로세스 kill → `error: "download_timeout"` (또는 context에 따라 `network_error`)
  6. ffmpeg exit code 0 → 임시 파일 → 최종 경로 atomic rename (기존 `renameUnique` 재사용)
  7. ffmpeg exit code non-zero → `error: "ffmpeg_error"` + 임시 파일 삭제
- [ ] **파일명 결정:**
  - URL 마지막 경로 세그먼트가 `foo.m3u8` → base name `foo` + 강제 확장자 `.mp4`
  - base name 추출 불가(빈 이름, `.`, `..`) → 기본값 `video.mp4`
  - 확장자 교체가 발생하므로 항상 `warnings: ["extension_replaced"]` 추가
  - 충돌 시 기존 `_1`, `_2` 자동 리네임 로직 그대로 적용
- [ ] **타입 및 후속 처리:**
  - `type: "video"` (항상)
  - 성공 시 일반 동영상과 동일: `.thumb/{name}.jpg` 썸네일 풀 제출 + duration 사이드카 생성
- [ ] **progress 이벤트 차이점:**
  - `start` 이벤트: `total` 필드 **생략** (미상) — 기존 "알 수 없으면 생략" 규칙 준수
  - `progress.received`: 출력 MP4 임시 파일의 현재 바이트 수 (수신 바이트 아님) — ffmpeg는 버퍼/remux 과정에서 수신 총량 ≠ 출력 총량
  - `done.size`: 최종 MP4 파일 크기 (atomic rename 직후 `Stat`)
- [ ] **보안:**
  - ffmpeg `-protocol_whitelist "http,https,tls,tcp,crypto"` 로 제한 — m3u8 내부 세그먼트 URL이 `file:`, `rtp:`, `udp:` 등으로 LFI/포트스캔을 시도할 수 없게 차단
  - variant/세그먼트 URL의 스킴은 ffmpeg가 화이트리스트로 강제하므로 Go 측 추가 검증 불필요 (단, master playlist 파싱 시 선택된 variant URL의 스킴이 `http`/`https`가 아니면 파싱 단계에서 거부 — 이중 방어)
  - ffmpeg 호출 시 URL은 argv로 전달 (쉘 미개입) — shell injection 불가
- [ ] **Live stream 처리:** 명시적 거부·감지 없음. 엔드리스 스트림은 `TotalTimeout`(10분) 또는 2 GiB 상한에서 자연 중단되고 `download_timeout`/`too_large`로 실패 처리. 부분적으로 기록된 임시 파일은 폐기.
- [ ] **DRM/Fairplay/암호화 세그먼트:** 지원 안 함 — ffmpeg가 실패하면 그대로 `ffmpeg_error` 반환

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
| POST | `/api/import-url?path=` | URL 목록에서 미디어 다운로드 → 저장 (SSE 진행 스트림) |
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

#### POST /api/import-url
- 쿼리: `path` (저장할 디렉토리, `/data` 기준 상대 경로)
- Body:
```json
{
  "urls": [
    "https://example.com/cat.jpg",
    "https://example.com/clip.mp4",
    "https://example.com/song.mp3"
  ]
}
```
- **응답**: `200 OK`, `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`
- SSE 프레임: 각 이벤트는 `data: {JSON}\n\n` 형식. 이벤트 이름은 생략(기본 `message`), JSON의 `phase` 필드로 구분
- **이벤트 흐름** (URL당):
  1. `start` — 다운로드 시작 (응답 헤더 검증 통과 직후)
  2. `progress` — 다운로드 진행 (0개 이상, throttled, §5.1.1)
  3. `done` 또는 `error` — 종료 (URL당 정확히 1개)
- 전체 끝에 `summary` 이벤트 1개

**이벤트 스키마**

```jsonc
// phase: "start"
{"phase":"start","index":0,"url":"https://example.com/clip.mp4",
 "name":"clip.mp4","total":524288000,"type":"video"}

// phase: "progress"
{"phase":"progress","index":0,"received":67108864}

// phase: "done"
{"phase":"done","index":0,"url":"https://example.com/clip.mp4",
 "path":"/movies/clip.mp4","name":"clip.mp4","size":524288000,
 "type":"video","warnings":[]}

// phase: "error"
{"phase":"error","index":1,"url":"http://example.com/x.html",
 "error":"unsupported_content_type"}

// phase: "summary"
{"phase":"summary","succeeded":2,"failed":1}
```

- `index`는 요청 `urls` 배열의 0-based 인덱스
- `total`은 `Content-Length` 값(바이트). 알 수 없으면 생략
- `type` ∈ `"image" | "video" | "audio"`
- `warnings` 가능 값:
  - `"insecure_http"` — HTTP(비암호화) URL 사용
  - `"renamed"` — 파일명 충돌로 자동 리네임 (최종명은 `name`/`path` 반영)
  - `"extension_replaced"` — URL 확장자와 `Content-Type` 불일치로 확장자 교체 (HLS는 항상 `.m3u8` → `.mp4` 교체이므로 함께 부착)
- `error` 가능 값:
  - `"invalid_scheme"` — `http`/`https` 외 스킴
  - `"invalid_url"` — URL 파싱 실패
  - `"connect_timeout"` — 연결 10초 초과
  - `"download_timeout"` — 전체 10분 초과
  - `"too_many_redirects"` — 5회 초과
  - `"tls_error"` — TLS 인증서 검증 실패
  - `"http_error"` — 4xx/5xx 응답
  - `"unsupported_content_type"` — 허용 Content-Type 목록 밖
  - `"missing_content_length"` — `Content-Length` 헤더 없음 (HLS 예외 — §2.6.1)
  - `"too_large"` — 2 GiB 초과
  - `"hls_playlist_too_large"` — HLS 플레이리스트 본문이 1 MiB 초과 (§2.6.1)
  - `"ffmpeg_error"` — HLS 리먹싱 중 ffmpeg 프로세스 실패 (non-zero exit, 입력 스트림 문제)
  - `"ffmpeg_missing"` — ffmpeg 바이너리가 서버 PATH에 없음 (운영자 설치 필요) — `ffmpeg_error`와 구분됨
  - `"network_error"` — 기타 네트워크 실패
  - `"write_error"` — 디스크 저장 실패
- 4xx 케이스 (요청 자체 거부 — SSE 스트림 시작 전 일반 JSON 에러 응답):
  - `400 {"error": "invalid path"}` — path traversal
  - `400 {"error": "no urls"}` — 빈 배열
  - `400 {"error": "too many urls"}` — 한 번에 50개 초과
  - `404 {"error": "path not found"}` — 저장 디렉토리 미존재

##### 5.1.1 Progress 이벤트 throttling
- `progress` 이벤트는 **수신 바이트 1 MiB마다** 또는 **250 ms마다** 중 먼저 도달하는 시점에 방출 (양쪽 모두 ticker/카운터 기반)
- 동일 값으로 `received`가 변하지 않으면 방출 생략 (중복 제거)
- 파일이 작아 `progress` 없이 `start` → `done` 바로 가는 케이스 허용
- 최종 바이트 수는 항상 `done` 이벤트 `size` 필드로 전달 (`progress`의 마지막 값은 신뢰하지 말 것)

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
- 단위 테스트 (URL import):
  - 파일명 추출/sanitize (확장자 있음/없음, `..`/컨트롤 문자, 빈 이름 — image/video/audio 기본값 각각)
  - 확장자 결정 (URL 우선 → Content-Type 폴백 → 충돌 시 Content-Type 우선, video/audio 확장자 포함)
  - 충돌 자동 리네임 (`foo.mp4` → `foo_1.mp4`, race 시 재시도)
  - URL 스킴 검증 (`http`/`https`만 통과)
  - Content-Type 허용 목록 검증 (image/video/audio 세 카테고리 각 포맷 + 거부 케이스)
  - 2 GiB 초과 Content-Length 사전 거부
  - Progress counter throttling 로직 (1 MiB/250ms 경계, 중복 제거)
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
- 통합 테스트 (URL import): `httptest.Server`로 모의 origin 띄워서 검증 (SSE 응답 파싱)
  - 정상 이미지 다운로드 → `start` → `done` 이벤트, 파일 저장 확인
  - 정상 MP4 동영상 다운로드 → `type: "video"` 반환 + `.thumb/` 생성
  - 정상 MP3 음악 다운로드 → `type: "audio"` 반환 + 섬네일 생성 안 함
  - `Content-Length` 누락 → `error: "missing_content_length"` 이벤트
  - 2 GiB + 1 응답 → `error: "too_large"` + 임시 파일 정리 확인
  - `Content-Type: text/html` → `error: "unsupported_content_type"` 이벤트
  - HTTP URL → `done` + `warnings: ["insecure_http"]`
  - URL 확장자와 Content-Type 불일치 → 확장자 교체 + `warnings: ["extension_replaced"]`
  - 부분 실패: 3개 URL 혼합 → 각 URL당 `done`/`error` 1개씩 + `summary` 1개
  - 리다이렉트 6회 → `error: "too_many_redirects"`
  - 큰 파일(>1 MiB) → `progress` 이벤트 ≥1개 포함, 각 이벤트의 `received` 단조 증가
- 단위 테스트 (HLS 플레이리스트 파서):
  - Master playlist에서 최고 `BANDWIDTH` variant 선택 (동률 시 선언 순서)
  - `BANDWIDTH` 누락 variant는 후순위 처리
  - variant URL 상대 경로 resolve (master URL base 기준)
  - `#EXT-X-STREAM-INF` 없는 media playlist는 원본 URL 반환
  - 1 MiB 초과 본문 → 파싱 단계에서 거부 (`hls_playlist_too_large`)
- 통합 테스트 (HLS import): 모의 origin에 master.m3u8 + media.m3u8 + 세그먼트(.ts) 고정 파일 준비
  - 표준 Content-Type(`application/vnd.apple.mpegurl`) + master playlist → 최고 비트레이트 variant의 세그먼트가 선택되어 MP4로 저장 확인 (파일 존재 + ffprobe로 "video/mp4" 확인 가능한 환경에서만)
  - Content-Type `text/plain` + URL `.m3u8` 폴백 → 정상 HLS 분기 진입 확인
  - `start` 이벤트에 `total` 필드 부재 확인 (`json.Marshal` 시 `omitempty` 동작)
  - `progress.received`가 임시 파일 크기 기반으로 단조 증가 확인
  - ffmpeg 종료 코드 non-zero 시뮬레이션 → `error: "ffmpeg_error"` + 임시 파일 정리 확인
  - ffmpeg 미설치 환경: skip 또는 fake binary 주입으로 테스트 (CI 정책에 따름)
  - 비 http/https variant URL(예: `file:///etc/passwd`를 담은 master playlist) → 파싱 단계에서 거부 (error) — ffmpeg까지 내려가지 않음
- 수동 테스트: 브라우저에서 업로드→섬네일→스트리밍 전체 흐름 확인 (썸네일 우하단 시간 오버레이 확인). Rename 후 썸네일·duration 오버레이가 유지되는지 확인.
- 수동 테스트 (URL import): 모달에서 URL 여러 개(이미지/동영상/음악 섞어) 입력 → URL별 프로그래스 바 실시간 진행 확인 → 완료 후 성공/실패 카운트 요약 확인

---

## 9. Boundaries

**항상 할 것 (Always)**
- Range 요청 지원 (스트리밍 seek 필수)
- 업로드 파일은 `/data` 볼륨 내부에만 저장 (path traversal 방지)
- 섬네일은 비동기로 생성 (업로드 응답 차단 안 함)
- Rename 시 `media.SafePath`로 원본·대상 경로 모두 검증 (path traversal 방지)
- Rename은 동일 부모 디렉토리 내에서만 허용 (경로 이동 금지)
- File rename은 `os.Link` + `os.Remove`로 atomic EEXIST 보장 (TOCTOU 방지)
- URL import: HTTPS TLS 인증서 검증, `Content-Length` 사전 검증, 다운로드 누적 2 GiB 캡, Content-Type 허용 목록(image/video/audio) 검증, 임시 파일 → atomic rename, 파일명 sanitize, SSE `Cache-Control: no-cache` 및 즉시 Flush
- HLS import: ffmpeg `-protocol_whitelist "http,https,tls,tcp,crypto"` 강제, ffmpeg `-rw_timeout 30000000`(30s)로 slow-loris 방어, master playlist의 variant URL 스킴도 `http`/`https`만 허용(이중 검증), variant가 master 자기자신으로 resolve되면 media playlist로 fallback(loop 방지), ffmpeg 프로세스는 context 취소·2 GiB 초과 시 kill, 출력 MP4는 기존 `renameUnique` 경로로 atomic rename, 실패 시 stderr는 서버 로그에만 기록(SSE 클라이언트로는 안전한 code만 노출)

**하지 않을 것 (Never)**
- TS 이외 포맷 트랜스코딩 (MP4/MKV/AVI는 원본 그대로 스트리밍)
- 사용자 인증/권한 관리
- 외부 CDN이나 클라우드 스토리지 연동
- Rename 시 확장자 변경 허용 (MIME/타입 감지 일관성 유지)
- Rename 시 자동 suffix 부여 (`_1`, `_2` 등) — 충돌은 항상 409로 거부
- URL import: `Authorization`/쿠키 등 인증 헤더 자동 첨부, `http`/`https` 외 스킴 허용, 2 GiB 초과 다운로드, 허용 목록 밖 Content-Type 저장, `Content-Length` 없는 응답 저장(HLS 경로만 예외), 동시 다운로드(batch는 순차 처리)
- HLS import: 재인코딩(`-c copy`로 리먹싱만, CPU 폭주 방지), DASH(`.mpd`) 지원, 원본 `.m3u8` + `.ts` 세그먼트를 그대로 저장, DRM/암호화 세그먼트 우회 시도, live stream 특별 처리(엔드리스 스트림은 공통 timeout/size 상한으로만 차단)

**Known limitations**
- Folder rename은 `os.Stat` + `os.Rename` 순서로, 두 콜 사이에 동일 이름 폴더가 생성되면 race 발생 가능. 단일 사용자 배포 대상이므로 acceptable.
- HLS live stream은 `TotalTimeout`(10분) 또는 2 GiB 시점에 강제 종료 — 긴 live 컨텐츠는 끝까지 기록되지 않는다. 명시적 live 감지·분기는 없음.
- HLS 다운로드는 `start` 이벤트에 `total`이 없어 클라이언트 프로그래스 바는 indeterminate(수치 없이 애니메이션) 표시가 필요. 기존 UI가 `total` 없음을 허용하는지 §2.5 모달 구현 시 확인.
- HLS 임시 파일 TOCTOU: `os.CreateTemp` → `Close` → ffmpeg `-y` 재오픈 사이에 동일 경로 write 권한을 가진 로컬 프로세스가 symlink로 대체하는 이론적 race 창이 존재. 단일 사용자 자기-호스팅 모델에서 `destDir` write 권한자는 사용자 본인이므로 실제 위협 없음 — 다중 사용자 배포로 확장 시 `O_NOFOLLOW` 또는 ffmpeg stdout 파이프 방식 재검토 필요.
- URL import SSRF 정책(약함)은 설계상 선택. 사설 IP(`127.0.0.1`, `10.x`, `192.168.x`, `169.254.169.254` 등)은 호스트 검증 없이 그대로 fetch 허용 — LAN 미디어 서버 호출 등 정상 케이스 허용이 우선. VPS/다중 사용자 호스팅으로 확장 시 cloud metadata endpoint(`169.254.169.254`)는 무조건 차단 권장.
