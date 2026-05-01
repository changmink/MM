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
- [ ] UI에서 현재 visible 파일을 여러 개 또는 전체 선택해 사이드바 폴더/breadcrumb 경로로 일괄 이동
- [ ] 폴더 생성 (현재 탐색 경로 기준, 이름 입력 모달 — 사이드바에서 진입)
- [ ] 폴더 삭제 (재귀 삭제 — 하위 파일/폴더 + `.thumb/` 디렉토리 포함; 메인 리스트 + 사이드바 트리 모두에서 진입)
- [ ] 폴더 이동 (사이드바 트리 노드 또는 메인 리스트 폴더 행 → 다른 트리 노드/breadcrumb DnD; 자기 자손으로 이동 거부, 충돌 시 409)

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

### 2.1.2 폴더 이동 (Move) 상세

기존 파일 이동(`PATCH /api/file {"to": "..."}`, §5)은 `media.MoveFile`이 디렉토리를 명시적으로 거부(`ErrSrcIsDir`)하여 폴더에 대해서는 사용할 수 없다. 본 기능은 폴더에도 동일한 PATCH 의미론을 부여한다 — `PATCH /api/folder`에 `{"to": "..."}` body를 받으면 폴더 자체를 destDir 안으로 이동.

- **API 형태:** `PATCH /api/folder?path=<src>` body가 `{"name":"..."}`이면 기존 rename, `{"to":"..."}`이면 이동. `PATCH /api/file`이 `patchFile`에서 body를 한 번 읽고 분기하는 것과 동일 패턴(`files.go:133` 참고).
- **이동 의미:** `srcAbs`(폴더)의 base name이 그대로 유지된 채 `destDir` 아래로 옮긴다. 결과 경로는 `destDir/<srcBaseName>`. 이름 변경은 동시에 수행하지 않음(이동과 rename은 별도 호출).
- **충돌 처리:** `destDir/<srcBaseName>`이 이미 존재(파일이든 폴더든)하면 `409 {"error": "already exists"}`. **자동 `_N` suffix 부여 없음** — 폴더는 파일과 달리 자동 suffix가 사용자 의도와 어긋나기 쉬워 명시적 거부가 안전. rename 정책과 일관.
- **자기 자손 이동 방지:** `destDir`이 `srcAbs`와 동일하거나 `srcAbs`의 자손이면 `400 {"error": "invalid destination"}`. 비교는 `filepath.Clean` 후 prefix 검사 + 경계가 path separator로 끝나는지 확인 (예: `/a/b`는 `/a/bc`의 prefix가 아님).
- **동일 부모 거부:** `filepath.Dir(srcAbs) == destDir`이면 `400 {"error": "same directory"}` — 기존 파일 이동(`files.go:227`)과 동일. 의미 없는 이동을 노이즈로 만들지 않음.
- **루트 이동 방지:** `srcAbs == h.dataDir`이면 `400 {"error": "cannot move root"}`. rename 가드와 동일.
- **원자성:** 단일 `os.Rename` 호출(폴더 전체 + 내부 `.thumb/` + 하위 모든 파일이 함께 이동). 사이드카 별도 처리 불필요(폴더 rename과 동일 원리, §2.1.1).
- **Cross-volume 처리:** `os.Rename`이 `EXDEV` 반환 시 **재귀 copy+remove 폴백 없이** `500 {"error": "cross_device"}` 반환. 단일 데이터 볼륨이 전제이며(SPEC §1, Docker volume 단일 마운트), 폴더 재귀 복사는 race·디스크 공간·중간 실패 처리 비용이 크므로 의도적 out-of-scope. 파일 이동은 EXDEV 시 copy+remove 폴백을 유지(`media/move.go:93`) — 단일 파일 단위라 안전.
- **사이드 효과:** 이동된 폴더 안의 파일 경로가 모두 바뀌므로, **현재 browse 경로(`currentPath`)가 이동된 폴더 자신 또는 그 자손**이라면 클라이언트가 새 경로로 navigate 해야 한다 — `rewritePathAfterFolderRename`(폴더 rename에서 사용 중)을 재사용해 `srcOldPath` → `destDir + "/" + baseName`으로 다시 계산.
- **응답:** `200 OK`, `{"path": "/movies/sub", "name": "sub"}` — 새 위치의 절대 상대 경로 + base name(불변).
- **UI 트리거:**
  - 사이드바 트리 노드를 다른 사이드바 트리 노드 위로 드래그
  - 사이드바 트리 노드를 breadcrumb의 다른 경로 위로 드래그
  - 메인 리스트 표(`buildTable`)의 폴더 행을 사이드바 트리 노드 또는 breadcrumb 위로 드래그
- **DnD payload 일반화:** 기존 `dataTransfer`의 `DND_MIME` payload(`{src, paths}`)는 파일 전용이었다. 폴더는 항상 단건 이동이므로 `paths` 배열에 폴더 경로를 그대로 1개 담아 같은 채널을 재사용 — drop 핸들러는 `is_dir` 구분 없이 `PATCH /api/file` 또는 `PATCH /api/folder`로 라우팅한다(클라이언트가 카드/노드 메타에서 `is_dir`를 알고 있음).
- **다중 선택과의 관계:** 폴더는 selected set(`selectedPaths`)에서 제외(`bindEntrySelection`이 `is_dir`이면 체크박스 자체를 표시하지 않음 — 기존 정책 유지). 따라서 폴더 이동은 항상 단건. 멀티 폴더 이동은 out-of-scope.

### 2.1.3 폴더 작업 UI 진입점 정리

기존 폴더 작업 UI가 메인 리스트 표에만 노출되어 있고(이미지/동영상 그리드에는 폴더가 분류되지 않음), 사이드바 트리에는 rename만 있어 폴더 단위 운영이 끊겨 있다. 0.0.1 릴리즈에 맞춰 진입점을 정리한다.

- [ ] **새 폴더 버튼 위치 이동**: 메인 툴바(현재 `#new-folder-btn`이 업로드 영역 근처)에서 제거하고 **사이드바 헤더 영역**(트리 root 위)으로 이동. 동작은 그대로 — 클릭 시 모달, currentPath 기준 생성, 성공 시 `_browse(currentPath, false)` + `_loadTree()`.
  - 사용자 멘탈 모델: "폴더 작업은 사이드바에서" 일관 — rename·delete·move·create가 모두 트리 영역 동선에 모임.
  - 모바일(<600px) 드로어에서도 동일 위치(사이드바 헤더). 드로어가 닫혀 있을 때는 자연스럽게 가려짐.
- [ ] **사이드바 트리 노드에 🗑 버튼 추가**: 기존 ✎ 버튼 옆에. 클릭 시 기존 `deleteFolder(path)` 호출 — 동작 변화 없음(`confirm()` 다이얼로그 + `DELETE /api/folder` + 트리·browse 재조회).
  - 루트는 삭제 불가(서버가 `cannot delete root` 400). UI는 트리 root 자체를 노드로 렌더하지 않으므로 추가 가드 불필요.
- [ ] **사이드바 트리 노드 DnD 활성화**: 노드 row(`.tree-node-row`)에 `draggable=true` + `dragstart`에서 `DND_MIME` payload 전송(`{src: node.path, paths: [node.path], is_dir: true}`). 기존 `attachDropHandlers`는 사이드바 트리 노드와 breadcrumb에 이미 부착되어 있으므로(`tree.js:112`), drop 처리 분기만 추가.
- [ ] **메인 리스트 폴더 행 DnD 활성화**: `buildTable`의 폴더 행(`!entry.is_dir`로 막혀 있던 `attachDragHandlers` 호출, `browse.js:352`)에서 `is_dir` 분기를 풀어 폴더에도 dragstart를 부착.
- [ ] **drop 핸들러 라우팅**: `moveFiles`/`fileOps.js`의 PATCH 호출을 `is_dir` 여부로 분기 — `/api/folder` vs `/api/file`. 폴더 이동 실패 응답 코드는 파일과 공통 처리(`already exists`/`invalid destination`/`same directory`/`cannot move root`/`cross_device` 모두 한 줄 alert).

### 2.2 이미지
- **지원 포맷:** JPG, PNG, WEBP, GIF
- [ ] 업로드 시 섬네일 자동 생성 (200×200px, JPEG)
- [ ] 섬네일은 원본과 동일 경로에 `.thumb/` 디렉토리에 저장
- [ ] 원본 이미지 서빙
- [ ] PNG 업로드 시 자동 JPEG 변환 — settings 토글, 흰 배경 합성, quality 90 (§2.8)

### 2.3 동영상 스트리밍
- **지원 포맷:** MP4, MKV, AVI (원본 스트리밍), TS (ffmpeg 트랜스코딩)
- [ ] HTTP Range 요청 지원 (seek 가능)
- [ ] MP4/MKV/AVI: 트랜스코딩 없이 원본 파일 스트리밍
- [ ] TS: ffmpeg로 실시간 MP4 트랜스코딩 후 스트리밍 (`Content-Type: video/mp4`)
- [ ] MIME 타입 자동 감지
- [ ] TS 파일을 MP4로 **영구 변환**하여 Range/seek 지원 + 반복 트랜스코딩 비용 제거 (§2.3.3)

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

### 2.3.3 TS → MP4 영구 변환

TS 파일은 현재 `/api/stream` 요청 시마다 ffmpeg로 실시간 리먹싱(§2.3, `internal/handler/stream.go:streamTS`)되며, 리먹싱된 MP4는 `.cache/streams/`에 캐시되지만 **Range/seek 미지원**이다. 이 기능은 TS 원본을 리먹싱한 `foo.mp4`를 같은 폴더에 영구 저장해 이후 모든 요청에서 원본 서빙(Range 포함) 경로를 타게 한다.

- **범위(scope):** `/data` 안의 기존 `.ts` 파일 → 동일 폴더에 같은 base name의 `.mp4` 파일 생성. 다른 포맷(MKV/AVI) 변환이나 코덱 재인코딩은 **out of scope**.
- **방식:** ffmpeg 리먹싱(`-c copy`)만 — TS는 보통 H.264/AAC이므로 컨테이너만 교체. 수 초 내 완료(파일 복사 수준 속도), CPU 비용 낮음. 재인코딩 폴백 **없음**.
- **API:** `POST /api/convert` (§5에 상세). body로 파일 경로 배열과 원본 삭제 플래그를 받고 SSE로 진행 스트림 반환 — URL import(§2.6)와 동일 이벤트 스키마(`start`/`progress`/`done`/`error`/`summary`).
- [ ] **개별 변환 트리거:** 동영상 썸네일 카드가 `.ts` 파일이면 "MP4로 변환" 버튼(🎞 또는 텍스트) 표시. 기존 rename/delete 버튼과 동일 레이아웃. 클릭 시 확인 모달 → 변환 시작.
- [ ] **일괄 변환 트리거:** 현재 browse 경로에 `.ts` 파일이 1개 이상이면 상단 툴바(§2.5.2 툴바와 공존 또는 별도 버튼)에 "모든 TS 변환 (N개)" 버튼 표시. 클릭 시 확인 모달 → 현재 **filter/sort 통과한 visible entries 중 `.ts` 전부**를 순차 변환.
- [ ] **ffmpeg 호출(기존 `streamTS` 패턴 재사용):**
  ```
  ffmpeg -y -loglevel error \
    -i <src.ts> \
    -map 0:v:0 -map 0:a:0? \
    -c:v copy -c:a copy \
    -bsf:a aac_adtstoasc \
    -movflags +faststart \
    <tmp.mp4>
  ```
  - 임시 파일 패턴: `.convert-*.mp4` (`.mp4` 확장자 필수 — ffmpeg가 muxer를 확장자로 선택; `5c5f871` 커밋 참고)
  - 출력은 destDir에 `os.CreateTemp` → ffmpeg 실행 → atomic `os.Rename`
  - stderr 버퍼링하여 실패 시 서버 로그에만 기록 (SSE 본문에는 `ffmpeg_error` 코드만 노출, stderr 그대로 노출 안 함)
- [ ] **파일명 결정:** `foo.ts` → `foo.mp4` (base name 유지, 확장자만 `.mp4` 교체)
  - **대소문자:** 원본이 `.TS`/`.Ts` 등이어도 출력은 소문자 `.mp4` 고정
  - **충돌 처리:** 목표 경로(`foo.mp4`)가 이미 존재하면 **`409 Conflict`** 계열 에러(`error: "already_exists"`) — 자동 `_1` suffix **없음**(rename 로직과 일관, Q3(a)). 사용자가 기존 `foo.mp4` 처리 결정해야 함.
- [ ] **원본 처리:**
  - 기본은 **원본 `.ts` 유지**
  - 요청 body의 `delete_original: true`면 최종 rename 성공 후 원본 `.ts` + `.thumb/foo.ts.jpg` + `.thumb/foo.ts.jpg.dur` 삭제
  - UI 모달에 "변환 후 원본 TS 삭제" 체크박스(기본 unchecked)
  - 원본 삭제 실패 시: 변환 자체는 성공 처리(`done` 이벤트) + `warnings: ["delete_original_failed"]` 추가. 서버 로그에 사유 기록.
