// state.js — 전역 상태 + 상수 (mutable export, setter 경유 재할당)

// ── Browse 상태 ─────────────────────────────────────────────────────────────
export let currentPath = '/';
export function setCurrentPath(v) { currentPath = v; }

export let allEntries = [];     // unfiltered /api/browse result for the current path
export function setAllEntries(v) { allEntries = v; }

export let imageEntries = [];   // images in current dir for lightbox (visible set)
export function setImageEntries(v) { imageEntries = v; }

export let videoEntries = [];   // videos in current dir for grid (visible set)
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

export let playlist = [];       // audio playlist (visible set)
export function setPlaylist(v) { playlist = v; }

export let playlistIndex = 0;
export function setPlaylistIndex(v) { playlistIndex = v; }

// ── 선택 (Set은 mutation으로 갱신) ──────────────────────────────────────────
export const selectedPaths = new Set();

// ── Sort/filter 상태 + 상수 ────────────────────────────────────────────────
// Sort/filter state. Drives toolbar + URL sync. Defaults match the URL
// defaults that are omitted from the querystring.
export const SORT_VALUES = new Set(['name:asc','name:desc','size:asc','size:desc','date:asc','date:desc']);
export const TYPE_VALUES = new Set(['all','image','video','audio','other','clip']);
// 움짤 ("clip") thresholds — see SPEC.md §2.5.3.
export const CLIP_MAX_BYTES = 50 * 1024 * 1024;
export const CLIP_MAX_DURATION_SEC = 30;
export const view = { sort: 'name:asc', q: '', type: 'all' };

// ── Tree ───────────────────────────────────────────────────────────────────
// Initial tree fetch depth — root + children + grandchildren in one round trip
// per user spec (Q1=opt3). Deeper nodes lazy-load on chevron click.
export const TREE_INIT_DEPTH = 2;

// ── Drag and Drop ──────────────────────────────────────────────────────────
// Custom MIME isolates internal file moves from external OS file uploads.
// Both share dragover semantics, so the upload zone checks for 'Files' instead.
export const DND_MIME = 'application/x-fileserver-move';

// Tracks the currently dragged file path. Needed at dragover time because
// dataTransfer.getData() is only readable on drop; types[] is readable
// always but doesn't include the value.
export let dragSrcPath = null;
export function setDragSrcPath(v) { dragSrcPath = v; }

export let dragSrcPaths = [];
export function setDragSrcPaths(v) { dragSrcPaths = v; }
