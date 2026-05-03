# Spec: 다중 파일 선택 이동 UI

Status: implemented (`feature/multi-file-move-ui`)

## Objective
현재 UI는 파일 하나를 드래그해서 폴더로 이동할 수 있다. 사용자가 현재 폴더의 여러 파일 또는 필터/검색 결과 전체 파일을 선택한 뒤, 선택 묶음을 사이드바 폴더나 breadcrumb 경로로 드래그해 한 번에 이동할 수 있게 한다.

## Tech Stack
- Frontend: vanilla HTML/CSS/JS (`web/index.html`, `web/style.css`, ES module 분할 후 `web/main.js` 진입 + `web/browse.js`/`web/fileOps.js` 등 — 본 spec 작성 시점은 `app.js` 단일 파일 시절)
- Backend: 기존 `PATCH /api/file?path=<src>` + body `{"to":"<destDir>"}` 재사용
- Tests: Go handler/media 회귀 테스트 + 브라우저 수동 검증

## Commands
- Handler tests: `go test ./internal/handler`
- Media tests: `go test ./internal/media`
- Build: `go build ./cmd/server`
- Manual dev: `go run ./cmd/server`

## Project Structure
- `web/app.js`: 선택 상태, 전체 선택, 다중 drag payload, 순차 move 요청 *(머지 후 ES module 분할로 `web/state.js` (selectedPaths) + `web/browse.js` (selection UI) + `web/fileOps.js` (drag payload·move 요청)으로 이전됨)*
- `web/index.html`: 툴바 선택 컨트롤, cache bust version
- `web/style.css`: 카드/테이블 선택 체크박스와 선택 상태 스타일
- `tasks/todo.md`: Phase 22 진행 상태
- `SPEC.md`: 파일 관리/프론트엔드 acceptance criteria 반영

## Code Style
```js
function moveSelectionFor(entry) {
  if (selectedPaths.has(entry.path)) return Array.from(selectedPaths);
  return [entry.path];
}
```
- 기존 단일 파일 이동 API를 감싸는 얇은 UI 로직만 추가한다.
- `Set`으로 선택 상태를 관리하고, 렌더 시 visible 파일에 없는 선택은 제거한다.
- 서버 API나 이동 충돌 정책은 변경하지 않는다.

## Testing Strategy
- 필터/검색/정렬 후 visible 파일 기준으로 전체 선택이 동작하는지 수동 확인한다.
- 선택된 파일 중 하나를 드래그하면 선택 묶음이 같은 목적지로 순차 이동하는지 확인한다.
- 선택되지 않은 파일을 드래그하면 기존 단일 파일 이동이 유지되는지 확인한다.
- `go test ./internal/handler`와 `go test ./internal/media`로 기존 이동 API 회귀를 확인한다.

## Boundaries
- Always: 폴더는 선택/이동 대상에서 제외하고 파일만 이동한다.
- Always: 기존 `PATCH /api/file` API를 순차 호출해 서버 검증과 충돌 처리를 재사용한다.
- Ask first: 서버에 bulk move API를 새로 추가하거나 응답 계약을 바꾸는 경우.
- Never: 사용자 미커밋 변경이 있는 루트 워크트리를 수정하지 않는다.

## Success Criteria
- 파일 카드와 테이블 행에서 개별 파일 선택/해제가 가능하다.
- 툴바에서 현재 visible 파일 전체를 선택하거나 선택을 해제할 수 있다.
- 선택된 파일 중 하나를 사이드바 폴더 또는 breadcrumb 경로로 드래그하면 선택 파일들이 해당 폴더로 이동한다.
- 선택이 없거나 선택되지 않은 파일을 드래그하면 기존 단일 파일 이동처럼 동작한다.
- 이동 완료 후 현재 목록을 한 번 새로고침하고, 이동된 선택은 정리된다.

## Open Questions
- 없음. "전체 파일"은 현재 필터/검색을 통과한 visible 파일 전체로 해석한다.