- [ ] **사이드카:** 새 `foo.mp4`의 썸네일/duration 사이드카는 생성하지 않음 — 기존 lazy 메커니즘(§2.3.1 on-demand, §2.3.2 `.dur` 생성)이 다음 `browse` 시점에 자동 생성. 단순성 우선.
- [ ] **동시성:** 요청 한 건 내에서 배열은 **순차 처리**(동시 ffmpeg 프로세스 1개) — URL import와 동일. 동일 소스에 대한 여러 요청이 겹치면 `stream.go`의 `lockStreamKey`와 동일한 per-path 뮤텍스로 보호.
- [ ] **취소:** 요청 context 취소(클라이언트 연결 끊김 포함) 시 현재 실행 중인 ffmpeg 프로세스 kill + 임시 파일 삭제. 배열의 남은 항목은 처리하지 않음.
- [ ] **타임아웃:** 파일당 **10분** 고정(`convertFileTimeout` 상수, §2.7 URL import 타임아웃과 독립 — 변환은 로컬 I/O라 네트워크 변동성과 무관). 초과 시 ffmpeg kill + `error: "convert_timeout"`. 매우 큰 TS 파일(>2시간)도 remux는 I/O bound이므로 이 제한으로 충분.
- [ ] **크기 상한:** URL import의 `url_import_max_bytes`(§2.7)는 **적용하지 않음** — 로컬 파일 remux는 디스크 공간이 허용하는 한 제한 없음. 디스크 풀 에러는 `error: "write_error"`.
- [ ] **Progress 이벤트:**
  - `start`: `total`에 원본 `.ts` 파일 크기를 채움 (출력 MP4 크기는 사전 예측 불가지만 ≈ 원본 크기, 진행률 대략 계산 가능)
  - `progress`: 임시 `.mp4` 파일의 현재 크기 — HLS import와 동일(500 ms polling + 1 MiB / 250 ms throttling, §5.1.1)
  - `done`: 최종 MP4 파일 크기 + `warnings` (해당 시)
- [ ] **응답 후 UI 갱신:** SSE `summary` 수신 후 클라이언트가 `loadBrowse()` 1회 호출 → 새 `.mp4` + (delete_original 시) 원본 제거가 반영됨.
- **Non-goals:**
  - 재인코딩(CRF, preset 선택 등) — remux 실패는 그대로 `ffmpeg_error` 반환(Q4(a))
  - 다른 포맷 변환(MKV→MP4, AVI→MP4 등) — 범위 외
  - 원본 `foo.ts`를 `foo.mp4`로 **덮어쓰기** (삭제는 별도 단계)
  - 변환 결과 저장 위치 변경(항상 원본 폴더)
  - 변환 큐 영속화(서버 재시작 시 진행 중 변환은 폐기, 재개 없음)
  - `.cache/streams/`의 기존 리먹싱 캐시 재활용(hash 기반 키라 별도 로직이 필요해 복잡도 상승; 단순하게 신규 ffmpeg 1회 실행)

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
- [ ] 폴더 생성 모달 (이름 입력 → 현재 경로에 생성; 진입 버튼은 **사이드바 헤더**에 위치 §2.1.3)
- [ ] 폴더 삭제 확인 모달 (재귀 삭제 경고 문구 포함; 메인 리스트 표 + **사이드바 트리 노드 🗑** 두 곳에서 진입 §2.1.3)
- [ ] 폴더 이동 DnD (사이드바 트리 ↔ 사이드바 트리 / 메인 리스트 폴더 행 → 사이드바 트리 또는 breadcrumb §2.1.2)
- [ ] **URL에서 가져오기 모달**: 업로드 버튼 옆 버튼 → textarea(줄바꿈 구분 URL) → "가져오기" → 각 URL별 실시간 프로그래스 바 표시 (다운로드 중 % / 완료 / 실패 상태) → 전체 완료 시 성공·실패 카운트 요약. **모달 닫기는 뷰 숨김일 뿐, 다운로드는 현재 탭이 살아있는 동안 계속 진행**(§2.6). 닫힌 동안 헤더 우측 미니 배지(`URL ↓ 완료/전체` + 실패 시 `⚠`)로 진행 집계를 노출하고, 클릭하면 모달이 다시 열린다. 진행 중인 배치가 있는 상태에서 재오픈하면 confirm 라벨이 **"새 배치 추가"** 로 바뀌어 기존 row 아래에 새 배치를 append할 수 있다 — 서버는 §2.6의 배치 직렬화 규칙으로 처리한다.
- [ ] **파일 용량 표시** (§2.5.1)
- [ ] **정렬·필터 툴바** (§2.5.2)
- [ ] **움짤 필터** (§2.5.3)
- [ ] **TS → MP4 변환 트리거** (§2.3.3): 동영상 카드별 "MP4로 변환" 버튼 + 현재 폴더 일괄 변환 버튼 + 진행 모달(URL import 모달과 동일한 SSE 진행 바 스타일)
- [ ] **사이드바 sticky-until-bottom + 업로드 존 sticky**: 사이드바는 콘텐츠 자연 높이로 자라며 `syncSidebarSticky()` 가 sticky `top` 을 동적으로 계산해, 트리가 길어도 페이지 스크롤만으로 마지막 노드까지 닿게 한다(내부 overflow 스크롤 없음). 업로드 존은 헤더 바로 아래에 sticky 로 고정되어 본문 스크롤 중에도 항상 보인다. 모바일(<600px) 드로어 동작은 그대로. 상세: [`tasks/spec-tree-full-visible.md`](tasks/spec-tree-full-visible.md).
- [ ] **다중 파일 선택 이동**: 파일 카드/테이블 행에서 체크박스로 파일을 선택하고, 툴바에서 현재 필터/검색을 통과한 visible 파일 전체를 선택/해제할 수 있다. 선택된 파일 중 하나를 사이드바 폴더 또는 breadcrumb 경로로 드래그하면 선택 묶음을 기존 `PATCH /api/file {"to": ...}` API로 순차 이동한다. 선택이 없거나 선택되지 않은 파일을 드래그하면 기존 단일 파일 이동 동작을 유지한다. 폴더는 선택 대상에서 제외한다. 상세: [`tasks/spec-multi-file-move-ui.md`](tasks/spec-multi-file-move-ui.md).
- [ ] **Rubber-band 영역 선택** (§2.5.4)

### 2.5.1 파일 용량 표시

현재 browse 경로에 직접 있는 파일들의 개수·합계를 상단에 요약하고, 개별 파일 크기를 모든 뷰에서 볼 수 있게 한다. 서버 API 변경 없음 — `/api/browse` 응답에 `size` 필드가 이미 존재하므로 클라이언트(`web/app.js`, `web/style.css`)만 수정한다.

- **범위(scope):** 현재 browse 경로에 직접 있는 파일만. 하위 폴더 재귀 합산은 **하지 않음**. 폴더는 메인 리스트에 표시되지 않으므로(`renderFileList`가 파일만 분류) 자연스럽게 제외됨.
- [ ] **합계 표시 위치:** breadcrumb 줄 오른쪽 끝에 `파일 {N}개 · {formatSize(total)}` 형태로 렌더. 파일 0개이면 요약 영역 숨김(빈 텍스트).
  - 좌측: 기존 breadcrumb 경로 링크. 우측: 새 `#browse-summary` 요소. `justify-content: space-between` 또는 `margin-left: auto`로 정렬.
- [ ] **합계 계산:** `entries.filter(e => !e.is_dir).reduce((s, e) => s + (e.size || 0), 0)` — `is_dir=true`는 제외. 음수/`NaN`이 들어올 일은 없으나 `|| 0`으로 방어.
- [ ] **개별 파일 용량:**
  - **기타/음악 표** (`buildTable`): 기존 `크기` 열 유지 (변경 없음).
  - **이미지 그리드** (`buildImageGrid`): 섬네일 **좌상단** size badge (`.size-badge`) 표시. 좌하단은 파일명 텍스트(`.thumb-name`)의 시작 부분과 겹쳐 이름이 가려지므로 상단으로 배치.
  - **동영상 그리드** (`buildVideoGrid`): 섬네일 **좌상단** size badge + 기존 **우하단** duration badge 병존. size badge는 duration badge와 동일한 반투명 배경·흰 글씨(시각 스타일), 위치만 다름.
- [ ] **포맷:** 기존 `formatSize` 그대로 사용 (`1.5 GB`, `512 MB`, `0 B` 등). 새 포맷 함수 도입 금지.
- [ ] **갱신 타이밍:** `browse()` 호출 시 한 번 계산 후 렌더. 업로드·삭제·rename 후에는 기존과 동일하게 `loadBrowse()`가 재호출되어 자동 갱신됨 (추가 작업 불필요).
- **Non-goals:**
  - 폴더 재귀 크기(디렉토리별 합산) — 범위 외.
  - 사이드바 트리(`renderTreeChildren`)에 크기 표시 — 범위 외.
  - 합계의 실시간 스트리밍 업데이트 — 기존 UI 패턴 일치(전체 재조회).

### 2.5.2 정렬 및 필터링 툴바

`/api/browse` 응답은 그대로 두고, 클라이언트에서 현재 폴더의 파일을 정렬·타입 필터·이름 검색할 수 있게 한다. 정렬·필터 상태는 URL 쿼리에 저장해 새로고침·공유·뒤로가기에서 복원된다.

- **범위(scope):** 현재 browse 경로에 직접 있는 파일만. 하위 폴더 재귀 검색 **없음**. 폴더는 사이드바 트리가 담당하며 툴바의 영향을 받지 않음.
- [ ] **툴바 위치 및 구성:** `#file-list` 바로 위에 `<div id="browse-toolbar">` 신설. 왼쪽→오른쪽 순서:
  1. **타입 세그먼트** — `전체 / 이미지 / 동영상 / 음악 / 기타` 버튼 5개 (`data-type="all|image|video|audio|other"`). 단일 선택(라디오 스타일). 기본 `all`.
  2. **검색 입력** — `<input type="search" placeholder="이름으로 검색">`. 대소문자 무시. `String.prototype.trim()` 후 빈 문자열이 아니면 파일명에 부분문자열 매칭(`name.toLowerCase().includes(q.toLowerCase())`).
  3. **정렬 select** — 6개 옵션:
     - `이름 ↑` (`name:asc`, 기본)
     - `이름 ↓` (`name:desc`)
     - `크기 ↑` (`size:asc`)
     - `크기 ↓` (`size:desc`)
     - `수정일 ↑` (`date:asc`, 오래된 것 먼저)
     - `수정일 ↓` (`date:desc`, 최신 먼저)
- [ ] **URL 파라미터:** 기본값(`name:asc`, 빈 검색, `all`)은 **생략**하여 URL을 깨끗하게 유지. 값이 있을 때만 포함:
  - `?path=/sub&sort=size:desc&q=foo&type=video`
  - 유효하지 않은 값(화이트리스트 밖)은 기본값으로 fallback 후 URL에서 제거.
  - **경로 이동:** `pushState`(뒤로가기 작동).
  - **툴바 변경:** `replaceState`(히스토리 스팸 방지).
  - **popstate:** URL 재파싱 후 툴바 컨트롤 값 복원 + 재렌더.
- [ ] **정렬 규칙:**
  - `name`: `String.prototype.localeCompare(undefined, { numeric: true, sensitivity: 'base' })` — 자연스러운 한글/숫자 순. 대소문자 무시.
  - `size`: 숫자 비교. 동률 시 이름 오름차순 tiebreaker.
  - `date`: `mod_time` ISO 문자열을 `Date` 파싱 후 `getTime()` 비교. 동률 시 이름 오름차순.
  - 모든 정렬은 **타입 섹션 내부**에만 적용. 섹션 순서(이미지→동영상→음악→기타)는 유지.
- [ ] **필터 적용 순서:** (1) 타입 → (2) 이름 검색 → (3) 정렬. 세 단계 모두 통과한 엔트리만 렌더.
- [ ] **섹션 구조 유지:** `renderFileList`의 이미지/동영상/음악/기타 분할은 그대로. 타입 필터로 가려진 섹션은 **섹션 타이틀도 함께 숨김**(0개 섹션 표시 금지 — 기존 규칙 동일).
- [ ] **합계(§2.5.1) 연동:** 합계 표시는 **필터 통과한 visible entries** 기준으로 재계산. "전체 X개 중 Y개 표시" 형태는 아님 — 단순히 `파일 Y개 · {size}`.
- [ ] **라이트박스/재생목록 연동:** `imageEntries`, `videoEntries`, `playlist`(오디오)는 **현재 visible 결과**로 재설정. 필터로 가려진 항목은 lightbox prev/next, 오디오 next 대상에서도 제외.
- [ ] **빈 결과 처리:** 필터 결과가 0개이면 기존 "파일이 없습니다" 문구 대신 "검색 결과가 없습니다" 표시. 파일 자체가 0개인 폴더와 구분.
- [ ] **성능:** 디바운스 없음 — 검색 입력마다 즉시 재렌더. 기준 규모(약 1k 엔트리)에서 재렌더 비용 무시 가능.
- [ ] **반응형:** 툴바는 좁은 화면에서 2줄로 wrap 허용 (`flex-wrap: wrap`). 세그먼트·검색·정렬 각각 최소 폭 유지.
- **Non-goals:**
  - 섹션별 개별 정렬·필터.
  - 재귀 검색(하위 폴더까지 이름 매칭).
  - `localStorage` persistence — URL이 단일 진실.
  - 확장자·날짜 범위 등 세부 필터.
  - 서버 사이드 정렬/페이지네이션 — 현재 규모에서 불필요.
- **서버 변경:** 없음 (`/api/browse` 응답 그대로).

### 2.5.3 움짤 필터

타입 세그먼트(§2.5.2)에 6번째 항목 **"움짤"** 을 추가한다. 움짤은 "짧고 작은 움직이는 미디어"를 한 번에 훑기 위한 단축 필터다.

