# Media Manager

개인용 로컬 네트워크 멀티미디어 서버. 이미지·동영상·음악을 업로드·탐색·스트리밍하고, URL에서 직접 다운로드하고, TS를 MP4로 영구 변환한다.

- **인증 없음** — 로컬 네트워크/단일 사용자 전제
- **Go 단일 바이너리 + ffmpeg** — Docker 컨테이너 하나로 배포
- **데이터는 Docker named volume**에 영속 저장 (`/data`)

현재 버전: **0.0.1** — 폴더 CRUD(생성·이름변경·삭제·이동)가 모두 닫힌 첫 공식 릴리즈. 자세한 설계는 [SPEC.md](SPEC.md) 참조.

---

## 주요 기능

### 파일 관리
- 업로드 (multipart, 드래그 앤 드롭)
- 폴더 트리 탐색 (사이드바) + 현재 폴더 리스트/그리드
- 파일·폴더 rename (확장자 고정), 삭제 (폴더는 재귀)
- 폴더 생성 모달 — **사이드바 헤더의 "+ 새 폴더"** 진입 (현재 browse 경로 기준 생성)
- **폴더 작업 동선은 사이드바 트리** — 트리 노드 hover 시 ✎ rename / 🗑 삭제 노출
- **폴더 이동(DnD)** — 트리 노드 또는 메인 리스트 폴더 행을 다른 트리 노드/breadcrumb 위로 끌어 놓기. 자기 자신/자손/동일 부모로의 이동은 거부, 충돌 시 409
- 다중 파일 선택 후 사이드바 폴더로 일괄 이동 (폴더는 단건 이동만)

### 이미지 / 동영상 / 음악
- 이미지 섬네일 자동 생성 (200×200 JPEG, `.thumb/` 사이드카)
- 동영상 섬네일 + duration 오버레이 (ffmpeg, 50%/25%/75% 프레임 폴백)
- HTTP Range 스트리밍 (MP4/MKV/AVI/MP3/FLAC/AAC/OGG/WAV/M4A)
- **TS 파일**: 실시간 remux 스트리밍 + **MP4로 영구 변환** (개별/일괄, SSE 진행률)

### URL 가져오기
- 여러 URL 동시 입력 → 서버가 순차 다운로드 + 디스크 스트리밍 저장
- 이미지/동영상/음악 + **HLS(`.m3u8`)** 지원 (최고 품질 variant 자동 선택, ffmpeg remux)
- Content-Type 기반 확장자 결정, 충돌 시 `_1` 자동 리네임
- SSE 실시간 진행률, 부분 실패 허용

### 탐색 UX
- 정렬·타입 필터·이름 검색 툴바 (URL 상태 동기화)
- "움짤" 필터: GIF + 짧고 작은 동영상 (`≤ 30s`, `≤ 50 MiB`)
- 파일 용량 요약 (breadcrumb 우측) + 개별 size badge

### 다운로드 설정
- 헤더 ⚙ 버튼 → 최대 다운로드 크기 / 타임아웃 조정
- `<dataDir>/.config/settings.json`에 영속화

---

## 실행

### Docker Compose (권장)

```bash
docker compose up -d
```

- 포트: `http://localhost:8080`
- 데이터: Docker named volume `media` (→ 컨테이너 `/data`)
- 볼륨 위치를 로컬 디렉터리로 바꾸려면 `docker-compose.yml`의 `volumes` 매핑을 `./data:/data` 등으로 수정

### 로컬 개발 (Go 직접 실행)

```bash
# ffmpeg 설치 필요 (썸네일 / 스트림 / 변환)
go run ./cmd/server
```

환경변수:

| 변수 | 기본값 | 설명 |
|---|---|---|
| `DATA_DIR` | `/data` | 미디어 파일 저장 루트 |
| `WEB_DIR` | `web` | 정적 프론트엔드 경로 |

---

## API 요약

