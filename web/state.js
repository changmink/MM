// state.js — 전역 상태 + 상수 (mutable export, setter 경유 재할당)

// ── Browse 상태 ─────────────────────────────────────────────────────────────
export let currentPath = '/';
export function setCurrentPath(v) { currentPath = v; }

export let allEntries = [];     // 현재 경로에 대한 필터되지 않은 /api/browse 결과
export function setAllEntries(v) { allEntries = v; }

export let imageEntries = [];   // 라이트박스용 현재 디렉터리의 이미지 (visible set)
export function setImageEntries(v) { imageEntries = v; }

export let videoEntries = [];   // 그리드용 현재 디렉터리의 영상 (visible set)
export function setVideoEntries(v) { videoEntries = v; }

export let visibleFilePaths = [];
export function setVisibleFilePaths(v) { visibleFilePaths = v; }

// ── Lightbox / Audio ───────────────────────────────────────────────────────
export let lbIndex = 0;
export function setLbIndex(v) { lbIndex = v; }

// 동영상 라이트박스가 들고 있는 현재 entry path. 이미지는 imageEntries[lbIndex]로
// 충분하지만 동영상은 prev/next가 없어 인덱스 추적이 없으므로 별도 보관.
// 라이트박스 닫힘 트리거에서 반드시 null로 리셋해 다음 이미지 라이트박스에서
// stale path가 살아남지 않게 한다.
export let lbCurrentVideoPath = null;
export function setLbCurrentVideoPath(v) { lbCurrentVideoPath = v; }

export let playlist = [];       // 오디오 플레이리스트 (visible set)
export function setPlaylist(v) { playlist = v; }

export let playlistIndex = 0;
export function setPlaylistIndex(v) { playlistIndex = v; }

// ── 선택 (Set은 mutation으로 갱신) ──────────────────────────────────────────
export const selectedPaths = new Set();

// ── Sort/filter 상태 + 상수 ────────────────────────────────────────────────
// 정렬/필터 상태. 툴바 + URL 동기화를 구동한다. 기본값은 querystring에서
// 생략되는 URL 기본값과 일치한다.
export const SORT_VALUES = new Set(['name:asc','name:desc','size:asc','size:desc','date:asc','date:desc']);
export const TYPE_VALUES = new Set(['all','image','video','audio','other','clip']);
// 움짤("clip") 임계값 — SPEC.md §2.5.3 참조.
export const CLIP_MAX_BYTES = 50 * 1024 * 1024;
export const CLIP_MAX_DURATION_SEC = 30;
export const view = { sort: 'name:asc', q: '', type: 'all' };

// ── Tree ───────────────────────────────────────────────────────────────────
// 트리 초기 fetch 깊이 — 사용자 spec(Q1=opt3)에 따라 한 번의 round-trip으로
// root + children + grandchildren을 가져온다. 더 깊은 노드는 chevron 클릭
// 시 lazy load 한다.
export const TREE_INIT_DEPTH = 2;

// ── Drag and Drop ──────────────────────────────────────────────────────────
// 커스텀 MIME으로 내부 파일 이동을 외부 OS 파일 업로드와 격리한다. 둘 다
// dragover 의미를 공유하므로 업로드 zone은 'Files'를 검사한다.
export const DND_MIME = 'application/x-fileserver-move';

// 현재 끌고 있는 파일 경로를 추적한다. dragover 시점에 필요한데, 그 이유는
// dataTransfer.getData()가 drop 시에만 읽을 수 있고 types[]는 항상 읽을 수
// 있지만 값은 포함하지 않기 때문이다.
export let dragSrcPath = null;
export function setDragSrcPath(v) { dragSrcPath = v; }

export let dragSrcPaths = [];
export function setDragSrcPaths(v) { dragSrcPaths = v; }