**움짤 정의 (필터 통과 조건):**
- **GIF (`mime === 'image/gif'`)**: **무조건 움짤.** GIF는 서버가 duration을 제공하지 않고, 실무에서 대부분 짧고 가볍다는 사용자 판단에 따라 크기·길이 체크를 생략한다.
- **동영상 (`type === 'video'`)**: `size ≤ 50 MiB` (50 × 1024² = 52,428,800 B) **AND** `duration_sec != null && duration_sec <= 30` 둘 다 만족해야 한다. `duration_sec`이 `null`(썸네일 placeholder / ffprobe 실패 등)이면 움짤로 간주하지 **않음** — 길이를 모르므로 보수적으로 제외.
- **그 외** (정적 이미지 — JPG/PNG/WEBP 등, 음악, 기타): 움짤 아님.

**UI:**
- [ ] 툴바 타입 세그먼트 맨 끝에 6번째 버튼 `움짤` (`data-type="clip"`). 기존 순서 유지: `전체 / 이미지 / 동영상 / 음악 / 기타 / 움짤`.
- 단일 선택(라디오) 동작.

**배타적 분류 (3-way):** `이미지 / 동영상 / 움짤`은 서로 **배타적**으로 분류된다. 움짤 조건에 해당하는 파일은 `이미지`나 `동영상` 탭에 **나타나지 않는다**:
- `이미지` 탭: 정적 이미지만 (GIF 제외)
- `동영상` 탭: 움짤 아닌 동영상만 (길거나 큰 동영상 / duration 미상 동영상)
- `움짤` 탭: GIF + 움짤 동영상
- `전체` 탭은 이 배타 규칙을 적용하지 않음 — 모든 파일을 자연 타입 섹션에 표시 (움짤도 이미지/동영상 섹션에 포함).
- `음악 / 기타` 탭은 움짤 조건과 무관 (은 해당 타입 내 움짤이 존재할 수 없음).

**URL 파라미터:**
- [ ] `TYPE_VALUES`에 `clip` 추가. 허용값: `all|image|video|audio|other|clip`. 기본 `all`은 여전히 URL에서 생략.
- 움짤 선택 시 URL `?...&type=clip`. 새로고침·공유에서 동일 상태 복원.

**필터 적용 (`applyView`):**
- [ ] 움짤 판별은 헬퍼로 분리:
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
  ```
- [ ] 타입 분기:
  - `view.type === 'all'`: `out = files`
  - `view.type === 'clip'`: `out = files.filter(isClip)`
  - 그 외(`image|video|audio|other`): `out = files.filter(e => e.type === view.type && !isClip(e))`
- 이후 이름 검색(q) · 정렬(sort)은 기존대로 순차 적용.

**섹션 구조:**
- 섹션 분할(이미지/동영상/음악/기타)은 유지. 움짤 모드에서 살아남은 GIF는 "이미지" 섹션에, 살아남은 짧은 동영상은 "동영상" 섹션에 표시된다. 음악·기타 섹션 제목은 0개가 되어 자연스럽게 숨김.

**합계·라이트박스·재생목록 연동:**
- §2.5.1·§2.5.2와 동일. 움짤 모드에서도 합계는 보이는 파일 기준, lightbox는 visible 이미지만 순환.

**Non-goals:**
- APNG / animated WEBP 감지 — 확장자만으로 판별 불가, ffprobe 호출이 필요해 범위 외.
- GIF duration 서버 측 추출 — 본 기능은 서버 무변경 원칙. 필요해지면 별도 Phase.
- 움짤 전용 뷰(섹션 병합, 자동재생 미리보기 등).
- 움짤 조건 커스터마이징 (50MB / 30s 상수, 사용자 설정 없음).

**서버 변경:** 없음.

### 2.5.4 Rubber-band 영역 선택

빈 영역에서 시작한 마우스 드래그로 사각형을 그려 그 안의 카드/행을 일괄 선택한다. Phase 22의 다중 선택 인프라(`selectedPaths`, [`tasks/spec-multi-file-move-ui.md`](tasks/spec-multi-file-move-ui.md))를 그대로 활용 — 별도 selection 상태 도입 안 함.

**활성화 조건:**
- mousedown이 카드/행/버튼/링크/체크박스/모달/라이트박스가 아닌 **빈 영역**에서 시작.
- **데스크톱 (>600px) 한정** — 모바일/터치는 기존 체크박스 UX 유지.
- 좌클릭(`button === 0`)만 — 우클릭·중클릭은 무시.

**상호작용:**
- mousedown → 시작점 + 기존 selection 스냅샷 기록.
- mousemove **5px 초과** 이동 → 반투명 overlay 생성, 시작점부터 커서까지의 사각형 그림. (5px 이하 이동은 click으로 간주해 selection 미변경.)
- 드래그 중 → 사각형과 **교차**(intersect)하는 visible 카드/행을 `selectedPaths`에 실시간 반영.
- mouseup → overlay 제거, selection 확정.
- ESC → 드래그 중단 + **mousedown 시점 selection 스냅샷으로 복원**.

**Modifier 키:**
- 기본(modifier 없음) → 드래그 시작 시 selection **대체** (시작 시 클리어 후 사각형 결과 적용).
- Ctrl 또는 Shift+드래그 → 기존 selection **유지 + 사각형이 잡은 항목 추가** (additive only — 사각형이 줄어들어도 한 번 들어온 항목은 빠지지 않음).

**대상:**
- 이미지 그리드 / 비디오 그리드 / 테이블 행. **visible 항목만** — 필터로 가려진 항목은 사각형이 덮어도 미선택.
- 폴더 카드는 selection 정책상 제외(§2.1.3) — `bindEntrySelection`이 이미 차단.

**시각:**
- overlay는 `position: absolute`, 반투명 accent 색 배경 + 1px solid border. main 영역 안에서만 그려져 사이드바·헤더·툴바 위로 안 넘침.
- 드래그 중 텍스트 선택 차단(`user-select: none`).

**Non-goals:**
- 모바일/터치 long-press + drag.
- 사이드바 트리에서의 영역 선택.
- 키보드 화살표 + Shift 범위 선택.
- 드래그 중 viewport 자동 스크롤(사각형이 viewport 끝에 닿을 때 자동 따라감) — 별도 phase.
- 사각형이 줄어들 때 toggle off (additive only 정책 일관).

**서버 변경:** 없음.

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
  5. DNS 해석 결과가 loopback/private/link-local/multicast/unspecified IP이면 거부 (`error: "private_network"`). 최초 요청과 redirect 이후 실제 dial 대상 모두에 적용된다. HLS의 master/variant playlist 본문, segment, key, init segment fetch는 모두 같은 보호 클라이언트를 통과하므로 DNS rebinding 우회가 닫혀 있다 — 자세한 흐름은 §2.6.1.
  6. 요청 시 `Authorization` 등 인증 헤더 자동 첨부 안 함
  7. 응답 헤더 검증:
     - `Content-Type`이 위 허용 목록에 없으면 거부 (`error: "unsupported_content_type"`)
     - **HLS 분기** (§2.6.1): Content-Type이 HLS 플레이리스트이거나, URL 경로가 `.m3u8`로 끝나고 Content-Type이 `text/plain` / `application/octet-stream` / 파싱 불가 → HLS 흐름으로 이탈하고 이하 §2.6 검증은 건너뜀
     - `Content-Length` 헤더가 있고 설정값 `url_import_max_bytes`(§2.7) 초과면 다운로드 시작 전 거부 (`error: "too_large"`)
     - `Content-Length` 헤더 없어도 다운로드 진행 (아래 누적 카운터로 런타임 보호)
  8. 임시 파일에 스트리밍 저장
  9. 다운로드 중 누적 바이트가 `url_import_max_bytes` 초과 시 즉시 중단 + 임시 파일 삭제 (`error: "too_large"`)
  10. 검증 통과 시 임시 파일 → 최종 경로로 atomic rename
  11. 이미지·동영상 성공 시 `.thumb/{name}.jpg` 섬네일 비동기 생성 (음악은 생략)
- [ ] **진행 이벤트 (SSE)**: URL당 최소 `start` → `done` 또는 `start` → `error`. 큰 파일은 중간에 `progress` 이벤트를 주기적으로 방출 (§5.1.1). 배치 단위로는 응답 헤더 직후 `register` 이벤트 1회(jobId 부여) → `queued` 이벤트 1회 → URL 단위 이벤트들 → `summary` 이벤트로 종료한다 (§5.1).
- [ ] **타임아웃**: 연결 10초 + 전체 다운로드는 설정값 `url_import_timeout_seconds`(§2.7, 기본 30분, 개별 URL 단위). 초과 시 `error: "download_timeout"`
- [ ] **배치 직렬화**: 서버는 `Handler.importSem`(size-1 채널 세마포어)로 `POST /api/import-url`을 **프로세스 전역에서 한 번에 한 배치씩 순차 처리**한다. 동시에 여러 클라이언트(또는 같은 사용자가 모달 재오픈으로 추가한 두 번째 배치)가 POST를 보내면, 응답 헤더는 즉시 가고 `queued` 이벤트가 곧바로 송출되지만, 후속 `start` 이벤트는 앞선 배치가 끝날 때까지 대기한다. 세마포어 wait는 **잡 컨텍스트(`Job.Ctx()`)와 함께 select** 되므로, 잡이 명시적 취소되면 미획득 상태로 즉시 종료된다. 클라이언트 disconnect는 잡을 취소하지 않는다 — 아래 *백그라운드 진행*.
- [ ] **잡 레지스트리 (인메모리)**: 모든 import 배치는 서버 `internal/importjob.Registry`에 등록되어 **request lifecycle과 분리**되어 산다. POST 응답 첫 `register` 프레임으로 받는 `jobId`(`imp_[a-z2-7]{8}`)는 새로고침/탭 재오픈 후 `GET /api/import-url/jobs/{id}/events`(§5.1)로 다시 구독할 수 있는 영구 식별자.
- [ ] **백그라운드 진행**: 클라이언트 fetch가 끊겨도(모달 close, 새로고침, 탭 닫음) **서버 잡은 끝까지 실행된다**. 클라이언트(§2.5)는 페이지 로드 시 `GET /api/import-url/jobs`로 활성/이력 잡 목록을 받아 row와 헤더 배지를 복원하고, 활성 잡에 대해 `EventSource`로 라이브 진행 stream에 재합류한다. 같은 사용자가 탭 두 개를 열면 같은 잡의 진행을 양쪽이 fan-out으로 본다(subscriber당 64-event 버퍼, drop on full — lifecycle 이벤트 누락 시 SetStatus(terminal)이 채널을 close하므로 핸들러가 hang하지 않는다).
- [ ] **취소**: `POST /api/import-url/jobs/{id}/cancel`(배치 전체) 또는 `POST .../cancel?index=N`(개별 URL). 진행 중 URL이면 per-URL ctx cancel → urlfetch 종료 → `error: "cancelled"` 이벤트. 대기 중 URL이면 즉시 `cancelled`로 마킹 + 이벤트 emit, 워커는 도달 시점에 skip. 잡 status 결정 규칙: succeeded≥1 → `completed`, 그 외 cancelled가 있으면 `cancelled`, 아니면 `failed`.
- [ ] **이력 dismiss**: 종료된 잡은 `DELETE /api/import-url/jobs/{id}`로 history에서 제거. 활성 잡은 409(먼저 cancel 필요). 종료된 잡 일괄 정리는 `DELETE /api/import-url/jobs?status=finished`. UI는 모달 footer "완료 항목 모두 지우기" 버튼.
- [ ] **활성 잡 cap**: 동시에 active(`queued`+`running`) 잡은 `MaxQueuedJobs=100`개로 제한. 초과 시 POST → `429 too_many_jobs`. 단일 사용자 + 직렬 처리라 사실상 도달 불가능한 안전장치.
- [ ] **서버 재시작 시 휘발**: 잡 레지스트리는 인메모리. SIGINT/SIGTERM은 `signal.NotifyContext` → `Registry.CancelAll()` → `Registry.WaitAll(5s)`(병렬 fan-in) → 진행 중 urlfetch/ffmpeg 정리 → `httpServer.Shutdown(10s)`. 재시작 후 새로고침하면 잡이 0건이고 임시 파일은 정리되어 있다. **디스크 영속 잡 큐는 의도적 비목표** (단일 사용자 LAN, 비용 대비 가치 낮음).
- [ ] **워커 panic 보호**: `runImportJob`은 `defer recover()`로 panic 시 `summary{Failed: len(URLs)}` + `SetStatus(StatusFailed)`로 잡을 종료 상태에 안착시킨다. 그렇지 않으면 슬롯이 영구 점유되고 graceful shutdown이 5초 대기를 모두 소비.
- [ ] **로그 redact**: `urlfetch` 실패 로그(`logFetchError`)는 URL의 userinfo(`user:pass@host`)와 sensitive query 키(`token`, `signature`, `key`, `apikey`, `password`, `secret` + AWS 시그니처 키)를 자동 마스킹한다. `*url.Error`도 동일하게 처리.
- [ ] **URL 길이 cap**: `normalizeURLs`에서 2 KB 초과 URL은 무시. JobSnapshot에 임의 길이 텍스트가 영구 적재되어 `GET /jobs`로 노출되는 것을 차단.
- [ ] **설정 스냅샷 시점**: `url_import_max_bytes` / `url_import_timeout_seconds`(§2.7)는 **POST 도착 시점**(세마포어 acquire 이전)에 스냅샷을 찍는다. 큐잉 중인 배치는 자기가 받은 시점의 값을 그대로 쓰며, 진행 중에 PATCH /api/settings로 값이 바뀌어도 영향받지 않는다.
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
- [ ] **다운로드 흐름** — ffmpeg는 항상 검증된 local 파일만 입력으로 받는다. 원격 fetch는 모두 Go 보호 클라이언트가 수행하여 DNS rebinding 우회를 차단한다:
  1. HLS 감지 → 초기 응답 본문을 메모리로 읽고 즉시 연결 close (master 본문 cap 1 MiB)
  2. master playlist 파싱 → `#EXT-X-STREAM-INF`이 있으면 BANDWIDTH 최고 variant 선택. 없으면 본문 자체가 media playlist
  3. variant URL이 master와 다르면 보호 클라이언트(`publicOnlyDialContext`)로 variant playlist 본문을 새로 fetch (cap 1 MiB)
  4. media playlist 파싱 → segment(`#EXTINF`), key(`#EXT-X-KEY:URI=`), init(`#EXT-X-MAP:URI=`) URI 추출, base 기준 resolve, 스킴 `http`/`https` 검증, segment 개수 cap **10,000**(`hls_too_many_segments`)
  5. 임시 디렉터리 `<destDir>/.urlimport-hls-<random>/` 생성 (browse dot-prefix 필터로 자동 숨김)
  6. 보호 클라이언트로 모든 segment / key / init을 임시 디렉터리에 사전 다운로드:
     - segment → `seg_NNNN.<ext>` (whitelist `.ts`/`.m4s`/`.mp4`/`.aac`/`.m4a`/`.vtt`, 외에는 `.bin`)
     - key → `key_N.bin` (per-resource cap 64 KiB)
     - init → `init_N.<ext>` (per-resource cap 16 MiB)
     - 누적 바이트가 `url_import_max_bytes`(§2.7) 초과 시 즉시 중단 → `error: "too_large"`
     - 한 리소스라도 실패(http 4xx/5xx, TLS, dial, private IP 등) → 즉시 중단, 임시 디렉터리 cleanup
  7. URI를 local 상대 경로로 재작성한 `playlist.m3u8`을 임시 디렉터리에 작성 (다른 라인 — `#EXTM3U`, `#EXTINF`, `#EXT-X-VERSION`, `#EXT-X-BYTERANGE`, `#EXT-X-ENDLIST` 등 — 은 verbatim 유지)
  8. ffmpeg 프로세스 spawn: `ffmpeg -hide_banner -loglevel error -protocol_whitelist "file,crypto" -allowed_extensions ALL -i <localPlaylistPath> -c copy -bsf:a aac_adtstoasc -f mp4 -movflags +faststart -y <outputPath>`
     - `outputPath`는 임시 디렉터리 안 `output.mp4`
     - stderr는 버퍼링하여 실패 시 로그로 남김 (응답 본문으로는 노출 안 함)
     - argv invariant — `-i` 인자에는 절대 local 경로만 들어가고 `http://`/`https://`/`tcp` 등 네트워크 protocol 토큰은 절대 등장하지 않는다 (단위 테스트로 잠금)
  9. 별도 goroutine에서 500 ms 주기로 출력 MP4 `os.Stat` → 현재 크기 → `progress` 이벤트 (1 MiB / 250 ms throttling)
  10. 출력 크기가 누적 cap의 잔여를 초과 시 ctx cancel로 ffmpeg 종료 → `error: "too_large"`
  11. `url_import_timeout_seconds`(§2.7) 또는 요청 context 취소 시 ctx 전파로 ffmpeg 종료 → `error: "download_timeout"` (또는 context에 따라 `network_error`)
  12. ffmpeg exit code 0 → 출력 MP4를 destDir로 atomic rename (`renameUnique`)
  13. ffmpeg exit code non-zero → `error: "ffmpeg_error"`
  14. 모든 경로(성공/실패/취소/패닉)에서 임시 디렉터리는 `defer os.RemoveAll`로 정리
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
  - `progress.received`는 두 단계의 단조 증가 카운터:
    - Phase 1 (segment/key/init 사전 다운로드): 누적 다운로드 바이트
    - Phase 2 (ffmpeg 출력): Phase 1 총량 + 출력 MP4 size
    - 클라이언트 입장에선 단일 monotonic 값. 의미 변경 없이 값만 단조 증가
  - `done.size`: 최종 MP4 파일 크기 (atomic rename 직후 `Stat`)
