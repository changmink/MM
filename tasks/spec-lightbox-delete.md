# Spec: 라이트박스 내 삭제

Status: draft

## Objective
원본 이미지·동영상을 라이트박스로 열어둔 상태에서 현재 항목을 바로 삭제할 수 있게 한다. 지금은 폴더 뷰의 썸네일 카드에서만 삭제 진입이 있어, 라이트박스로 한 장씩 훑으며 정리할 때 닫고 다시 카드로 돌아가는 마찰이 발생한다. 본 스펙은 SPEC.md §2.5.5의 acceptance criteria를 풀어 쓴 작업 계약이다.

## Tech Stack
- Frontend: vanilla HTML/CSS/JS (`web/index.html`, `web/style.css`, `web/browse.js`, `web/fileOps.js`)
- Backend: 기존 `DELETE /api/file?path=<rel>` 재사용 — 신규 핸들러·API 없음
- Tests: Go handler 회귀(`go test ./internal/handler`) + 브라우저 수동 검증

## Commands
- Handler tests: `go test ./internal/handler`
- 전체 테스트: `go test ./...`
- Build: `go build ./cmd/server`
- Manual dev: `go run ./cmd/server` 후 브라우저에서 이미지/동영상 라이트박스 검증

## Project Structure
- `web/index.html`: 라이트박스 컨트롤(`#lightbox` 내부)에 `<button id="lb-delete">` 추가 — `lb-prev/lb-next/lb-close`와 동일 마크업 패턴
- `web/style.css`: `.lb-delete` 셀렉터를 `.lb-close, .lb-prev, .lb-next` 공유 규칙(`web/style.css:523`)에 합치고 위치만 별도 지정(예: `top: 16px; right: 72px;`)
- `web/browse.js`:
  - `openLightboxImage` / `openLightboxVideo`가 보여주는 현재 entry를 외부에서 알 수 있도록 라이트박스 상태(`lbType`, 현재 이미지/동영상 path)를 export 또는 모듈 내부에 보관
  - `wireBrowse`의 lightbox controls 블록(`web/browse.js:504-530`)에 `$.lbDelete` 클릭 핸들러 + `keydown`의 `Delete` 분기 추가
  - 이미지 삭제 후 `imageEntries.splice(lbIndex, 1)` → 인덱스 보정 → `openLightboxImage(newIndex)` 또는 라이트박스 close 분기
- `web/fileOps.js`: `deleteFile`은 그대로. 라이트박스 콜백은 별도 함수(`deleteFromLightbox(path, kind)`)로 분리하거나 `deleteFile`에 옵션 매개변수 도입 — 기존 호출자(썸네일 카드) 동작 회귀 금지
- `web/dom.js`: `$.lbDelete` 캐시 추가
- `tasks/todo.md`: Phase 28 신설 (라이트박스 내 삭제)
- `SPEC.md`: §2.5 체크리스트 + §2.5.5 본문 (이미 반영)

## Code Style
```js
// browse.js — 이미지 라이트박스 삭제
async function deleteCurrentImage() {
  if (!imageEntries.length) return;
  const entry = imageEntries[lbIndex];
  const ok = await deleteFile(entry.path, { skipBrowse: true });
  if (!ok) return; // alert는 deleteFile이 처리
  imageEntries.splice(lbIndex, 1);
  if (imageEntries.length === 0) {
    closeLightbox();
  } else {
    setLbIndex(lbIndex % imageEntries.length);
    openLightboxImage(lbIndex);
  }
  _browse(currentPath, false);
}
```
- `confirm()` 1회 — 새 모달 도입 금지 (기존 `deleteFile` 패턴 유지)
- 이미지 mutation은 라이트박스 안에서만 로컬로 처리하고, 마지막에 `_browse()`로 서버 진실 동기화
- 새 키 바인딩은 기존 `keydown` 리스너 한곳(`web/browse.js:525`)에 합쳐 핸들러가 흩어지지 않게 한다

## Testing Strategy
- 수동(브라우저):
  - 이미지 라이트박스에서 🗑 클릭 → confirm OK → 다음 이미지로 자동 이동, 마지막이면 닫힘
  - 같은 흐름을 `Delete` 키로 재현
  - confirm 취소 시 라이트박스 상태 그대로
  - 동영상 라이트박스에서 🗑/Delete → 닫힘 + 폴더 새로고침
  - 정렬·필터(§2.5.2) 통과한 visible 이미지만으로 prev/next 순환 유지되는지 확인
  - 삭제 후 사이드카(`.thumb/{name}.jpg`, `.dur`)가 남지 않는지(서버 처리 회귀)
- 자동:
  - `go test ./internal/handler` — `DELETE /api/file` 회귀 (기존 테스트 유지)
  - 새 핸들러를 추가하지 않으므로 신규 Go 테스트 의무 없음. UI 회귀는 수동.

## Boundaries
- **Always:** 같은-출처 보호된 `DELETE /api/file`만 호출한다. 새 mutation 엔드포인트 추가 금지.
- **Always:** 이미지 인덱스 보정 후 `_browse(currentPath, false)`로 서버 진실과 동기화한다.
- **Always:** 라이트박스가 닫힌 상태에서는 `Delete` 키 핸들러가 작동하지 않는다(다른 UI 단축키와 충돌 회피).
- **Ask first:** 휴지통/undo·다중 라이트박스 삭제·rename/move를 라이트박스 안에 추가하는 경우.
- **Ask first:** confirm 대신 새 인앱 모달을 도입하는 경우(현재 패턴 일관성 우선).
- **Never:** 사이드카 정리 로직을 클라이언트에서 수행. 서버 `DELETE /api/file` 핸들러가 이미 처리한다.
- **Never:** `_browse()` 호출 없이 imageEntries만 mutate하고 끝내기 — 서버 진실과 갈라진다.

## Success Criteria
- 이미지 라이트박스에서 🗑 또는 `Delete`로 현재 이미지 삭제 → 자동으로 다음 이미지(또는 닫기) 표시
- 동영상 라이트박스에서 🗑 또는 `Delete`로 현재 동영상 삭제 → 라이트박스 닫힘 + 폴더 새로고침
- 삭제 후 폴더 뷰가 갱신되어 해당 항목이 사라지고, `.thumb/` 사이드카도 함께 정리됨
- confirm 취소 시 어떠한 변경도 일어나지 않음
- 기존 썸네일 카드의 삭제 동작은 회귀 없음
- `go test ./...` 그린

## Out of Scope
- 다중 선택 라이트박스 일괄 삭제
- 휴지통(trash) / undo
- 라이트박스 안에서의 rename·move
- 음악 재생목록 트랙 삭제
- 모바일 long-press 삭제 제스처