| Method | Path | 설명 |
|---|---|---|
| GET | `/api/browse?path=` | 디렉터리 목록 |
| GET | `/api/tree` | 폴더 트리 (사이드바) |
| GET | `/api/stream?path=` | Range 스트리밍 (TS는 실시간 remux) |
| GET | `/api/thumb?path=` | 섬네일 (이미지/동영상) |
| POST | `/api/upload?path=` | 멀티파트 업로드 |
| POST | `/api/folder?path=` | 폴더 생성 |
| PATCH | `/api/folder?path=` | 폴더 rename(`{name}`) 또는 이동(`{to}`) |
| DELETE | `/api/folder?path=` | 폴더 재귀 삭제 |
| PATCH | `/api/file?path=` | 파일 rename |
| DELETE | `/api/file?path=` | 파일 삭제 |
| POST | `/api/import-url?path=` | URL/HLS 다운로드 시작 (SSE 응답에 `register`+`queued`+...) |
| GET | `/api/import-url/jobs` | 활성·이력 잡 목록 (페이지 새로고침 시 복원용) |
| GET | `/api/import-url/jobs/{id}/events` | 잡에 라이브 SSE 재구독 (snapshot+events) |
| POST | `/api/import-url/jobs/{id}/cancel` | 배치 전체 취소 (`?index=N`이면 개별 URL) |
| DELETE | `/api/import-url/jobs/{id}` | 종료된 잡을 history에서 제거 (활성이면 409) |
| DELETE | `/api/import-url/jobs?status=finished` | 종료된 잡 일괄 정리 |
| POST | `/api/convert` | TS → MP4 영구 변환 (SSE) |
| GET/PATCH | `/api/settings` | 다운로드 설정 |

mutating 라우트(POST·PATCH·DELETE)는 모두 `Origin == Host` 또는 `Sec-Fetch-Site` allowlist를 통과해야 한다. 거부 시 `403 cross_origin` (상세는 SPEC.md §5.3).

스키마 상세는 [SPEC.md §5](SPEC.md#5-api-design).

---

## 구조

```
cmd/server/        엔트리포인트 (net/http + graceful shutdown)
internal/
  handler/        HTTP 엔드포인트
  media/          타입/MIME 판별
  thumb/          이미지·동영상 썸네일 생성 (ffmpeg 프레임 폴백)
  urlfetch/       URL/HLS 다운로드 (SSE 진행 스트림)
  convert/        TS → MP4 ffmpeg remux 러너
  importjob/      잡 라이프사이클·이벤트 채널·Registry (인메모리)
  settings/       다운로드 설정 영속화
web/              index.html + app.js + style.css (vanilla)
```

---

## 테스트

```bash
go test ./...
```

핸들러·썸네일·urlfetch·convert 계층에 단위/통합 테스트 존재. 일부 테스트는 `ffmpeg`/`ffprobe` 바이너리를 요구.

---

## 기술 스택

- **Backend**: Go 1.26, net/http stdlib, `github.com/disintegration/imaging`
- **Transcoding/Probe**: ffmpeg, ffprobe (alpine apk)
- **Frontend**: Vanilla HTML/CSS/JS (의존성 없음)
- **Container**: Docker multi-stage build (alpine:3.19 런타임)

---

## 릴리즈 노트

### 0.0.1 — 폴더 CRUD 완성판
- **폴더 이동 백엔드**: `media.MoveDir` + `PATCH /api/folder` body 분기(`{name}` rename | `{to}` move). 자기 자신·자손 거부, 충돌 시 409 (자동 suffix 없음 — rename과 일관). EXDEV는 500 (단일 볼륨 전제).
- **사이드바 트리 폴더 운영**: 노드 hover 시 ✎ rename · 🗑 삭제 노출. "+ 새 폴더" 버튼이 메인 헤더에서 사이드바 헤더로 이전 (모바일 드로어에서는 햄버거 → 드로어 열기 후 사용).
- **폴더 DnD**: 사이드바 트리 노드 또는 메인 리스트 폴더 행을 다른 트리 노드/breadcrumb 위로 끌어 놓아 이동. 자기 자신/자손으로 드래그하면 drop 거부.
- **Breaking changes**: 없음. 기존 API/UI 동작 그대로, 새 폴더 이동 경로만 추가.