- [ ] **보안:**
  - ffmpeg `-protocol_whitelist "file,crypto"` 로 제한 — 네트워크 protocol(http/https/tcp/tls/udp/rtp 등) 모두 차단. 입력 playlist + 모든 segment/key/init은 Go가 미리 받아 임시 디렉터리에 둔 local 파일이므로 ffmpeg가 자체 DNS 해석을 수행할 여지가 없다 — 이것이 DNS rebinding 차단의 핵심 invariant
  - master/variant/segment/key/init URI의 스킴(`http`/`https`)은 Go 파서가 검증, 모든 fetch는 `publicOnlyDialContext`(IP-pin) 통과 — 사설 IP는 dial 시점에 거부
  - `-allowed_extensions ALL`은 local 파일 입력에만 영향, 네트워크 fetch가 없으므로 LFI/포트스캔 위험 없음
  - ffmpeg 호출 시 인자는 argv로 전달 (쉘 미개입) — shell injection 불가
- [ ] **Live stream 처리:** 명시적 거부·감지 없음. 엔드리스 스트림은 다운로드 타임아웃(§2.7) 또는 최대 크기 상한(§2.7)에서 자연 중단되고 `download_timeout`/`too_large`로 실패 처리. 부분적으로 기록된 임시 파일은 폐기.
- [ ] **DRM/Fairplay/암호화 세그먼트:** 지원 안 함 — ffmpeg가 실패하면 그대로 `ffmpeg_error` 반환

### 2.7 다운로드 설정 (Settings)

URL Import(§2.6)와 HLS Import(§2.6.1)가 공유하는 두 개의 런타임 설정을 UI에서 조정할 수 있다. 서버 전역 단일 값(single-tenant 배포).

- **설정 항목:**
  - `url_import_max_bytes` — URL/HLS 다운로드 누적 바이트 상한. 기본 **10 GiB** (`10 * 1024³ = 10737418240`). 경계: 1 MiB ~ 1 TiB (`1048576` ~ `1099511627776`).
  - `url_import_timeout_seconds` — URL/HLS 다운로드 per-URL 총 타임아웃. 기본 **1800초(30분)**. 경계: 60 ~ 14400초 (1분 ~ 240분).
  - `auto_convert_png_to_jpg` — `/api/upload`에서 PNG를 받으면 JPEG로 자동 변환할지 여부 (§2.8.1). 기본 **`true`**. boolean이라 경계 검증 없음.
- **저장 위치:** `<dataDir>/.config/settings.json` — browse에서 숨김(`.`-prefix 필터로 기존에 제외됨). atomic write(temp + rename).
- **형식:**
  ```json
  {
    "url_import_max_bytes": 10737418240,
    "url_import_timeout_seconds": 1800,
    "auto_convert_png_to_jpg": true
  }
  ```
- [ ] **초기 로드:** 서버 시작 시 `settings.json` 읽기. 파일 부재·JSON 파싱 실패·경계 위반 값이면 경고 로그 후 기본값 사용(쓰기는 하지 않음 — 사용자가 PATCH할 때까지 메모리 default).
- [ ] **요청별 적용:** 각 URL import/HLS 요청 **시작 시점**에 현재 설정 스냅샷을 찍어 사용. 다운로드 중간에 PATCH가 와도 진행 중인 요청은 원래 값 유지(race-free).
- [ ] **UI:** 헤더 ⚙ 버튼 → 설정 모달
  - 필드 1: 최대 다운로드 크기 (MiB 단위 number input, `1` ~ `1048576`), 옆에 helper text로 GiB 환산 표시 (예: `10240 MiB ≈ 10.0 GiB`)
  - 필드 2: 다운로드 타임아웃 (분 단위 number input, `1` ~ `240`)
  - 필드 3: "PNG 업로드 시 JPG로 자동 변환" 체크박스 (기본 ON, §2.8.1)
  - 저장 버튼 → `PATCH /api/settings` → 성공 시 모달 닫고 값 캐시 갱신, 실패 시 모달 내부에 에러 메시지 표시
- [ ] **서버 검증:** PATCH 시 세 필드 모두 검사 — `url_import_max_bytes` / `url_import_timeout_seconds`는 경계, `auto_convert_png_to_jpg`는 boolean 타입. 범위 밖 값은 `400 {"error": "out_of_range", "field": "url_import_max_bytes" | "url_import_timeout_seconds"}`, 타입 오류는 `400 {"error": "invalid request"}`. 저장 실패 시 `500 {"error": "write_failed"}`.
- [ ] **Content-Length 누락 허용:** §2.6에서 `missing_content_length` 거부 정책은 제거. CL 없이도 다운로드 진행하며 런타임 누적 바이트 카운터가 `url_import_max_bytes` 초과 시 즉시 중단(HLS와 동일한 size watcher 방식).

### 2.8 PNG → JPG 변환

PNG 파일을 JPEG로 영구 변환한다. 두 진입점:

1. **자동 업로드 변환** (§2.8.1): `/api/upload`로 PNG가 들어오면 디스크에 저장하기 전에 JPEG로 변환. settings 토글로 ON/OFF.
2. **수동 변환** (§2.8.2): `POST /api/convert-image`로 기존 PNG 파일을 명시적으로 변환. UI 카드별 버튼 + 폴더 일괄 변환 버튼.

용도: 사진/스크린샷 갤러리 운용에서 PNG는 무손실 압축 특성상 같은 콘텐츠도 JPEG의 수 배 크기다. 단일 사용자 미디어 서버에서 알파 채널이 필요한 케이스는 드물고, 디스크 절감 효과가 더 크다는 사용자 판단. 알파 채널(투명도) 손실은 JPEG의 구조적 한계이므로 흰 배경 합성으로 처리 — 알파가 필수인 PNG는 자동 변환을 끄거나 수동 변환을 회피해야 한다.

