# file_server

개인용 로컬 네트워크 멀티미디어 서버. 이미지·동영상·음악을 업로드·탐색·스트리밍하고, URL에서 직접 다운로드하고, TS를 MP4로 영구 변환한다.

- **인증 없음** — 로컬 네트워크/단일 사용자 전제
- **Go 단일 바이너리 + ffmpeg** — Docker 컨테이너 하나로 배포
- **데이터는 Docker named volume**에 영속 저장 (`/data`)

자세한 설계는 [SPEC.md](SPEC.md) 참조.

---

## 주요 기능

### 파일 관리
- 업로드 (multipart, 드래그 앤 드롭)
- 폴더 트리 탐색 (사이드바) + 현재 폴더 리스트/그리드
- 파일·폴더 rename (확장자 고정), 삭제 (폴더는 재귀)
- 폴더 생성 모달

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
| PATCH | `/api/folder?path=` | 폴더 rename |
| DELETE | `/api/folder?path=` | 폴더 재귀 삭제 |
| PATCH | `/api/file?path=` | 파일 rename |
| DELETE | `/api/file?path=` | 파일 삭제 |
| POST | `/api/import-url?path=` | URL/HLS 다운로드 (SSE) |
| POST | `/api/convert` | TS → MP4 영구 변환 (SSE) |
| GET/PATCH | `/api/settings` | 다운로드 설정 |

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