**구현 공통 규약:**
- **라이브러리:** 기존 `github.com/disintegration/imaging`(§3) 재사용. PNG 디코드 + JPEG 인코드 모두 동일 라이브러리. ffmpeg 호출 없음.
- **JPEG quality:** **90 고정.** 사용자 설정 없음 (단순성 우선; settings에 quality 필드 추가하지 않음).
- **알파 채널 처리:** 알파가 있는 PNG는 **흰색(#FFFFFF) 배경에 합성**한 뒤 JPEG로 인코드. `image.NewRGBA(bounds)` → 흰색 fill → `draw.Draw(dst, bounds, src, image.Point{}, draw.Over)` 패턴.
- **메모리 폭주 방어 (decompression bomb):** 디코드 전에 `image.DecodeConfig`로 헤더만 먼저 읽어 `width × height > 64M 픽셀`(≈ 8K×8K, RGBA로 ~256 MiB)인 입력을 거부한다 (`imageconv.MaxPixels`, sentinel `imageconv.ErrImageTooLarge`). 5–60 KB짜리 zlib bomb이 65535×65535 헤더로 16 GiB 할당을 요구하는 시나리오 차단. handler는 wire code `image_too_large`로 매핑(§5), 자동 업로드 변환(§2.8.1)에서는 `convert_failed` 폴백과 동일하게 원본 PNG 보존 + warning.
- **EXIF/메타데이터:** 보존하지 않음. PNG에는 거의 없고, JPEG 인코더 기본 동작에 맡긴다.
- **확장자 정책:** 출력은 항상 소문자 `.jpg` (`.jpeg` 아님). 입력 PNG가 `.PNG`/`.Png`이어도 동일.
- **신규 패키지:** `internal/imageconv/` — 단일 함수 `ConvertPNGToJPG(srcPath, destPath string, quality int) error`. atomic write(`os.CreateTemp` 같은 디렉토리 → 인코드 → `os.Rename`) 포함. handler / upload 양쪽에서 호출.

### 2.8.1 자동 업로드 변환

`/api/upload` (§5)가 PNG를 받으면 디스크에 PNG로 저장하는 대신 JPEG로 변환해 저장한다. settings의 `auto_convert_png_to_jpg`(§2.7)가 `true`일 때만 작동.

- [ ] **트리거 조건:** multipart 파일의 base name 확장자가 `.png`(대소문자 무시) **AND** `h.settingsSnapshot().AutoConvertPNGToJPG == true`. 그 외 PNG는 변환하지 않고 원본 그대로 저장.
- [ ] **흐름:**
  1. multipart 스트림을 destDir 안 임시 파일에 저장: `os.CreateTemp(destDir, ".pngconvert-*.png")` — `.`-prefix라 browse 필터로 자동 숨김. 사용자에게 보일 최종 파일명을 점유하지 않는다.
  2. 임시 PNG → JPEG 변환(`imageconv.ConvertPNGToJPG`): 디코드 → 흰 배경 합성 → quality 90 인코드 → 두 번째 임시 파일(`.pngconvert-*.jpg`)에 atomic write.
  3. 첫 번째 임시 파일(원본 PNG) 삭제.
  4. 두 번째 임시 파일을 최종 경로로 atomic rename. 최종 파일명은 `<basename>.jpg` (확장자만 `.jpg`로 교체, base name 유지).
  5. 최종 경로 충돌 시 기존 `createUniqueFile` 패턴(O_CREATE|O_EXCL 재시도)으로 `<basename>_1.jpg`, `_2.jpg` 자동 suffix → 응답 `warnings`에 `"renamed"` 추가.
  6. 썸네일 풀 제출은 변환된 JPEG를 대상으로 (기존 동작과 동일, type=image).
- [ ] **변환 실패 시 폴백:** `imageconv.ConvertPNGToJPG`가 에러 반환(decode 실패, encode 실패, write 실패) → 두 번째 임시 파일 cleanup, **첫 번째 임시 PNG를 원래 흐름대로 최종 경로로 rename(.png 그대로 저장)**, 응답 `warnings`에 `"convert_failed"` 추가. 업로드 자체는 성공(201)으로 처리. 서버 로그(`slog.Warn`)에 사유 기록. **이유:** 사용자가 어찌됐든 파일은 받기를 기대한다 — 변환 실패가 업로드 실패로 변환되면 데이터 손실로 인식된다.
- [ ] **응답 스키마 변경:** 기존 `{path, name, size, type}`에 두 필드 추가 — 상세는 §5 `POST /api/upload`.
  - `converted: bool` — true면 PNG → JPG 변환됨, false면 원본 그대로 (자동 변환 OFF 또는 변환 실패 또는 PNG 아님).
  - `warnings: string[]` — `"renamed"`, `"convert_failed"` 누적.
- [ ] **설정 OFF 시:** PNG도 다른 파일처럼 원본 그대로 저장. `converted: false`, `warnings: []`. 응답 외 모든 동작은 기존 업로드와 동일 (multipart → `createUniqueFile`).
- [ ] **type 판별:** 변환 성공이든 실패든 응답 `type`은 최종 파일 기준 (`media.DetectType`). PNG 그대로면 `image`, JPG로 변환돼도 `image` — 변하지 않음.
- [ ] **설정 스냅샷:** 요청 시작 시점에 `h.settingsSnapshot()`로 값을 고정. 업로드 중간에 PATCH로 토글이 바뀌어도 진행 중 업로드는 원래 값 유지(§2.7 race-free 정책 일관).
- [ ] **동시성:** 업로드는 항상 unique 임시 파일 사용 → per-path lock 불필요. 디코드/인코드는 CPU bound이지만 단일 사용자 가정이라 별도 워커 풀 도입 안 함 (handler goroutine에서 직접 수행).

### 2.8.2 수동 변환 API

기존 PNG 파일을 명시적으로 JPEG로 변환. **SSE가 아닌 동기 JSON 응답** — PNG 변환은 일반 사진 크기에서 1초 내외이고, 폴더 일괄 변환(최대 500개)도 단일 사용자 운용에서 수 분 내 종료되어 progress 스트림이 가치보다 복잡도가 큼.

- **API:** `POST /api/convert-image` (§5에 상세).
- [ ] **개별 변환 트리거:** 이미지 카드가 PNG 파일이면 "JPG로 변환" 버튼 표시. 기존 rename/delete 버튼과 동일 레이아웃. 클릭 시 확인 모달 → 변환 시작.
- [ ] **일괄 변환 트리거:** 상단 툴바(§2.5.2와 공존)의 단일 버튼이 selection 상태에 따라 모드 전환 — 클릭 시 확인 모달 → 한 번의 요청으로 변환:
  - **선택 0개 + visible PNG ≥ 1개:** "모든 PNG 변환 (M개)" — 현재 **filter/sort 통과한 visible entries 중 PNG 전부**.
  - **선택 ≥ 1개이고 그중 PNG ≥ 1개:** "선택 PNG 변환 (N개)" — `selectedPaths` ∩ visible entries 중 PNG만 추려서 변환. 비-PNG는 자동으로 제외(차단/경고 없음).
  - **선택 ≥ 1개인데 PNG 0개 / 선택 0개 + visible PNG 0개:** 버튼 숨김.
  - **selection 정책:** 폴더는 `selectedPaths`에서 자동 제외(§2.1.3 기존 정책)이라 폴더가 섞일 수 없음. 다른 폴더로 이동 시 selection이 비워지는 동작도 기존 그대로.
- [ ] **변환 동작 (요청 1건당 file 단위 순차 처리):**
  1. `media.SafePath`로 입력 경로 검증 → traversal 차단.
  2. `os.Stat`으로 파일/디렉토리 구분, 확장자 `.png`(대소문자 무시) 화이트리스트.
  3. 목표 경로 `<basename>.jpg`가 같은 디렉토리에 이미 존재하면 **항목 결과를 `error: "already_exists"`로 마킹** — 자동 suffix **없음**(rename·TS→MP4 정책과 일관). 사용자가 기존 `.jpg` 처리 결정해야 함.
  4. `imageconv.ConvertPNGToJPG`로 임시 파일에 변환 → atomic rename으로 최종 경로 안착.
  5. `delete_original: true`이면 변환 성공 후 원본 PNG + `.thumb/{name}.png.jpg` 삭제. 사이드카 삭제 실패 시 결과의 `warnings`에 `"delete_original_failed"` 추가 (이미지에는 `.dur` 사이드카 없음 — §2.3.2 동영상 전용).
  6. 새 JPEG의 썸네일은 **별도 생성하지 않음** — 기존 lazy 메커니즘(§2.3.1)이 다음 `browse`에서 자동 생성 (TS→MP4와 동일 단순화).
- [ ] **파일명 결정:** `foo.png` → `foo.jpg` (base name 유지, 확장자만 `.jpg` 교체).
  - **대소문자:** 원본이 `.PNG`/`.Png` 등이어도 출력은 소문자 `.jpg` 고정.
  - **충돌 처리:** 위 3번. 자동 suffix **없음** (자동 업로드 변환과 다른 정책 — 수동은 사용자의 명시적 행위라 의도 추정 금지, rename 정책과 일관).
- [ ] **응답:** `200 OK`, 동기 JSON. 항목별 결과 배열. 항목 단위 성공/실패가 섞여도 HTTP 200 — TS→MP4 SSE의 batch summary와 동일한 정신. 상세는 §5 `POST /api/convert-image`.
- [ ] **타임아웃:** 파일당 30초 (`imageConvertFileTimeout` 상수). 초과 시 `error: "convert_timeout"`. 정상 PNG는 1초 내외라 도달 불가능한 안전장치.
- [ ] **취소:** 요청 context 취소(클라이언트 연결 끊김) 시 진행 중 변환은 중단되며 임시 파일은 cleanup. 동기 응답이라 클라이언트 입장에서는 fetch가 abort되는 형태 — 별도 cancel API 없음.
- [ ] **응답 후 UI 갱신:** 응답 수신 후 클라이언트가 `loadBrowse()` 1회 호출 → 새 `.jpg` + (delete_original 시) 원본 제거가 반영.

**Non-goals (자동·수동 공통):**
- JPEG 외 다른 출력 포맷 (WEBP, AVIF) — 범위 외.
- PNG 외 다른 입력 포맷 (BMP, TIFF, WEBP) — 범위 외.
- quality 사용자 조절 — 90 고정.
- 알파 채널 보존을 위한 PNG 우회 (예: 알파가 있는 PNG는 변환 거부) — 정책상 항상 흰 배경 합성.
- EXIF/메타데이터 보존.
- 변환 큐 / 잡 레지스트리 영속화 — 동기 요청이라 불필요.
- progress 이벤트 / SSE — 동기 응답으로 단순화.
- 동시 변환 (배열은 항상 순차 처리) — 단일 사용자 가정.
- URL import(§2.6) 결과의 자동 변환 — 다운로드와 변환 의도를 분리. 다운로드 받은 PNG는 수동 변환으로만 처리.

---

## 3. Tech Stack

| Layer | Choice | Reason |
|-------|--------|--------|
| Backend | Go (net/http stdlib) | 성능, 단일 바이너리 |
| Image processing | `github.com/disintegration/imaging` | 순수 Go, CGo 불필요. 썸네일 + PNG → JPG 변환(§2.8)에서 공유 |
| Transcoding | ffmpeg (alpine apk) | TS → MP4 실시간 트랜스코딩 |
| Frontend | Vanilla HTML + CSS + JS | 의존성 없음 |
| Container | Docker + Docker Compose | 배포 단순화 |
| Storage | Docker named volume → `/data` | 영속성 |

---

## 4. Project Structure

```
file_server/
├── cmd/
│   └── server/
│       └── main.go             # 진입점 — 설정 로드 + handler.Register + graceful shutdown
├── internal/
│   ├── handler/                # HTTP 엔드포인트 (각 파일이 라우트군 하나)
│   │   ├── handler.go          # Handler 구조체, Register, writeError, requireSameOrigin
│   │   ├── browse.go           # GET /api/browse — 디렉터리 조회
│   │   ├── tree.go             # GET /api/tree — 사이드바 트리
│   │   ├── files.go            # 업로드/리네임/삭제/폴더 CRUD
│   │   ├── stream.go           # Range 스트리밍 + TS 실시간 remux (.cache/streams/)
│   │   ├── thumb.go            # /api/thumb (lazy 생성 fallback 포함)
│   │   ├── import_url.go       # URL/HLS 다운로드 SSE 핸들러
│   │   ├── import_url_jobs.go  # /api/import-url/jobs* (목록/구독/취소/삭제)
│   │   ├── convert.go          # TS → MP4 영구 변환 SSE 핸들러
│   │   └── settings.go         # GET/PATCH /api/settings
│   ├── media/                  # 타입 판별, MIME, SafePath, MoveFile (최하위 레이어)
│   ├── thumb/                  # 이미지·동영상 섬네일 + duration 사이드카, 워커 풀
│   ├── urlfetch/               # HTTP 다운로드 + HLS remux (SSE 용 Callbacks hook)
│   ├── convert/                # TS → MP4 ffmpeg remux runner
│   ├── imageconv/              # PNG → JPG 변환 (§2.8) — disintegration/imaging 기반, 흰 배경 합성
│   ├── importjob/              # 잡 라이프사이클·이벤트 채널·Registry (인메모리)
│   └── settings/               # §2.7 URL import 설정 — JSON 영속화 + 스냅샷 getter
├── web/
│   ├── index.html
│   ├── style.css
│   └── app.js
├── Dockerfile
├── docker-compose.yml
└── SPEC.md
```

의존 방향: `cmd → handler → (importjob, urlfetch, convert, imageconv, thumb, settings, media) → media`. `media`는 최하위, 상향 의존 금지.

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
| PATCH | `/api/folder?path=` | 폴더 이름 변경 또는 이동 (body로 분기) |
| DELETE | `/api/folder?path=` | 폴더 재귀 삭제 (하위 내용 + `.thumb/` 포함) |
| POST | `/api/import-url?path=` | URL 목록에서 미디어 다운로드 → 저장 (SSE 진행 스트림) |
| POST | `/api/convert` | TS 파일 목록을 MP4로 영구 변환 (SSE 진행 스트림) |
| POST | `/api/convert-image` | PNG 파일 목록을 JPG로 영구 변환 (동기 JSON, §2.8) |
| GET | `/api/settings` | 현재 다운로드 설정 조회 (§2.7) |
| PATCH | `/api/settings` | 다운로드 설정 갱신 (§2.7) |
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
  "type": "video",
  "converted": false,
  "warnings": []
}
// 에러 400/500
{"error": "message"}
```
- `converted`: PNG → JPG 자동 변환(§2.8.1)이 수행되어 최종 파일이 원본과 다른 확장자가 된 경우 `true`. 그 외 항상 `false` (PNG 아님 / 자동 변환 OFF / 변환 실패 폴백).
- `warnings` 가능 값:
  - `"renamed"` — 자동 변환 후 목표 `.jpg` 충돌로 `_1`/`_2` 자동 suffix 부착 (§2.8.1).
  - `"convert_failed"` — PNG → JPG 변환 시도가 실패해 원본 PNG로 폴백 저장 (§2.8.1). 업로드 자체는 성공.

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
Body 형태로 두 동작 분기 (`PATCH /api/file`과 동일 패턴):
- `{"name": "..."}` → **이름 변경** (동일 부모 디렉토리 내)
- `{"to":   "..."}` → **이동** (다른 디렉토리로, base name 유지)

두 필드를 동시에 보내면 `400 {"error": "specify either name or to, not both"}`. 둘 다 없으면 `400 {"error": "missing name or to"}`.

##### 이름 변경 (`{"name": "..."}`)
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

##### 이동 (`{"to": "..."}`)
- 성공: `200 OK`
  ```json
  {
    "path": "/photos/2024/sub",
    "name": "sub"
  }
  ```
  (`name`은 원본의 base name 그대로, 이동만 수행)
- 미존재 (src): `404 {"error": "not found"}`
- src가 파일을 가리킴: `400 {"error": "not a directory"}`
- 루트 이동 시도: `400 {"error": "cannot move root"}`
- 대상 디렉토리 없음 또는 디렉토리 아님: `400 {"error": "invalid destination"}`
- 자기 자손으로 이동 시도 (`/a` → `/a/b`): `400 {"error": "invalid destination"}`
- 동일 부모 (이동 의미 없음): `400 {"error": "same directory"}`
- 동일 base name이 destDir에 이미 존재: `409 {"error": "already exists"}`
- cross-volume(`EXDEV`): `500 {"error": "cross_device"}` — 폴더 재귀 copy 폴백 없음
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
- **배치 단위 흐름**:
  1. `register` — 응답 헤더 직후 1회. `jobId`(영구 식별자)를 클라이언트에게 전달한다. 새로고침/다른 탭에서 이 jobId로 `GET /api/import-url/jobs/{id}/events`에 재구독 가능 (§2.6 잡 레지스트리)
  2. `queued` — 1회. 서버가 POST를 받아들였음을 알리는 시그널 — 다른 배치가 진행 중이면 후속 `start`가 지연될 수 있다 (§2.6 배치 직렬화). 세마포어가 비어있으면 acquire 직후 바로 `start`가 이어지므로 클라이언트는 보통 이 이벤트를 보지 못하지만, 큐잉이 발생하면 모든 URL row를 "대기 중 (순서 대기)" 상태로 표시한다.
  3. URL당 이벤트 (아래)
  4. `summary` — 배치의 성공/실패/취소 합계 1회로 종료
- **URL당 이벤트 흐름**:
  1. `start` — 다운로드 시작 (응답 헤더 검증 통과 직후)
  2. `progress` — 다운로드 진행 (0개 이상, throttled, §5.1.1)
  3. `done` 또는 `error` — 종료 (URL당 정확히 1개; 취소된 URL은 `error: "cancelled"`)

**이벤트 스키마**

```jsonc
// phase: "register" — 배치당 1회. POST 응답 첫 프레임에만 등장 (snapshot replay에는 미포함).
{"phase":"register","jobId":"imp_a3f8k2lm"}

// phase: "queued"  — 배치당 1회. 페이로드는 phase 필드뿐.
{"phase":"queued"}

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

// phase: "summary"  — `cancelled`는 0이면 omitempty
{"phase":"summary","succeeded":2,"failed":1,"cancelled":0}
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
  - `"download_timeout"` — `url_import_timeout_seconds`(§2.7) 초과
  - `"too_many_redirects"` — 5회 초과
  - `"private_network"` — loopback/private/link-local/multicast/unspecified IP 대상 차단
  - `"tls_error"` — TLS 인증서 검증 실패
  - `"http_error"` — 4xx/5xx 응답
  - `"unsupported_content_type"` — 허용 Content-Type 목록 밖
  - `"too_large"` — `url_import_max_bytes`(§2.7) 초과 — `Content-Length` 사전 검증 또는 런타임 누적 카운터
  - `"hls_playlist_too_large"` — HLS 플레이리스트 본문이 1 MiB 초과 (master 또는 variant) (§2.6.1)
  - `"hls_too_many_segments"` — media playlist의 `#EXTINF` segment 개수가 10,000 초과 (§2.6.1) — 정상 VOD에는 도달 불가, 악의적 폭주 1차 방어
  - `"ffmpeg_error"` — HLS 리먹싱 중 ffmpeg 프로세스 실패 (non-zero exit, 입력 스트림 문제)
  - `"ffmpeg_missing"` — ffmpeg 바이너리가 서버 PATH에 없음 (운영자 설치 필요) — `ffmpeg_error`와 구분됨
  - `"network_error"` — 기타 네트워크 실패
  - `"write_error"` — 디스크 저장 실패
  - `"cancelled"` — 명시적 cancel API 호출 또는 배치 cancel로 중단 (§2.6 취소)
- 4xx 케이스 (요청 자체 거부 — SSE 스트림 시작 전 일반 JSON 에러 응답):
  - `400 {"error": "invalid path"}` — path traversal
  - `400 {"error": "no urls"}` — 빈 배열
  - `400 {"error": "too many urls"}` — 한 번에 500개 초과
  - `404 {"error": "path not found"}` — 저장 디렉토리 미존재
  - `429 {"error": "too_many_jobs"}` — 활성 잡 수가 `MaxQueuedJobs=100` 초과 (§2.6 활성 잡 cap)

#### GET /api/import-url/jobs
- 응답: `200 OK`, `Content-Type: application/json`
- Body:
  ```json
  {
    "active":   [/* JobSnapshot, ... */],
    "finished": [/* JobSnapshot, ... */]
  }
  ```
  - 두 배열 모두 `createdAt` asc 정렬
- `JobSnapshot`:
  ```jsonc
  {
    "id":        "imp_a3f8k2lm",
    "destPath":  "movies/2026",      // dataDir-relative slash 경로
    "status":    "running",          // queued | running | completed | failed | cancelled
    "createdAt": "2026-04-25T12:00:00Z",
    "urls": [
      {
        "url":      "https://...",
        "name":     "foo.mp4",       // 알려진 후에만 채워짐
        "type":     "video",         // image | video | audio
        "status":   "running",       // pending | running | done | error | cancelled
        "received": 12345,
        "total":    67890,           // 알 수 없으면 omitempty
        "warnings": [],
        "error":    ""               // status=error/cancelled에서만 채워짐
      }
    ],
    "summary": { "succeeded": 1, "failed": 0, "cancelled": 0 }  // 종료 상태 진입 시에만
  }
  ```

#### GET /api/import-url/jobs/{id}/events
- 응답: `200 OK`, `Content-Type: text/event-stream`
- 첫 프레임: snapshot envelope
  ```jsonc
  {"phase":"snapshot","job": <JobSnapshot>}
  ```
- 이후 라이브 라이프사이클 이벤트 (`queued` / `start` / `progress` / `done` / `error` / `summary`). `register`는 POST 응답 전용이라 여기에 등장하지 않음.
- 잡이 이미 종료 상태면 snapshot 1회 후 connection close. 종료 상태로 전이된 활성 잡도 동일 — `SetStatus(terminal)`이 subscriber 채널을 close하면 핸들러는 `summary` 또는 channel-closed 중 먼저 도달한 쪽을 보고 리턴.
- 미존재 ID → `404 {"error":"job not found"}`

#### POST /api/import-url/jobs/{id}/cancel
- 쿼리:
  - 미지정 → 잡 전체 cancel
  - `?index=N` → URL N만 cancel (잡 진행은 다음 URL부터 계속)
- 응답: `204 No Content`
- 동작: §2.6 *취소* 항목 참고
- 이미 종료된 잡 또는 URL → `409 {"error":"job already finished"}` / `409 {"error":"url already finished"}`
- 잘못된 index (비숫자, 음수, ≥ urls 길이) → `400`
- 미존재 ID → `404`

#### DELETE /api/import-url/jobs/{id}
- 응답: `204 No Content`
- 활성 잡(`queued`/`running`) → `409 {"error":"job_active"}` (먼저 cancel 필요)
- 종료된 잡 → history에서 제거. SetStatus(terminal) 시점에 subscriber 채널은 이미 close되어 있어 broadcast 불필요.
- 미존재 ID → `404`

#### DELETE /api/import-url/jobs?status=finished
- 쿼리 `status=finished` 필수 (누락 시 `400 {"error":"missing status=finished filter"}`) — 의도하지 않은 "wipe everything" 방지
- 응답: `200 OK`, `{"removed": <int>}`
- 종료된 잡만 일괄 제거. 활성 잡은 영향 없음.

##### 5.1.1 Progress 이벤트 throttling
- `progress` 이벤트는 **수신 바이트 1 MiB마다** 또는 **250 ms마다** 중 먼저 도달하는 시점에 방출 (양쪽 모두 ticker/카운터 기반)
- 동일 값으로 `received`가 변하지 않으면 방출 생략 (중복 제거)
- 파일이 작아 `progress` 없이 `start` → `done` 바로 가는 케이스 허용
- 최종 바이트 수는 항상 `done` 이벤트 `size` 필드로 전달 (`progress`의 마지막 값은 신뢰하지 말 것)

#### POST /api/convert

TS 파일을 MP4로 영구 변환. SSE 스트림으로 진행 상태 전송. 이벤트 스키마·throttling은 URL import(§5.1, §5.1.1)와 동일 — `phase`: `start` / `progress` / `done` / `error` / `summary`.

- **Body:**
  ```json
  {
    "paths": ["movies/clip1.ts", "movies/clip2.ts"],
    "delete_original": false
  }
  ```
  - `paths`: 변환할 `.ts` 파일 경로 배열(`/data` 기준 상대). 최소 1개, 최대 **500개** (URL import와 동일 상한).
  - `delete_original`: 변환 성공 시 원본 `.ts` + 사이드카 삭제 여부(기본 `false`).
- **응답:** `200 OK`, `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`

**이벤트 스키마**

```jsonc
// phase: "start"
{"phase":"start","index":0,"path":"movies/clip1.ts",
 "name":"clip1.mp4","total":314572800,"type":"video"}

// phase: "progress"
{"phase":"progress","index":0,"received":67108864}

// phase: "done"
{"phase":"done","index":0,"path":"movies/clip1.mp4",
 "name":"clip1.mp4","size":310378496,"type":"video","warnings":[]}

// phase: "error"
{"phase":"error","index":1,"path":"movies/clip2.ts",
 "error":"already_exists"}

// phase: "summary"
{"phase":"summary","succeeded":1,"failed":1}
```

- `start.total`은 원본 `.ts` 파일 크기(최종 MP4와 근사, 정확치 아님)
- `progress.received`는 임시 `.mp4` 출력 파일의 현재 바이트 수(ffmpeg remux 중 stat 폴링)
- `done.size`는 최종 MP4 파일 크기 (atomic rename 직후 `Stat`)
- `warnings` 가능 값:
  - `"delete_original_failed"` — `delete_original: true`였으나 원본 `.ts`(또는 사이드카) 삭제 실패. 변환 자체는 성공.
- `error` 가능 값:
  - `"invalid_path"` — path traversal 또는 `/data` 밖 경로
  - `"not_found"` — 경로에 파일이 없음
  - `"not_a_file"` — 경로가 디렉토리
  - `"not_ts"` — 파일 확장자가 `.ts`가 아님(대소문자 무시)
  - `"already_exists"` — 목표 `foo.mp4`가 이미 존재
  - `"ffmpeg_missing"` — ffmpeg 바이너리가 서버 PATH에 없음
  - `"ffmpeg_error"` — ffmpeg non-zero exit(입력 손상, 비호환 코덱 등)
  - `"convert_timeout"` — 10분 초과
  - `"write_error"` — 디스크 저장 실패(디스크 풀 등)
  - `"canceled"` — 클라이언트 연결 끊김/요청 context 취소
- **4xx 케이스** (SSE 스트림 시작 전 일반 JSON 에러 응답):
  - `400 {"error": "no paths"}` — 빈 배열
  - `400 {"error": "too many paths"}` — 500개 초과
  - `400 {"error": "invalid request"}` — JSON 파싱 실패
  - `405 {"error": "method not allowed"}` — POST 외

#### POST /api/convert-image

PNG 파일을 JPG로 영구 변환 (§2.8.2). **동기 JSON 응답** — SSE가 아니다. PNG 변환은 빠르고 (정상 사진 1초 내외), 일괄 500개도 단일 사용자 운용에서 수 분 내 종료되어 progress 스트림이 가치보다 복잡도가 큼. 항목별 결과는 응답 배열에 한꺼번에 포함된다.

- **Body:**
  ```json
  {
    "paths": ["movies/foo.png", "movies/bar.png"],
    "delete_original": false
  }
  ```
  - `paths`: 변환할 `.png` 파일 경로 배열(`/data` 기준 상대). 최소 1개, 최대 **500개** (URL import / convert와 동일 상한).
  - `delete_original`: 변환 성공 시 원본 `.png` + 사이드카(`.thumb/{name}.png.jpg`) 삭제 여부 (기본 `false`).
- **응답:** `200 OK`, `Content-Type: application/json`. 항목별 성공/실패가 섞여도 200 — 항목별 결과 객체로 표현.

  ```json
  {
    "succeeded": 1,
    "failed": 1,
    "results": [
      {
        "index": 0,
        "path": "movies/foo.png",
        "output": "movies/foo.jpg",
        "name": "foo.jpg",
        "size": 234567,
        "warnings": []
      },
      {
        "index": 1,
        "path": "movies/bar.png",
        "error": "already_exists"
      }
    ]
  }
  ```
- `results[i]`: 성공이면 `output` / `name` / `size` / `warnings` 채움, 실패면 `error`만 채움 (상호 배타).
- `warnings` 가능 값:
  - `"delete_original_failed"` — `delete_original: true`였으나 원본 PNG 또는 `.thumb/` 사이드카 삭제 실패. 변환 자체는 성공.
- `error` 가능 값:
  - `"invalid_path"` — path traversal 또는 `/data` 밖 경로
  - `"not_found"` — 경로에 파일이 없음
  - `"not_a_file"` — 경로가 디렉토리
  - `"not_png"` — 파일 확장자가 `.png`가 아님(대소문자 무시)
  - `"already_exists"` — 목표 `foo.jpg`가 이미 존재 (자동 suffix 없음)
  - `"image_too_large"` — 헤더의 width × height가 cap(64M 픽셀, ≈ 8K×8K) 초과 — 메모리 폭주 방어로 디코드 전 거부
  - `"decode_failed"` — PNG 디코드 실패 (손상/비표준)
  - `"encode_failed"` — JPEG 인코드 실패
  - `"write_failed"` — 디스크 저장 실패 (디스크 풀 등)
  - `"convert_timeout"` — 30초 초과 (정상 PNG에서는 도달 불가능한 안전장치)
  - `"canceled"` — 클라이언트 연결 끊김/요청 context 취소
- **4xx 케이스** (응답 시작 전 일반 JSON 에러):
  - `400 {"error": "no paths"}` — 빈 배열
  - `400 {"error": "too many paths"}` — 500개 초과
  - `400 {"error": "invalid request"}` — JSON 파싱 실패
  - `405 {"error": "method not allowed"}` — POST 외

#### GET /api/settings
- 성공: `200 OK`, `Content-Type: application/json`
- 응답: 현재 메모리 캐시된 설정 값 그대로 (§2.7 형식)
  ```json
  {
    "url_import_max_bytes": 10737418240,
    "url_import_timeout_seconds": 1800,
    "auto_convert_png_to_jpg": true
  }
  ```

#### PATCH /api/settings
- 요청: `Content-Type: application/json`, 위 응답과 동일한 스키마(세 필드 모두 필수)
- 성공: `200 OK` + 갱신된 값 반환 (디스크 쓰기 + 메모리 캐시 갱신 후)
- 실패:
  - `400 {"error": "invalid request"}` — JSON 파싱 실패 / 필드 누락 / 타입 불일치(boolean 자리에 다른 타입 포함)
  - `400 {"error": "out_of_range", "field": "url_import_max_bytes"}` — 1 MiB ~ 1 TiB 경계 밖
  - `400 {"error": "out_of_range", "field": "url_import_timeout_seconds"}` — 60 ~ 14400 경계 밖
  - `500 {"error": "write_failed"}` — settings.json 쓰기 실패(디스크 풀, 권한 등) — 메모리 캐시는 변경하지 않음

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

## 5.3 Same-origin / CSRF 보호

LAN 단일 사용자 모델에는 인증이 없으므로, 다른 오리진의 페이지가 사용자 브라우저를 통해 본 서버로 mutating 요청을 보내는 시나리오는 **요청 자체의 진위(authenticity)** 로만 막는다. `internal/handler/handler.go`의 `requireSameOrigin` 미들웨어가 모든 mutating 라우트(POST·PATCH·DELETE·PUT)에 걸려 있다. GET·HEAD·SSE 구독 (`EventSource`) 은 통과 — 읽기 전용이며 EventSource는 `Origin` 헤더를 일관되게 전송하지 않기 때문.

검사 규칙:

1. `Origin` 헤더가 있으면 `url.Parse(Origin).Host == r.Host` 일 때만 허용.
2. `Origin` 헤더가 없으면 `Sec-Fetch-Site` 폴백을 **allowlist** 로 검사:
   - `""` (curl·서버사이드·pre-2020 브라우저), `"same-origin"`, `"none"` (사용자 직접 입력 URL) → 허용.
   - `"same-site"` (같은 eTLD+1의 다른 서브도메인), `"cross-site"`, `"cross-origin"`, 미지의 미래 값 → 거부 (fail-closed).
3. 거부 시 `403 {"error": "cross_origin"}`.

차단되는 시나리오:

- 다른 오리진의 페이지가 `<form action="http://server/api/...">` 또는 `fetch(...)` 로 mutating 요청 송출.
- 같은 eTLD+1의 다른 서브도메인 페이지(브라우저는 `Sec-Fetch-Site: same-site` 송출).

허용되는 시나리오:

- 같은 오리진의 본 서버 프론트엔드 (`Origin == Host`).
- `curl` 등 헤더를 보내지 않는 도구 (LAN 내부 운영 시나리오 — `Origin` 없음 + `Sec-Fetch-Site` 없음).
- 사용자가 주소창에 URL을 직접 입력해 발생한 GET 페이지 로드 (`Sec-Fetch-Site: none`).

> **Note:** 본 정책은 *오리진의 진위* 만 검사하며 IP·네트워크 검증은 하지 않는다. SSRF 정책은 §2.6 "약한 SSRF" 규칙을 따른다.

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
- 섬네일: `/data/media/**/.thumb/`
- 다운로드 설정: `/data/.config/settings.json`
- 실시간 remux 캐시: `/data/.cache/streams/`

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
  - 현재 설정(`url_import_max_bytes`) 초과 Content-Length 사전 거부 — 테스트는 작은 값(예: 1 KiB)으로 cap 주입 후 검증
  - `Content-Length` 헤더 부재 + 본문이 cap 이내 → 정상 완료
  - `Content-Length` 헤더 부재 + 본문이 cap 초과 → 런타임 카운터가 `too_large` 반환 + 임시 파일 정리
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
  - `PATCH /api/folder` (rename) 성공 시 하위 내용(`.thumb/` 포함)이 새 경로에 그대로 존재 확인
  - Path traversal 방지 (`name`에 `/`·`\\` 포함 시 400)
- 단위 테스트 (`media.MoveDir` — 신규):
  - 정상 이동: `srcDir`이 `destDir/<basename>`으로 옮겨지고 하위 파일·`.thumb/`가 모두 따라감
  - 충돌: `destDir`에 동일 base name이 이미 존재(파일이든 폴더든) → `ErrDestExists`
  - 자기 자신 이동: `destDir == srcDir` → `ErrCircular`
  - 자기 자손 이동: `destDir`이 `srcDir`의 자손 → `ErrCircular`
  - prefix 가짜양성 방지: `/a/bc`로 이동 시 `/a/b`의 자손으로 오판하지 않음
  - cross-volume 시뮬레이션(EXDEV 모킹) → `ErrCrossDevice` (재귀 copy 폴백 없음 확인)
- 통합 테스트 (`PATCH /api/folder` 이동 분기):
  - 정상 이동 → 200 + `{path, name}`. 새 위치에 파일/.thumb/하위폴더 모두 존재 확인
  - body가 `{name, to}` 동시 → 400 `specify either name or to, not both`
  - body가 `{}` → 400 `missing name or to`
  - 루트 이동 시도 (path=`/` 또는 빈 문자열) → 400 `cannot move root`
  - destDir 미존재/파일 가리킴 → 400 `invalid destination`
  - 자기 자손 destDir → 400 `invalid destination`
  - 동일 부모 destDir → 400 `same directory`
  - 충돌 → 409 `already exists`
  - traversal (`to`에 `..` 등) → 400 `invalid path`
- 통합 테스트 (`DELETE /api/folder` UI 진입점, 이미 백엔드 통과):
  - 사이드바 트리 노드의 🗑 클릭 → `confirm()` accept → `DELETE /api/folder` → 트리·browse 재조회 (수동/E2E)
- 수동 테스트 (DnD 폴더 이동):
  - 사이드바 트리 노드 A → 사이드바 트리 노드 B 위로 드래그 → 이동 후 B 아래 A 표시, A의 currentPath이면 자동 navigate
  - 사이드바 트리 노드 → breadcrumb 다른 경로 위로 드래그 → 동일 동작
  - 메인 리스트 표의 폴더 행 → 사이드바 트리 노드 위로 드래그 → 동작 확인
  - 자기 자손 destDir로 드래그 시 drop 거부 시각 피드백 (`dropEffect: 'none'`)
- 수동 테스트 (새 폴더 버튼 위치):
  - 사이드바 헤더의 "새 폴더" 클릭 → currentPath 기준 생성 모달 → 성공 시 트리 + 메인 리스트 동시 갱신
  - 메인 툴바에서 기존 "새 폴더" 버튼이 사라졌는지 확인
- 통합 테스트 (URL import): `httptest.Server`로 모의 origin 띄워서 검증 (SSE 응답 파싱)
  - 정상 이미지 다운로드 → `start` → `done` 이벤트, 파일 저장 확인
  - 정상 MP4 동영상 다운로드 → `type: "video"` 반환 + `.thumb/` 생성
  - 정상 MP3 음악 다운로드 → `type: "audio"` 반환 + 섬네일 생성 안 함
  - `Content-Length` 누락 + 본문이 cap 이내 → 정상 `done`
  - Content-Length가 설정 cap + 1 인 응답 → `error: "too_large"` (사전 거부)
  - Content-Length 누락 + 본문이 설정 cap 초과 → `error: "too_large"` (런타임) + 임시 파일 정리 확인
  - `Content-Type: text/html` → `error: "unsupported_content_type"` 이벤트
  - HTTP URL → `done` + `warnings: ["insecure_http"]`
  - private network URL(`127.0.0.1`, `192.168.0.0/16` 등) → `error: "private_network"`
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
- 단위 테스트 (settings §2.7):
  - JSON read/write round-trip (atomic write temp+rename 검증)
  - 파일 부재 → 기본값 반환(디스크 쓰기 없음)
  - 파일 손상(JSON 파싱 실패) → 경고 로그 + 기본값 반환
  - 경계 위반 값이 디스크에 존재 → 경고 로그 + 기본값 반환
  - PATCH 경계 검증: max_bytes `0`, `1048575`(1 MiB-1), `1099511627777`(1 TiB+1) → `out_of_range`; timeout `59`, `14401` → `out_of_range`
  - PATCH 성공 시 메모리 캐시가 즉시 갱신되어 다음 URL import에 반영
- 통합 테스트 (settings): `GET /api/settings` → 기본값 반환, `PATCH /api/settings`로 cap 축소 후 같은 핸들러에 URL import 요청 → 새 cap 적용된 `too_large` 관측
- 수동 테스트 (settings): 헤더 ⚙ → 모달 열기, 크기/타임아웃 편집, GiB helper text 확인, 저장 후 재로드 시 값 유지, 범위 밖 입력 시 에러 메시지 표시
- 단위 테스트 (TS→MP4 변환):
  - 경로/확장자 검증: `.ts` 외 확장자 거부(`not_ts`), 대소문자(`.TS`, `.Ts`) 허용, 디렉토리 경로 거부, path traversal 거부
  - 목표 파일명 계산: `foo.ts` → `foo.mp4`, `foo.TS` → `foo.mp4`(소문자 확장자 고정)
  - 충돌 감지: `foo.mp4` 사전 존재 시 `already_exists` 반환(ffmpeg 호출 전)
- 통합 테스트 (TS→MP4 변환, `httptest.NewRecorder` + 실제 ffmpeg):
  - 정상 TS 1개 변환 → `start` → `progress`(≥0개) → `done` → `summary` 이벤트, `.mp4` 파일 생성 + 원본 `.ts` 유지 확인
  - `delete_original: true` → 변환 성공 후 원본 `.ts` + `.thumb/{name}.ts.jpg` + `.ts.jpg.dur` 삭제 확인
  - 배열 2개 순차 변환 → index 0 → index 1 순서, 각각 `done` 1개씩, 마지막 `summary`에 `succeeded: 2`
  - 409 충돌: 목표 `foo.mp4` 사전 존재 시 `error: "already_exists"` 이벤트 + 임시 파일 미생성 확인
  - 부분 실패: 2개 중 1개는 `.ts` 아님 → 해당 index만 `error: "not_ts"`, 나머지는 정상 `done`
  - 취소: 변환 중 context 취소 → ffmpeg kill + 임시 파일 `.convert-*.mp4` 정리 확인
  - ffmpeg 미설치 환경: `ffmpeg_missing` 이벤트(PATH lookup 실패)
  - 손상 TS(헤더 truncate 등): `ffmpeg_error` + stderr는 SSE 응답에 노출되지 않음(서버 로그에만)
  - `delete_original_failed` 경고: 원본 `.ts`를 read-only로 설정 후 `delete_original: true` → `done.warnings: ["delete_original_failed"]`
  - 4xx: 빈 배열 → `400 "no paths"`, 501개 → `400 "too many paths"`, 유효하지 않은 JSON → `400 "invalid request"`
- 수동 테스트: TS 동영상 카드에 변환 버튼 → 변환 모달 → 진행 바 → 완료 후 `.mp4`로 재생(seek 동작 확인). 폴더에 TS 3개 있을 때 "모든 TS 변환" 버튼 → 순차 변환 → 성공/실패 요약 확인.
- 단위 테스트 (settings §2.7) — `auto_convert_png_to_jpg` 추가:
  - PATCH `auto_convert_png_to_jpg` 토글: `true→false`, `false→true` 모두 디스크 + 메모리 캐시 동기 반영
  - boolean 자리에 string/number 등 잘못된 타입 → `invalid request`
  - 디스크 settings.json에 `auto_convert_png_to_jpg` 키 누락 → 기본값 `true` 폴백
- 단위 테스트 (PNG → JPG 변환, `internal/imageconv`):
  - 정상 변환: 알파 없는 RGB PNG → 디코드 가능한 JPEG 생성, `imaging` 라이브러리로 다시 디코드해 dimensions 일치 확인
  - 알파 채널 합성: RGBA PNG (반투명/완전투명 픽셀 포함) → 흰 배경 합성 후 JPEG 인코드, 알파였던 픽셀 위치가 흰색에 가깝게 합성됐는지 샘플 검사
  - 경로 검증: src 미존재 → `os.ErrNotExist` 계열 오류 전파, src가 디렉토리 → 명확한 오류
  - 손상 PNG (truncated / 잘못된 magic) → decode 단계에서 오류 (handler에서 `decode_failed`로 매핑)
  - atomic write: `os.CreateTemp` → `os.Rename` 패턴 검증, ConvertPNGToJPG 중간 실패 시 임시 파일이 destDir에 남지 않음
  - 출력 확장자 정규화: 입력이 `.PNG`/`.Png`이어도 ConvertPNGToJPG는 `destPath`를 그대로 사용 (handler가 소문자 `.jpg`를 결정)
- 통합 테스트 (`POST /api/convert-image`, `httptest.NewRecorder`):
  - 정상 PNG 1개 변환 → `200`, `succeeded:1`, `.jpg` 파일 생성 + 원본 `.png` 유지 확인 (`delete_original:false`)
  - `delete_original:true` → 변환 성공 후 원본 `.png` + `.thumb/{name}.png.jpg` 삭제 확인
  - 배열 2개 변환 → `results` 배열 길이 2, 각각 index 0 / 1, 두 `.jpg` 모두 디스크에 존재
  - 충돌: 목표 `foo.jpg` 사전 존재 → 해당 항목 `error: "already_exists"` + 임시 파일 미생성 + 원본 `.png` 무영향
  - 부분 실패: 2개 중 1개는 `.png` 아님 (예: `.txt`) → 해당 index만 `error: "not_png"`, 나머지는 정상 `done`
  - `delete_original_failed` 경고: 원본 PNG 또는 사이드카를 read-only 디렉토리에 두고 `delete_original:true` → 결과 항목의 `warnings: ["delete_original_failed"]`, 변환 자체는 성공
  - 4xx: 빈 배열 → `400 "no paths"`, 501개 → `400 "too many paths"`, 잘못된 JSON → `400 "invalid request"`, GET → `405 "method not allowed"`
  - traversal: `paths: ["../../etc/passwd"]` → 항목 `error: "invalid_path"`
  - 손상 PNG: 헤더 truncate된 PNG → 항목 `error: "decode_failed"`, 임시 파일 정리 확인
- 통합 테스트 (`POST /api/upload` 자동 변환, §2.8.1):
  - settings `auto_convert_png_to_jpg:true` 상태에서 PNG 업로드 → 응답 `name: "*.jpg"`, `converted:true`, `warnings:[]`, 디스크에 `.jpg`만 존재 (원본 `.png` 미저장, `.pngconvert-*` 임시 파일도 정리됨)
  - settings `false` 상태에서 PNG 업로드 → 원본 `.png` 그대로 저장, `converted:false`, `warnings:[]`
  - 변환 실패 폴백: decode 실패하는 손상 PNG 업로드 → 원본 PNG 저장 + `warnings: ["convert_failed"]` + `converted:false`, 응답 코드는 여전히 `201`
  - 충돌 자동 suffix: `foo.jpg` 사전 존재 + `foo.png` 업로드(자동 변환 ON) → 결과 파일명 `foo_1.jpg` + `warnings: ["renamed"]` + `converted:true`
  - 비-PNG 업로드(JPG/MP4 등)는 자동 변환 영향 없음 — `converted:false`, `warnings:[]`, 기존 동작 그대로
  - 알파 채널 RGBA PNG 업로드 → `.jpg` 생성 + 알파 위치가 흰색으로 합성됐는지 샘플 검사 (다시 디코드)
- 수동 테스트 (PNG → JPG 변환):
  - 이미지 카드 "JPG로 변환" 버튼 → 모달 확인 → 성공 후 `loadBrowse()`로 카드가 `.jpg`로 갱신됨 확인
  - 폴더에 PNG 3개 + 다른 파일 → "모든 PNG 변환 (3)" 버튼 → 한 번의 요청 → 결과 알림(성공/실패 카운트) 표시
  - PNG 5개 중 2개 + 비-PNG 1개를 체크박스 선택 → 툴바 버튼이 "선택 PNG 변환 (2)"로 즉시 전환 → 모달 파일 목록에 PNG 2개만 노출 → 변환 후 두 파일만 `.jpg`로 갱신, 나머지(선택 안 한 PNG 3개 + 비-PNG 1개) 무영향
  - settings 모달의 "PNG 자동 변환" 체크박스 OFF → PNG 업로드 시 원본 PNG 그대로 표시되는지 확인, 토글 다시 ON → 다음 PNG 업로드부터 `.jpg`로 저장되는지 확인
  - 알파 PNG 자동 변환 결과를 다른 뷰어에서 열어 흰 배경 합성 확인

---

## 9. Boundaries

**항상 할 것 (Always)**
- Range 요청 지원 (스트리밍 seek 필수)
- 업로드 파일은 `/data` 볼륨 내부에만 저장 (path traversal 방지)
- 섬네일은 비동기로 생성 (업로드 응답 차단 안 함)
- Rename 시 `media.SafePath`로 원본·대상 경로 모두 검증 (path traversal 방지)
- Rename은 동일 부모 디렉토리 내에서만 허용 (경로 이동 금지 — 이동은 별도 PATCH body)
- File rename은 `os.Link` + `os.Remove`로 atomic EEXIST 보장 (TOCTOU 방지)
- 폴더 이동 (§2.1.2): 원본·대상 모두 `media.SafePath`로 검증, 자기 자신 또는 자손으로의 이동을 `filepath.Clean` + path-separator 경계 검사로 거부, 동일 부모는 거부, 대상 충돌은 자동 suffix 없이 409 반환, 단일 `os.Rename`으로 폴더 + `.thumb/` + 하위 모두 원자 이동, EXDEV는 재귀 copy 폴백 없이 500
- URL import: HTTPS TLS 인증서 검증, 요청 시작 시점에 설정 스냅샷(§2.7)을 찍어 사용, `Content-Length` 있으면 사전 검증 + 런타임 누적 카운터로 이중 방어(설정값 `url_import_max_bytes` 초과 시 중단), Content-Type 허용 목록(image/video/audio) 검증, 임시 파일 → atomic rename, 파일명 sanitize, SSE `Cache-Control: no-cache` 및 즉시 Flush
- HLS import: ffmpeg는 항상 검증된 local 파일만 입력으로 받는다 — master/variant playlist 본문, segment, key, init segment를 모두 Go 보호 클라이언트(`publicOnlyDialContext`)가 사전 다운로드한 뒤 임시 디렉터리(`<destDir>/.urlimport-hls-<random>/`)에 두고 URI를 local 상대 경로로 재작성한 playlist를 ffmpeg에 전달, ffmpeg `-protocol_whitelist "file,crypto"` 강제(네트워크 protocol 모두 차단 — DNS rebinding 우회 차단의 핵심), `-allowed_extensions ALL`은 local 파일 입력에만 영향, master playlist의 variant URL 스킴도 `http`/`https`만 허용(이중 검증), variant가 master 자기자신으로 resolve되면 media playlist로 fallback(loop 방지), media playlist segment 개수 cap 10,000(`hls_too_many_segments`), key 64 KiB / init 16 MiB per-resource cap, 누적 cap은 `url_import_max_bytes`(§2.7) 단일 카운터를 segment 다운로드와 ffmpeg 출력이 공유, ffmpeg 프로세스는 ctx cancel로 종료(외부 cancel·timeout·size cap 모두 동일 경로), 임시 디렉터리는 `defer os.RemoveAll`로 모든 경로(성공/실패/취소/패닉)에서 cleanup, 출력 MP4는 기존 `renameUnique` 경로로 atomic rename, 실패 시 stderr는 서버 로그에만 기록(SSE 클라이언트로는 안전한 code만 노출)
- Settings: PATCH 시 두 필드 모두 경계 검증 후 atomic write (temp + rename), 저장 실패 시 메모리 캐시는 변경하지 않음 (디스크-메모리 drift 방지), 진행 중인 URL 요청은 시작 시점 스냅샷 값을 끝까지 유지 (race-free)
- TS→MP4 변환: 입력·출력 경로 모두 `media.SafePath`로 검증, 확장자 `.ts` 화이트리스트 검증(대소문자 무시), 목표 `.mp4` 사전 존재 시 ffmpeg 호출 전 거부, ffmpeg argv 전달(쉘 미개입), 임시 파일 `.convert-*.mp4` → atomic rename, context 취소·타임아웃 시 ffmpeg kill + 임시 파일 정리, stderr는 서버 로그에만 기록(SSE에는 `ffmpeg_error` 코드만), 동일 소스 경로에 대한 동시 요청은 `stream.go`의 per-path 뮤텍스와 동일 패턴으로 직렬화
- PNG→JPG 변환: 입력·출력 경로 모두 `media.SafePath`로 검증, 입력 확장자 `.png` 화이트리스트(대소문자 무시), 출력 확장자는 항상 소문자 `.jpg`, 알파 채널은 흰색 배경에 합성(JPEG 구조적 한계 처리), JPEG quality 90 고정, 임시 파일(`.pngconvert-*.png`/`.jpg`, `.imageconv-*`) → atomic rename, 자동 업로드 변환은 settings 스냅샷을 요청 시작 시점에 고정(토글 race-free), 자동 변환 실패 시 원본 PNG로 폴백 저장(업로드 성공 유지 + `convert_failed` warning), 수동 변환은 목표 `.jpg` 사전 존재 시 거부(자동 suffix 없음), 디코드/인코드 실패는 코드 오류로만 노출(스택 트레이스나 내부 경로 비공개)

**하지 않을 것 (Never)**
- TS 이외 포맷 트랜스코딩 (MP4/MKV/AVI는 원본 그대로 스트리밍)
- 사용자 인증/권한 관리
- 외부 CDN이나 클라우드 스토리지 연동
- Rename 시 확장자 변경 허용 (MIME/타입 감지 일관성 유지)
- Rename 시 자동 suffix 부여 (`_1`, `_2` 등) — 충돌은 항상 409로 거부
- 폴더 이동 시 자동 suffix 부여 — 충돌은 항상 409로 거부 (rename과 일관)
- 폴더 이동 시 cross-volume 재귀 copy 폴백 — EXDEV는 500으로 즉시 거부 (단일 데이터 볼륨 전제)
- 폴더 이동 시 동시에 이름 변경 — `{"to"}`와 `{"name"}` body 동시 지정은 400 (한 호출에 하나의 의도)
- 다중 폴더 이동 — 폴더는 multi-select 대상이 아니며, DnD payload는 항상 단건 폴더만 운반
- URL import: `Authorization`/쿠키 등 인증 헤더 자동 첨부, `http`/`https` 외 스킴 허용, 설정값 `url_import_max_bytes` 초과 다운로드, 허용 목록 밖 Content-Type 저장, 동시 다운로드(batch는 순차 처리)
- Settings: 인증/권한 검사(single-tenant 전제), 경계 밖 값 저장, 진행 중인 요청에 새 값 반영(스냅샷 정책), 설정을 핸들러별로 분기(URL import와 HLS는 반드시 동일 값 공유)
- HLS import: 재인코딩(`-c copy`로 리먹싱만, CPU 폭주 방지), DASH(`.mpd`) 지원, 원본 `.m3u8` + `.ts` 세그먼트를 그대로 저장, DRM/암호화 세그먼트 우회 시도, live stream 특별 처리(엔드리스 스트림은 공통 timeout/size 상한으로만 차단)
- TS→MP4 변환: 재인코딩 폴백(remux 실패는 `ffmpeg_error` 반환), `.ts` 외 확장자 변환(범위 외), 다른 포맷 간 변환(MKV↔MP4 등), 원본 `.ts` 덮어쓰기(목표 `.mp4` 충돌 시 항상 409 — 자동 suffix 없음), 동시 ffmpeg 프로세스 실행(배열은 순차 처리), 변환 큐 영속화(서버 재시작 시 진행 중 변환 폐기)
- PNG→JPG 변환: PNG 외 입력 포맷 변환(BMP/TIFF/WEBP/HEIC 등 — 범위 외), JPG 외 출력 포맷(WEBP/AVIF — 범위 외), JPEG quality 사용자 조절(90 고정), 알파 채널 보존(흰 배경 합성 강제 — 알파가 필요한 케이스는 변환 회피해야 함), EXIF/메타데이터 보존, 수동 변환의 자동 suffix(`_1`/`_2` — 충돌은 항상 409 로 거부), URL import(§2.6) 결과의 자동 변환(다운로드와 변환 의도를 분리 — 수동 변환으로만), 자동 변환 시 원본 PNG도 별도 저장(변환 성공 시 원본은 디스크에 남기지 않음 — 사용자가 원본을 원하면 자동 변환을 OFF), SSE/progress 스트림(동기 응답으로 단순화), 동시 변환(배열은 항상 순차 처리)

**Known limitations**
- Folder rename은 `os.Stat` + `os.Rename` 순서로, 두 콜 사이에 동일 이름 폴더가 생성되면 race 발생 가능. 단일 사용자 배포 대상이므로 acceptable.
- Folder move도 같은 stat-then-rename 패턴이며 동일 race 가정 — 단일 사용자 배포 대상이므로 acceptable.
- 폴더 이동은 단일 볼륨 가정 (Docker named volume 1개). cross-volume 마운트(예: `/data/external` 별도 mount)에서는 EXDEV가 발생하며 0.0.1에서는 거부한다. 필요하면 후속 버전에서 재귀 copy+remove 폴백 도입.
- HLS live stream은 설정값 `url_import_timeout_seconds`(§2.7, 기본 30분) 또는 `url_import_max_bytes`(기본 10 GiB) 시점에 강제 종료 — 긴 live 컨텐츠는 끝까지 기록되지 않는다. 명시적 live 감지·분기는 없음. 필요하면 UI에서 값을 키워 재시도 가능.
- HLS 다운로드는 `start` 이벤트에 `total`이 없어 클라이언트 프로그래스 바는 indeterminate(수치 없이 애니메이션) 표시가 필요. 기존 UI가 `total` 없음을 허용하는지 §2.5 모달 구현 시 확인.
- HLS 임시 파일 TOCTOU: ffmpeg가 출력 MP4를 임시 디렉터리(`<destDir>/.urlimport-hls-<random>/output.mp4`) 안에 작성하므로 임시 파일 자체에 대한 별도 TOCTOU 창은 없다. atomic rename은 임시 디렉터리 → destDir로 단방향이고, 임시 디렉터리의 random suffix가 충돌 가능성을 사실상 0에 수렴시킨다.
- HLS 사전 다운로드 비용: 모든 segment/key/init을 ffmpeg 호출 전에 Go가 먼저 받아오므로 매우 큰 VOD(수만 segment)에선 첫 progress 이벤트까지의 지연이 길어진다. UX 측면에서 progress bar는 indeterminate 상태로 시작하여 점진적 단조 증가로 전환된다. 정상 사용 범위에선 무시할 만한 차이.

---

## 10. 0.0.1 릴리즈 범위

첫 공식 릴리즈. 폴더 운영(생성·이름 변경·삭제·이동)이 모두 갖춰져 single-user 미디어 서버로서 기본 기능이 닫힌다.

**포함:**
- [ ] 폴더 이동 백엔드 (§2.1.2 — `media.MoveDir` 신설, `PATCH /api/folder` body 분기)
- [ ] 폴더 이동 UI (§2.1.2, §2.1.3 — 사이드바 트리 ↔ 트리 / 메인 표 폴더 행 → 트리·breadcrumb DnD)
- [ ] 사이드바 트리 노드 🗑 삭제 버튼 (§2.1.3)
- [ ] 새 폴더 버튼 위치 이동 (메인 툴바 → 사이드바 헤더, §2.1.3)
- [ ] README 갱신 — 본 SPEC §2.1.2 / §2.1.3을 README features 목록에 반영, 0.0.1 릴리즈 노트 추가, 기존 폴더 작업 설명 업데이트

**범위 외 (후속 버전):**
- 사이드바 트리 노드별 + 버튼으로 임의 위치 폴더 생성
- 컨텍스트 메뉴 (우클릭) UI
- 다중 폴더 선택 이동
- cross-volume 폴더 이동 (EXDEV 재귀 copy 폴백)
- `/api/version` 엔드포인트, GitHub release 자동화
- WebDAV / 멀티 사용자 / 인증
