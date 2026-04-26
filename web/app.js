'use strict';

// ── State ────────────────────────────────────────────────────────────────────
let currentPath = '/';
let allEntries = [];     // unfiltered /api/browse result for the current path
let imageEntries = [];   // images in current dir for lightbox (visible set)
let videoEntries = [];   // videos in current dir for grid (visible set)
let lbIndex = 0;
let playlist = [];       // audio playlist (visible set)
let playlistIndex = 0;

// Sort/filter state. Drives toolbar + URL sync. Defaults match the URL
// defaults that are omitted from the querystring.
const SORT_VALUES = new Set(['name:asc','name:desc','size:asc','size:desc','date:asc','date:desc']);
const TYPE_VALUES = new Set(['all','image','video','audio','other','clip']);
// 움짤 ("clip") thresholds — see SPEC.md §2.5.3.
const CLIP_MAX_BYTES = 50 * 1024 * 1024;
const CLIP_MAX_DURATION_SEC = 30;
const view = { sort: 'name:asc', q: '', type: 'all' };

// ── DOM refs ─────────────────────────────────────────────────────────────────
const breadcrumb    = document.getElementById('breadcrumb');
const fileList      = document.getElementById('file-list');
const uploadZone    = document.getElementById('upload-zone');
const fileInput     = document.getElementById('file-input');
const uploadProgress = document.getElementById('upload-progress');
const lightbox      = document.getElementById('lightbox');
const lbContent     = document.getElementById('lb-content');
const lbClose       = document.getElementById('lb-close');
const lbPrev        = document.getElementById('lb-prev');
const lbNext        = document.getElementById('lb-next');
const audioPlayer   = document.getElementById('audio-player');
const audioEl       = document.getElementById('audio-el');
const audioTitle    = document.getElementById('audio-title');
const playlistEl    = document.getElementById('playlist');
const newFolderBtn  = document.getElementById('new-folder-btn');
const browseSummary = document.getElementById('browse-summary');
const browseToolbar = document.getElementById('browse-toolbar');
const typeButtons   = browseToolbar.querySelectorAll('.type-btn');
const toolbarSearch = document.getElementById('toolbar-search');
const toolbarSort   = document.getElementById('toolbar-sort');
const folderModal   = document.getElementById('folder-modal');
const folderNameInput = document.getElementById('folder-name-input');
const folderCancelBtn = document.getElementById('folder-cancel-btn');
const folderConfirmBtn = document.getElementById('folder-confirm-btn');
const folderError   = document.getElementById('folder-error');
const sidebarToggle = document.getElementById('sidebar-toggle');
const sidebarBackdrop = document.getElementById('sidebar-backdrop');
const treeRoot       = document.getElementById('tree-root');
const renameModal       = document.getElementById('rename-modal');
const renameTitle       = document.getElementById('rename-title');
const renameHint        = document.getElementById('rename-hint');
const renameInput       = document.getElementById('rename-input');
const renameError       = document.getElementById('rename-error');
const renameCancelBtn   = document.getElementById('rename-cancel-btn');
const renameConfirmBtn  = document.getElementById('rename-confirm-btn');
const urlImportBtn  = document.getElementById('url-import-btn');
const urlModal      = document.getElementById('url-modal');
const urlInput      = document.getElementById('url-input');
const urlError      = document.getElementById('url-error');
const urlRows       = document.getElementById('url-rows');
const urlSummary    = document.getElementById('url-summary');
const urlResult     = document.getElementById('url-result');
const urlCancelBtn  = document.getElementById('url-cancel-btn');
const urlConfirmBtn = document.getElementById('url-confirm-btn');
const urlClearFinishedBtn = document.getElementById('url-clear-finished-btn');
const urlBadge     = document.getElementById('url-badge');
const convertAllBtn       = document.getElementById('convert-all-btn');
const convertModal        = document.getElementById('convert-modal');
const convertFileList     = document.getElementById('convert-file-list');
const convertDeleteOrig   = document.getElementById('convert-delete-original');
const convertError        = document.getElementById('convert-error');
const convertResult       = document.getElementById('convert-result');
const convertRows         = document.getElementById('convert-rows');
const convertSummary      = document.getElementById('convert-summary');
const convertCancelBtn    = document.getElementById('convert-cancel-btn');
const convertConfirmBtn   = document.getElementById('convert-confirm-btn');

// Initial tree fetch depth — root + children + grandchildren in one round trip
// per user spec (Q1=opt3). Deeper nodes lazy-load on chevron click.
const TREE_INIT_DEPTH = 2;

// Custom MIME isolates internal file moves from external OS file uploads.
// Both share dragover semantics, so the upload zone checks for 'Files' instead.
const DND_MIME = 'application/x-fileserver-move';

// Tracks the currently dragged file path. Needed at dragover time because
// dataTransfer.getData() is only readable on drop; types[] is readable
// always but doesn't include the value.
let dragSrcPath = null;

// ── Routing ───────────────────────────────────────────────────────────────────
// popstate treats the URL as the source of truth — read view + path out of it,
// sync the toolbar widgets, then fetch. browse(..., false) won't rewrite the
// URL, so we don't loop.
window.addEventListener('popstate', () => {
  const p = new URLSearchParams(location.search).get('path') || '/';
  readViewFromURL();
  syncToolbarUI();
  browse(p, false);
});

// ── URL <-> view state ────────────────────────────────────────────────────────
function readViewFromURL() {
  const p = new URLSearchParams(location.search);
  const s = p.get('sort'); view.sort = SORT_VALUES.has(s) ? s : 'name:asc';
  view.q = (p.get('q') || '').trim();
  const t = p.get('type'); view.type = TYPE_VALUES.has(t) ? t : 'all';
}
function syncURL(push) {
  const p = new URLSearchParams();
  p.set('path', currentPath);
  if (view.sort !== 'name:asc') p.set('sort', view.sort);
  if (view.q) p.set('q', view.q);
  if (view.type !== 'all') p.set('type', view.type);
  const qs = '?' + p.toString();
  if (push) history.pushState({}, '', qs);
  else history.replaceState({}, '', qs);
}
function syncToolbarUI() {
  typeButtons.forEach(btn => btn.classList.toggle('active', btn.dataset.type === view.type));
  toolbarSearch.value = view.q;
  toolbarSort.value = view.sort;
}

// ── Browse ────────────────────────────────────────────────────────────────────
async function browse(path, pushState = true) {
  currentPath = path;
  if (pushState) syncURL(true);
  renderBreadcrumb(path);

  let data;
  try {
    const res = await fetch('/api/browse?path=' + encodeURIComponent(path));
    if (!res.ok) throw new Error(await res.text());
    data = await res.json();
  } catch (e) {
    fileList.innerHTML = `<p class="error">오류: ${e.message}</p>`;
    return;
  }

  allEntries = data.entries || [];
  renderView();
  highlightTreeCurrent();
}

// Apply sort/filter to allEntries and render. Split from browse() so the
// toolbar can re-render without refetching. Keeps lightbox/playlist arrays
// in sync with the visible set so prev/next don't land on hidden entries.
function renderView() {
  const visible = applyView(allEntries);
  imageEntries = visible.filter(e => e.type === 'image');
  videoEntries = visible.filter(e => e.type === 'video');
  playlist     = visible.filter(e => e.type === 'audio');
  renderBrowseSummary(visible);
  renderFileList(visible);
  updateConvertAllBtn(visible);
}

function visibleTSPaths(visible) {
  return visible
    .filter(e => !e.is_dir && e.type === 'video' && e.name.toLowerCase().endsWith('.ts'))
    .map(e => e.path);
}

function updateConvertAllBtn(visible) {
  const paths = visibleTSPaths(visible);
  if (paths.length === 0) {
    convertAllBtn.hidden = true;
    return;
  }
  convertAllBtn.hidden = false;
  convertAllBtn.textContent = `모든 TS 변환 (${paths.length})`;
}

// 움짤 — GIF is always a clip; a video is a clip only when it's small
// AND short (null duration excludes — we can't prove it's short).
function isClip(e) {
  if (e.mime === 'image/gif') return true;
  if (e.type === 'video') {
    return e.size <= CLIP_MAX_BYTES
      && e.duration_sec != null
      && e.duration_sec <= CLIP_MAX_DURATION_SEC;
  }
  return false;
}

function applyView(entries) {
  const files = entries.filter(e => !e.is_dir);
  // image/video/clip are mutually exclusive: clips never appear in the
  // image or video tabs. The "전체" tab keeps all files in their natural
  // sections so nothing is hidden without an explicit filter.
  let out;
  if (view.type === 'all') {
    out = files;
  } else if (view.type === 'clip') {
    out = files.filter(isClip);
  } else {
    out = files.filter(e => e.type === view.type && !isClip(e));
  }
  if (view.q) {
    const needle = view.q.toLowerCase();
    out = out.filter(e => e.name.toLowerCase().includes(needle));
  }
  const [key, dir] = view.sort.split(':');
  const mul = dir === 'desc' ? -1 : 1;
  const byName = (a, b) =>
    a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
  out.sort((a, b) => {
    let cmp = 0;
    if (key === 'name') cmp = byName(a, b);
    else if (key === 'size') cmp = a.size - b.size;
    else if (key === 'date') cmp = new Date(a.mod_time) - new Date(b.mod_time);
    if (cmp === 0 && key !== 'name') cmp = byName(a, b);
    return mul * cmp;
  });
  return out;
}

function renderBrowseSummary(entries) {
  const files = entries.filter(e => !e.is_dir);
  if (files.length === 0) {
    browseSummary.textContent = '';
    return;
  }
  const total = files.reduce((s, e) => s + (e.size || 0), 0);
  browseSummary.textContent = `파일 ${files.length}개 · ${formatSize(total)}`;
}

function renderBreadcrumb(path) {
  breadcrumb.innerHTML = '';

  const home = document.createElement('a');
  home.href = 'javascript:void(0)';
  home.textContent = '홈';
  home.addEventListener('click', () => browse('/'));
  attachDropHandlers(home, '/');
  breadcrumb.appendChild(home);

  const parts = path.split('/').filter(Boolean);
  let accumulated = '';
  parts.forEach((part, i) => {
    const sep = document.createElement('span');
    sep.textContent = '/';
    breadcrumb.appendChild(sep);

    accumulated += '/' + part;
    const isLast = i === parts.length - 1;
    if (isLast) {
      const span = document.createElement('span');
      span.textContent = part;
      breadcrumb.appendChild(span);
    } else {
      const a = document.createElement('a');
      a.href = 'javascript:void(0)';
      a.textContent = part;
      const p = accumulated;
      a.addEventListener('click', () => browse(p));
      attachDropHandlers(a, p);
      breadcrumb.appendChild(a);
    }
  });
}

function renderFileList(entries) {
  fileList.innerHTML = '';

  // Folders intentionally omitted from the main list — the sidebar tree is
  // the single navigation surface. Files-only sections below.
  const images = entries.filter(e => e.type === 'image');
  const videos = entries.filter(e => e.type === 'video');
  const audios = entries.filter(e => e.type === 'audio');
  const others = entries.filter(e => e.type === 'other');

  if (images.length) {
    fileList.appendChild(sectionTitle('이미지'));
    fileList.appendChild(buildImageGrid(images));
  }
  if (videos.length) {
    fileList.appendChild(sectionTitle('동영상'));
    fileList.appendChild(buildVideoGrid(videos));
  }
  if (audios.length) {
    fileList.appendChild(sectionTitle('음악'));
    fileList.appendChild(buildTable(audios));
  }
  if (others.length) {
    fileList.appendChild(sectionTitle('기타'));
    fileList.appendChild(buildTable(others));
  }

  const fileCount = images.length + videos.length + audios.length + others.length;
  if (!fileCount) {
    const msg = (view.q || view.type !== 'all')
      ? '검색 결과가 없습니다.'
      : '파일이 없습니다.';
    fileList.innerHTML = `<p style="color:var(--text-dim);padding:20px 0">${msg}</p>`;
  }
}

function sectionTitle(text) {
  const el = document.createElement('div');
  el.className = 'section-title';
  el.textContent = text;
  return el;
}

function buildImageGrid(images) {
  const grid = document.createElement('div');
  grid.className = 'image-grid';
  images.forEach((entry, i) => {
    const card = document.createElement('div');
    card.className = 'thumb-card';

    const thumbSrc = entry.thumb_available
      ? '/api/thumb?path=' + encodeURIComponent(entry.path)
      : '/api/stream?path=' + encodeURIComponent(entry.path);

    card.innerHTML = `
      <img src="${esc(thumbSrc)}" alt="${esc(entry.name)}" loading="lazy">
      <div class="thumb-name">${esc(entry.name)}</div>
      <span class="size-badge">${esc(formatSize(entry.size))}</span>
      <button class="rename-btn" title="이름 변경" aria-label="이름 변경">✎</button>
      <button class="delete-btn" title="삭제" aria-label="삭제">✕</button>
    `;
    card.querySelector('img').addEventListener('click', () => openLightboxImage(i));
    card.querySelector('.rename-btn').addEventListener('click', (ev) => {
      ev.stopPropagation();
      openRenameModal(entry);
    });
    card.querySelector('.delete-btn').addEventListener('click', (ev) => {
      ev.stopPropagation();
      deleteFile(entry.path);
    });
    attachDragHandlers(card, entry);
    grid.appendChild(card);
  });
  return grid;
}

function buildVideoGrid(videos) {
  const grid = document.createElement('div');
  grid.className = 'image-grid';
  videos.forEach((entry, i) => {
    const card = document.createElement('div');
    card.className = 'thumb-card';

    const thumbSrc = '/api/thumb?path=' + encodeURIComponent(entry.path);
    const dur = formatDuration(entry.duration_sec);
    const durBadge = dur ? `<span class="duration-badge">${esc(dur)}</span>` : '';
    const isTS = entry.name.toLowerCase().endsWith('.ts');
    const convertBtn = isTS
      ? `<button class="convert-btn" title="MP4로 변환" aria-label="MP4로 변환">MP4</button>`
      : '';

    card.innerHTML = `
      <img src="${esc(thumbSrc)}" alt="${esc(entry.name)}" loading="lazy">
      <div class="thumb-name">${esc(entry.name)}</div>
      <span class="size-badge">${esc(formatSize(entry.size))}</span>
      ${durBadge}
      ${convertBtn}
      <button class="rename-btn" title="이름 변경" aria-label="이름 변경">✎</button>
      <button class="delete-btn" title="삭제" aria-label="삭제">✕</button>
    `;
    card.querySelector('img').addEventListener('click', () => openLightboxVideo(entry));
    card.querySelector('.rename-btn').addEventListener('click', (ev) => {
      ev.stopPropagation();
      openRenameModal(entry);
    });
    card.querySelector('.delete-btn').addEventListener('click', (ev) => {
      ev.stopPropagation();
      deleteFile(entry.path);
    });
    if (isTS) {
      card.querySelector('.convert-btn').addEventListener('click', (ev) => {
        ev.stopPropagation();
        openConvertModal([entry.path]);
      });
    }
    attachDragHandlers(card, entry);
    grid.appendChild(card);
  });
  return grid;
}

function buildTable(entries) {
  const table = document.createElement('table');
  table.className = 'file-table';
  table.innerHTML = `<thead><tr>
    <th>이름</th>
    <th class="size-cell">크기</th>
    <th></th>
  </tr></thead>`;
  const tbody = document.createElement('tbody');

  entries.forEach(entry => {
    const tr = document.createElement('tr');
    const icon = iconFor(entry.type, entry.is_dir);
    const size = entry.is_dir ? '—' : formatSize(entry.size);
    tr.innerHTML = `
      <td class="name-cell"><span class="icon">${icon}</span>${esc(entry.name)}</td>
      <td class="size-cell">${size}</td>
      <td class="action-cell">
        <button class="rename-action" title="이름 변경" aria-label="이름 변경">✎</button>
        <button class="delete-action" title="삭제" aria-label="삭제">🗑</button>
      </td>
    `;
    tr.querySelector('.name-cell').addEventListener('click', () => handleClick(entry));
    tr.querySelector('.rename-action').addEventListener('click', () => openRenameModal(entry));
    tr.querySelector('.delete-action').addEventListener('click', () =>
      entry.is_dir ? deleteFolder(entry.path) : deleteFile(entry.path)
    );
    if (!entry.is_dir) {
      attachDragHandlers(tr, entry);
    }
    tbody.appendChild(tr);
  });

  table.appendChild(tbody);
  return table;
}

function handleClick(entry) {
  if (entry.is_dir) {
    browse(entry.path);
  } else if (entry.type === 'video') {
    openLightboxVideo(entry);
  } else if (entry.type === 'audio') {
    playAudio(entry);
  } else if (entry.type === 'image') {
    const idx = imageEntries.findIndex(e => e.path === entry.path);
    openLightboxImage(idx >= 0 ? idx : 0);
  } else {
    window.open('/api/stream?path=' + encodeURIComponent(entry.path), '_blank');
  }
}

// ── Lightbox ──────────────────────────────────────────────────────────────────
function openLightboxImage(index) {
  lbIndex = index;
  const entry = imageEntries[lbIndex];
  lbContent.innerHTML = `<img src="/api/stream?path=${encodeURIComponent(entry.path)}" alt="${esc(entry.name)}">`;
  lightbox.classList.remove('hidden');
}

function openLightboxVideo(entry) {
  const mime = entry.path.toLowerCase().endsWith('.ts') ? 'video/mp4' : (entry.mime || 'video/mp4');
  lbContent.innerHTML = `
    <video controls autoplay>
      <source src="/api/stream?path=${encodeURIComponent(entry.path)}" type="${esc(mime)}">
    </video>`;
  lightbox.classList.remove('hidden');
}

lbClose.addEventListener('click', () => {
  lightbox.classList.add('hidden');
  lbContent.innerHTML = '';
});

lbPrev.addEventListener('click', () => {
  if (!imageEntries.length) return;
  lbIndex = (lbIndex - 1 + imageEntries.length) % imageEntries.length;
  openLightboxImage(lbIndex);
});

lbNext.addEventListener('click', () => {
  if (!imageEntries.length) return;
  lbIndex = (lbIndex + 1) % imageEntries.length;
  openLightboxImage(lbIndex);
});

lightbox.addEventListener('click', e => {
  if (e.target === lightbox) {
    lightbox.classList.add('hidden');
    lbContent.innerHTML = '';
  }
});

document.addEventListener('keydown', e => {
  if (lightbox.classList.contains('hidden')) return;
  if (e.key === 'Escape') lbClose.click();
  if (e.key === 'ArrowLeft') lbPrev.click();
  if (e.key === 'ArrowRight') lbNext.click();
});

// ── Audio Player ──────────────────────────────────────────────────────────────
function playAudio(entry) {
  playlistIndex = playlist.findIndex(e => e.path === entry.path);
  if (playlistIndex < 0) playlistIndex = 0;
  loadPlaylistTrack(playlistIndex);
  audioPlayer.classList.remove('hidden');
  renderPlaylist();
}

function loadPlaylistTrack(index) {
  const entry = playlist[index];
  audioEl.src = '/api/stream?path=' + encodeURIComponent(entry.path);
  audioTitle.textContent = entry.name;
  audioEl.play();
  renderPlaylist();
}

function renderPlaylist() {
  playlistEl.innerHTML = '';
  playlist.forEach((entry, i) => {
    const item = document.createElement('div');
    item.className = 'playlist-item' + (i === playlistIndex ? ' active' : '');
    item.textContent = entry.name;
    item.addEventListener('click', () => {
      playlistIndex = i;
      loadPlaylistTrack(i);
    });
    playlistEl.appendChild(item);
  });
}

audioEl.addEventListener('ended', () => {
  if (playlistIndex < playlist.length - 1) {
    playlistIndex++;
    loadPlaylistTrack(playlistIndex);
  }
});

// ── Upload ────────────────────────────────────────────────────────────────────
// Internal card drags carry our custom MIME but no Files; external OS drags
// carry Files. Gate on Files so dragging an internal file over the upload
// zone doesn't light it up.
function isExternalFileDrag(e) {
  return Array.from(e.dataTransfer.types).includes('Files');
}

uploadZone.addEventListener('dragover', e => {
  if (!isExternalFileDrag(e)) return;
  e.preventDefault();
  uploadZone.classList.add('drag-over');
});
uploadZone.addEventListener('dragleave', () => uploadZone.classList.remove('drag-over'));
uploadZone.addEventListener('drop', e => {
  if (!isExternalFileDrag(e)) return;
  e.preventDefault();
  uploadZone.classList.remove('drag-over');
  uploadFiles(e.dataTransfer.files);
});

fileInput.addEventListener('change', () => {
  uploadFiles(fileInput.files);
  fileInput.value = '';
});

function uploadFiles(files) {
  Array.from(files).forEach(file => uploadOne(file));
}

function uploadOne(file) {
  const container = document.createElement('div');
  container.className = 'progress-item';
  container.innerHTML = `
    <span>${esc(file.name)}</span>
    <div class="bar"><div class="bar-fill" style="width:0%"></div></div>
  `;
  uploadProgress.appendChild(container);
  const fill = container.querySelector('.bar-fill');

  const xhr = new XMLHttpRequest();
  xhr.upload.addEventListener('progress', e => {
    if (e.lengthComputable) {
      fill.style.width = Math.round((e.loaded / e.total) * 100) + '%';
    }
  });
  xhr.addEventListener('load', () => {
    if (xhr.status === 201) {
      fill.style.width = '100%';
      setTimeout(() => container.remove(), 1500);
      browse(currentPath, false);
    } else {
      container.style.color = 'var(--danger)';
    }
  });
  xhr.addEventListener('error', () => {
    container.style.color = 'var(--danger)';
  });

  const form = new FormData();
  form.append('file', file);
  xhr.open('POST', '/api/upload?path=' + encodeURIComponent(currentPath));
  xhr.send(form);
}

// ── Folder Create ─────────────────────────────────────────────────────────────
newFolderBtn.addEventListener('click', openFolderModal);
folderCancelBtn.addEventListener('click', closeFolderModal);
folderModal.addEventListener('click', e => { if (e.target === folderModal) closeFolderModal(); });
folderConfirmBtn.addEventListener('click', submitCreateFolder);
folderNameInput.addEventListener('keydown', e => { if (e.key === 'Enter') submitCreateFolder(); });

function openFolderModal() {
  folderNameInput.value = '';
  folderError.textContent = '';
  folderError.classList.add('hidden');
  folderModal.classList.remove('hidden');
  folderNameInput.focus();
}

function closeFolderModal() {
  folderModal.classList.add('hidden');
}

let folderSubmitting = false;

async function submitCreateFolder() {
  if (folderSubmitting) return;
  const name = folderNameInput.value.trim();
  if (!name) {
    showFolderError('폴더 이름을 입력하세요.');
    return;
  }
  folderSubmitting = true;
  folderConfirmBtn.disabled = true;
  try {
    const res = await fetch('/api/folder?path=' + encodeURIComponent(currentPath), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    if (res.status === 201) {
      closeFolderModal();
      browse(currentPath, false);
      loadTree();
    } else if (res.status === 409) {
      showFolderError('이미 존재하는 폴더입니다.');
    } else {
      showFolderError('유효하지 않은 이름입니다.');
    }
  } finally {
    folderSubmitting = false;
    folderConfirmBtn.disabled = false;
  }
}

function showFolderError(msg) {
  folderError.textContent = msg;
  folderError.classList.remove('hidden');
}

// ── URL Import ────────────────────────────────────────────────────────────────
// Labels intentionally omit specific byte/time limits because those are
// configurable at runtime via /api/settings — hardcoding numbers here made
// the UI lie when the user had bumped the cap or timeout.
const URL_ERROR_LABELS = {
  missing_content_length: 'Content-Length 헤더 없음',
  too_large: '크기 상한 초과',
  unsupported_content_type: '지원하지 않는 미디어 타입',
  invalid_scheme: '지원하지 않는 스킴',
  invalid_url: '잘못된 URL',
  http_error: 'HTTP 응답 에러',
  connect_timeout: '연결 타임아웃',
  download_timeout: '다운로드 타임아웃',
  tls_error: 'TLS 검증 실패',
  too_many_redirects: '리다이렉트 과다',
  network_error: '네트워크 오류',
  write_error: '저장 실패',
  ffmpeg_error: 'HLS 리먹싱 실패',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  hls_playlist_too_large: 'HLS 플레이리스트 크기 초과',
  // `cancelled` is handled as a distinct row state (not a failure) — see
  // handleSSEEvent's error branch and applyURLStateToRow's "cancelled"
  // case. No dictionary entry needed.
};

// Each batch captures one in-flight POST /api/import-url. Several batches
// can coexist: while one is running, reopening the modal turns the confirm
// button into "새 배치 추가" and additional submits append a new batch
// below the existing rows. The server serializes them via `importSem`,
// emitting a `queued` SSE event we surface as a "waiting" row state.
//
// We deliberately do NOT wire up an AbortController per batch — closing
// the modal must keep the fetch running so the user can reopen and still
// see progress. Browser-initiated fetch aborts (tab close / navigation)
// flow to the server as `r.Context()` cancel.
const urlBatches = [];
let urlBatchSeq = 0;
// True only during the short POST setup window of an in-flight submit —
// prevents accidental double-submits of the same URL list. Once SSE
// consumption starts we flip it off so the user can queue another batch
// without waiting for this one to finish.
let urlSubmittingNow = false;
// When a round finishes with only failures we keep the batch metadata
// around briefly so the badge can still surface the ⚠ marker the user
// might have missed if they were off-screen. Cleared on the next open or
// the next completion round.
let urlBadgeLinger = false;

function anyBatchActive() {
  return urlBatches.some(b => !b.done);
}
function anyBatchSucceeded() {
  return urlBatches.some(b => b.succeeded > 0);
}

urlImportBtn.addEventListener('click', openURLModal);
urlCancelBtn.addEventListener('click', closeURLModal);
urlConfirmBtn.addEventListener('click', submitURLImport);
urlClearFinishedBtn.addEventListener('click', dismissAllFinishedBatches);
urlModal.addEventListener('click', e => { if (e.target === urlModal) closeURLModal(); });
urlBadge.addEventListener('click', openURLModal);
document.addEventListener('keydown', e => {
  if (urlModal.classList.contains('hidden')) return;
  if (e.key === 'Escape') closeURLModal();
});

function openURLModal() {
  urlInput.value = '';
  urlError.textContent = '';
  urlError.classList.add('hidden');

  // Preserve the modal's contents whenever urlBatches has anything to
  // show. That includes:
  //   - active batches (live progress)
  //   - finished batches restored from bootstrapURLJobs (history rows
  //     the user can dismiss via "닫기" / "완료 항목 모두 지우기")
  //   - finished batches still lingering from an in-session error round
  // Only fully-empty registry counts as a "fresh start" — that's when
  // maybeFinalize already cleared everything after a successful round
  // or after the 3-second linger of an error-only round.
  if (urlBatches.length === 0) {
    urlBadgeLinger = false;
    urlRows.innerHTML = '';
    urlSummary.textContent = '';
    urlSummary.className = 'url-summary hidden';
    urlResult.classList.add('hidden');
  }

  urlModal.classList.remove('hidden');
  updateURLBadge();
  updateConfirmButton();
  urlInput.focus();
}

// Keeps the confirm button in sync with the current registry state so
// its label always answers "what happens if I click now?". We re-enter
// here from every state transition: open, submit entry, SSE started,
// batch settled.
function updateConfirmButton() {
  if (urlSubmittingNow) {
    urlConfirmBtn.disabled = true;
    urlConfirmBtn.textContent = '가져오는 중...';
  } else if (anyBatchActive()) {
    urlConfirmBtn.disabled = false;
    urlConfirmBtn.textContent = '새 배치 추가';
  } else {
    urlConfirmBtn.disabled = false;
    urlConfirmBtn.textContent = '가져오기';
  }
}

function closeURLModal() {
  // Pure view-hide: fetches keep running, the badge takes over the
  // progress surface. Tab close or navigation still aborts via the
  // browser, which the server handles as r.Context() cancel.
  urlModal.classList.add('hidden');
  updateURLBadge();
}

function updateURLBadge() {
  // The "완료 항목 모두 지우기" button lives inside the modal — keep its
  // visibility in lockstep with the registry so the user sees it as soon
  // as a finished batch is available to dismiss.
  const hasFinished = urlBatches.some(b => b.done);
  urlClearFinishedBtn.classList.toggle('hidden', !hasFinished);

  const modalHidden = urlModal.classList.contains('hidden');
  const shouldShow = modalHidden && (anyBatchActive() || urlBadgeLinger);
  if (!shouldShow) {
    urlBadge.classList.add('hidden');
    urlBadge.classList.remove('has-error');
    return;
  }
  // Badge aggregates ONLY active batches (restored or freshly POSTed) so
  // the running progress bar reflects what's still in flight. Restored
  // finished batches are background context, not active progress.
  let completed = 0, total = 0, failed = 0;
  for (const b of urlBatches) {
    if (b.done) continue;
    completed += b.succeeded + b.failed + (b.cancelled || 0);
    total     += b.total;
    failed    += b.failed;
  }
  urlBadge.classList.remove('hidden');
  urlBadge.classList.toggle('has-error', failed > 0);
  urlBadge.textContent = `URL ↓ ${completed}/${total}` + (failed > 0 ? ' ⚠' : '');
}

// maybeFinalize runs whenever a batch settles (normal completion, abort,
// network error, or HTTP error). It scopes summarization to "this session's
// round" — batches submitted in this session AND not loaded from the
// server's history snapshot. Restored batches are backdrop UI; aggregating
// them into the round summary would mix prior-session results into the
// current operation's totals.
function maybeFinalize() {
  // Round = batches submitted in this session (restored ones are history).
  const round = urlBatches.filter(b => !b.restored);
  if (round.length === 0) {
    updateURLBadge();
    return;
  }
  if (round.some(b => !b.done)) return;

  let succeeded = 0, failed = 0, cancelled = 0;
  for (const b of round) {
    succeeded += b.succeeded;
    failed    += b.failed;
    cancelled += b.cancelled || 0;
  }

  // Mirror server status precedence (SPEC §2.6): succeeded≥1 → completed
  // even if some failed/cancelled; cancelled-only → cancelled; else error.
  let cls;
  if (succeeded > 0 && failed === 0 && cancelled === 0)      cls = 'status-done';
  else if (succeeded > 0)                                    cls = 'status-mixed';
  else if (failed === 0 && cancelled > 0)                    cls = 'status-cancelled';
  else                                                       cls = 'status-error';

  // Round entry guard above (round.length === 0 short-circuits) plus the
  // "every batch done" requirement guarantee at least one terminal URL,
  // so at least one of the three counters is non-zero. No empty-parts
  // fallback needed.
  const parts = [];
  if (succeeded > 0) parts.push(`성공 ${succeeded}`);
  if (failed > 0)    parts.push(`실패 ${failed}`);
  if (cancelled > 0) parts.push(`취소 ${cancelled}`);
  urlSummary.className = 'url-summary ' + cls;
  urlSummary.textContent = parts.join(' · ');

  if (succeeded > 0) {
    // Success: clear ONLY this round (restored history stays). Browse
    // refresh will surface the new files; the modal can keep history rows
    // for the user to dismiss when they want.
    clearRoundBatches(round);
    urlBadgeLinger = false;
    updateURLBadge();
    browse(currentPath, false);
    return;
  }

  // No success (all failed or all cancelled): keep the badge up briefly so
  // the user can click in to inspect, then clear THIS round (we capture
  // `round` in closure so a new round started in the meantime is unaffected).
  urlBadgeLinger = true;
  updateURLBadge();
  setTimeout(() => {
    clearRoundBatches(round);
    urlBadgeLinger = false;
    updateURLBadge();
  }, 3000);
}

// clearRoundBatches tears down the DOM for each batch and removes it from
// urlBatches, while leaving the rest of the registry alone. Used by
// maybeFinalize so completed rounds graduate out of the active state but
// restored history stays visible until the user dismisses it.
function clearRoundBatches(batches) {
  for (const b of batches) {
    removeBatchRows(b);
    const idx = urlBatches.indexOf(b);
    if (idx !== -1) urlBatches.splice(idx, 1);
  }
}

async function submitURLImport() {
  // Reject only while a POST is mid-flight — once SSE is flowing the user
  // is free to queue another batch on top.
  if (urlSubmittingNow) return;
  const urls = urlInput.value
    .split('\n')
    .map(s => s.trim())
    .filter(Boolean);

  if (urls.length === 0) {
    showURLError('URL을 한 줄에 하나씩 입력하세요.');
    return;
  }
  if (urls.length > 50) {
    showURLError('한 번에 최대 50개까지 입력할 수 있습니다.');
    return;
  }

  urlError.classList.add('hidden');
  // Wipe DOM only when the registry is truly empty. Restored history (or
  // a still-clearing prior round) stays visible — clearing them here while
  // they remain in urlBatches would desync DOM from internal counters.
  if (urlBatches.length === 0) {
    urlRows.innerHTML = '';
    urlSummary.textContent = '';
    urlSummary.className = 'url-summary hidden';
  }
  urlResult.classList.remove('hidden');

  const batch = {
    id: ++urlBatchSeq,
    jobId: null,
    rowEls: new Map(),
    headerEl: null,
    succeeded: 0,
    failed: 0,
    cancelled: 0,
    total: urls.length,
    done: false,
    restored: false,
    eventSource: null,
  };
  urlBatches.push(batch);

  // Position label counts only current-round batches — restored history
  // has its own labels ("복원된 배치"/"이전 결과") so it shouldn't bump the
  // "배치 N" numbering. The first batch in a fresh round gets no label.
  const roundCount = urlBatches.filter(b => !b.restored).length;
  appendBatchHeader(batch, roundCount > 1 ? `배치 ${roundCount}` : '');

  // Pre-create one pending row per URL so users see immediate feedback even
  // before the first SSE event arrives.
  urls.forEach((u, i) => ensureURLRow(batch, i, u));
  // Clear the textarea so the user can immediately type another batch
  // without having to select-all-and-delete first.
  urlInput.value = '';

  urlSubmittingNow = true;
  updateConfirmButton();

  // Tracks whether the SSE stream actually opened. The HTTP-error branch
  // pops the batch and returns before flipping this, so orphan-finalize
  // (below) only runs for streams that did open and were then cut.
  let sseOpened = false;

  try {
    const res = await fetch('/api/import-url?path=' + encodeURIComponent(currentPath), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({ urls }),
    });
    if (!res.ok) {
      let msg = '';
      try { msg = (await res.json()).error || ''; } catch { /* not JSON */ }
      if (!msg) msg = `요청 실패 (${res.status})`;
      showURLError(msg);
      // Tear down this batch's DOM regardless — its pre-created rows
      // would otherwise linger as stuck "대기 중" placeholders. If
      // nothing else (active or restored) is in the registry, hide the
      // whole result panel; otherwise leave it visible for siblings.
      removeBatchRows(batch);
      const idx = urlBatches.indexOf(batch);
      if (idx !== -1) urlBatches.splice(idx, 1);
      if (urlBatches.length === 0) {
        urlResult.classList.add('hidden');
      }
      return;
    }
    // SSE connected. Free the button so the user can queue another batch
    // without having to wait for this one to finish.
    sseOpened = true;
    urlSubmittingNow = false;
    updateConfirmButton();
    await consumeSSE(res, ev => handleSSEEvent(batch, ev));
  } catch (e) {
    // AbortError can still arrive on browser-initiated cancels (tab
    // navigation) even though we no longer wire up an AbortController —
    // the document's lifecycle signal aborts pending fetches. Swallow
    // those silently; the page is going away anyway.
    if (e.name !== 'AbortError') {
      showURLError('요청 실패: ' + e.message);
    }
  } finally {
    // The server's contract is start → done|error per URL plus a final
    // summary, so any URL row still in pending/downloading state means the
    // stream was cut (network drop, proxy timeout, server crash). Convert
    // those into explicit failures — otherwise the badge / aggregate
    // summary undercount and the round can falsely report "0 failed".
    // Only run when the stream actually opened (sseOpened) — for HTTP
    // errors the batch was already popped from the registry above.
    if (sseOpened) finalizeOrphanRows(batch);
    batch.done = true;
    urlSubmittingNow = false;
    updateBatchControls(batch);
    maybeFinalize();
    updateConfirmButton();
    updateURLBadge();
  }
}

// finalizeOrphanRows promotes any non-terminal row in the batch (still
// pending or downloading) into a "연결 끊김" failure. Called from
// submitURLImport's finally when the SSE stream opened but ended without
// terminal events for every URL.
function finalizeOrphanRows(batch) {
  for (const row of batch.rowEls.values()) {
    if (row.classList.contains('status-done') ||
        row.classList.contains('status-error') ||
        row.classList.contains('status-cancelled')) {
      continue;
    }
    row.classList.remove('url-row-indeterminate');
    setRowStatus(row, 'status-error', '실패 · 연결 끊김');
    batch.failed++;
  }
}

// consumeSSE reads the response body as a stream of `data: {json}\n\n` frames
// and dispatches each parsed event to onEvent. A trailing partial frame (no
// terminating blank line) is intentionally dropped — the server always
// flushes complete frames, so anything left over is corruption we ignore.
async function consumeSSE(res, onEvent) {
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let idx;
    while ((idx = buf.indexOf('\n\n')) !== -1) {
      const frame = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      const line = frame.trim();
      if (!line.startsWith('data:')) continue;
      const payload = line.slice(5).trim();
      try {
        onEvent(JSON.parse(payload));
      } catch (e) {
        console.warn('bad sse frame', payload, e);
      }
    }
  }
}

function ensureURLRow(batch, index, fallbackUrl) {
  let row = batch.rowEls.get(index);
  if (row) return row;
  row = document.createElement('div');
  row.className = 'url-row status-pending';
  row.dataset.batch = String(batch.id);
  row.dataset.index = String(index);
  row.dataset.total = '0';
  row.innerHTML = `
    <div class="url-row-head">
      <span class="url-row-name">${esc(fallbackUrl || '')}</span>
      <span class="url-row-status">대기 중</span>
      <button class="url-row-cancel" type="button" aria-label="이 URL 취소" title="취소">✕</button>
    </div>
    <div class="url-progress-bar"><div class="url-progress-fill"></div></div>
  `;
  // CSS hides this on terminal states; clicks before that fire a per-URL
  // cancel against the server. We let SSE drive the visible state change so
  // a network failure cleanly leaves the button intact for retry.
  row.querySelector('.url-row-cancel')
    .addEventListener('click', () => cancelURLAt(batch, index));
  urlRows.appendChild(row);
  batch.rowEls.set(index, row);
  return row;
}

function setRowStatus(row, statusClass, statusText) {
  row.classList.remove('status-pending', 'status-downloading', 'status-done', 'status-error', 'status-cancelled');
  row.classList.add(statusClass);
  row.querySelector('.url-row-status').textContent = statusText;
}

function handleSSEEvent(batch, ev) {
  switch (ev.phase) {
    case 'register': {
      // First frame on POST responses — server hands us the jobId so a
      // refresh can rebind via GET /jobs/{id}/events. Restored batches
      // already have jobId from the bootstrap fetch and never see this
      // phase. Once we have jobId the per-batch cancel/dismiss controls
      // become meaningful, so refresh their visibility.
      if (!batch.jobId) batch.jobId = ev.jobId;
      updateBatchControls(batch);
      break;
    }
    case 'snapshot': {
      // First frame on EventSource subscriptions — re-apply server state
      // to every row. Idempotent: if bootstrapURLJobs already restored
      // the rows from GET /jobs, this just overwrites with the same
      // values. The job inside `ev` mirrors the JobSnapshot wire shape.
      applyJobSnapshotToBatch(batch, ev.job);
      // Race window: the job may have transitioned to terminal between
      // bootstrap's GET /jobs and our subscribe. The server's stream
      // closes cleanly in that case (Subscribe pre-closes the channel
      // for terminal jobs and never publishes summary), so without this
      // guard the browser's default EventSource auto-reconnect would
      // spin against a finished job every ~3s — wasting connections and
      // re-encoding the same snapshot indefinitely. Detect the terminal
      // status here and finalize locally just like the live summary
      // path would have.
      const status = ev.job && ev.job.status;
      if (status === 'completed' || status === 'failed' || status === 'cancelled') {
        if (batch.eventSource) {
          batch.eventSource.close();
          batch.eventSource = null;
        }
        if (!batch.done) {
          batch.done = true;
          updateBatchControls(batch);
          maybeFinalize();
          updateURLBadge();
        }
      }
      break;
    }
    case 'queued': {
      // Server accepted the POST but has not yet acquired the batch
      // semaphore — another batch is still running. Flip this batch's
      // rows to a distinct "waiting behind another batch" status so the
      // user isn't left staring at a stale generic "대기 중" forever.
      // The `start` event will overwrite this to "downloading" once the
      // semaphore clears.
      for (const row of batch.rowEls.values()) {
        setRowStatus(row, 'status-pending', '대기 중 (순서 대기)');
      }
      break;
    }
    case 'start': {
      const row = ensureURLRow(batch, ev.index, ev.url);
      row.querySelector('.url-row-name').textContent = ev.name || ev.url;
      const total = Number(ev.total) || 0;
      row.dataset.total = String(total);
      // HLS imports omit `total` (unknown length) — flip the row into
      // indeterminate mode so CSS runs the shuttle animation instead of
      // pinning the bar at 0%.
      row.classList.toggle('url-row-indeterminate', total === 0);
      const sizeText = total > 0 ? formatSize(total) : '크기 미상';
      const typePart = ev.type ? `${ev.type} · ` : '';
      setRowStatus(row, 'status-downloading', typePart + sizeText);
      break;
    }
    case 'progress': {
      const row = batch.rowEls.get(ev.index);
      if (!row) return;
      const total = Number(row.dataset.total) || 0;
      if (total > 0) {
        const pct = Math.min(100, (ev.received / total) * 100);
        row.querySelector('.url-progress-fill').style.width = pct.toFixed(1) + '%';
        row.querySelector('.url-row-status').textContent =
          `${formatSize(ev.received)} / ${formatSize(total)} · ${Math.floor(pct)}%`;
      } else {
        row.querySelector('.url-row-status').textContent = formatSize(ev.received);
      }
      break;
    }
    case 'done': {
      const row = ensureURLRow(batch, ev.index, ev.url);
      row.querySelector('.url-row-name').textContent = ev.name || ev.url;
      row.classList.remove('url-row-indeterminate');
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warn = (ev.warnings && ev.warnings.length > 0) ? ` · ${ev.warnings.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warn}`);
      batch.succeeded++;
      updateURLBadge();
      break;
    }
    case 'error': {
      const row = ensureURLRow(batch, ev.index, ev.url);
      row.classList.remove('url-row-indeterminate');
      // Cancellation is a deliberate user action, not a failure — render
      // it with the dedicated status-cancelled class (muted) and bump
      // the cancelled counter so summary text and badge reflect intent.
      if (ev.error === 'cancelled') {
        setRowStatus(row, 'status-cancelled', '취소됨');
        batch.cancelled = (batch.cancelled || 0) + 1;
      } else {
        const label = URL_ERROR_LABELS[ev.error] || ev.error || '알 수 없는 오류';
        setRowStatus(row, 'status-error', '실패 · ' + label);
        batch.failed++;
      }
      updateURLBadge();
      break;
    }
    case 'summary': {
      // Per-batch summary is no longer rendered directly — with multiple
      // batches possibly in flight, consecutive summaries would overwrite
      // each other unpredictably. maybeFinalize() aggregates across every
      // batch in the round once the last one settles.
      //
      // For EventSource-subscribed batches (restored via bootstrap or a
      // second tab) the POST submitURLImport finally never runs, so we
      // close the stream and finalize here instead. POST-driven batches
      // have no eventSource and fall through — their finally handles it.
      if (batch.eventSource) {
        batch.eventSource.close();
        batch.eventSource = null;
        batch.done = true;
        updateBatchControls(batch);
        maybeFinalize();
        updateURLBadge();
      }
      break;
    }
  }
}

function showURLError(msg) {
  urlError.textContent = msg;
  urlError.classList.remove('hidden');
}

// ── Background job bootstrap / fan-out subscription (Phase 20 J4) ────────────
// On every page load we ask the server which import jobs are alive, restore
// rows for them, and (for active jobs) attach an EventSource so live progress
// keeps flowing into the same UI the POST flow uses. A second tab opening
// the page sees the same jobs with no extra ceremony.

async function bootstrapURLJobs() {
  let body;
  try {
    const res = await fetch('/api/import-url/jobs');
    if (!res.ok) return;
    body = await res.json();
  } catch (e) {
    // Network or parse failure: silently fall through. The user sees no
    // restored progress, but the rest of the app still works.
    console.warn('bootstrapURLJobs failed', e);
    return;
  }
  const active = Array.isArray(body.active) ? body.active : [];
  const finishedAll = Array.isArray(body.finished) ? body.finished : [];
  // Soft cap on restored history. Server keeps every dismissed-but-not-
  // cleared job until restart (single-user, unbounded growth tolerated by
  // the spec); the client renders only the most recent N so a long-running
  // browser session can't bloat the modal DOM. Active jobs are never
  // capped — they're still in flight and dropping any would lose UI.
  const HISTORY_CAP = 50;
  const finished = finishedAll.length > HISTORY_CAP
    ? finishedAll.slice(finishedAll.length - HISTORY_CAP)
    : finishedAll;
  if (active.length === 0 && finished.length === 0) return;

  // Render finished first so they sit at the top of the result area —
  // active progress rows naturally land below as the user-current focus.
  for (const job of finished) restoreJobBatch(job, false);
  for (const job of active) restoreJobBatch(job, true);

  if (urlBatches.length > 0) {
    urlResult.classList.remove('hidden');
  }
  updateConfirmButton();
  updateURLBadge();
}

// restoreJobBatch builds a batch from a server JobSnapshot and renders one
// row per URL with the correct status/progress already applied. Restored
// batches carry restored=true so they don't fold into the current session's
// summary aggregation (see maybeFinalize) — they are backdrop history that
// the user can dismiss when they want.
function restoreJobBatch(jobSnap, isActive) {
  const batch = {
    id: ++urlBatchSeq,
    jobId: jobSnap.id,
    rowEls: new Map(),
    headerEl: null,
    succeeded: 0,
    failed: 0,
    cancelled: 0,
    total: jobSnap.urls.length,
    done: !isActive,
    restored: true,
    eventSource: null,
  };
  urlBatches.push(batch);

  // Synthetic tag distinguishes restored rows from freshly submitted ones.
  appendBatchHeader(batch, isActive ? '복원된 배치' : '이전 결과');

  jobSnap.urls.forEach((u, i) => {
    const row = ensureURLRow(batch, i, u.url);
    applyURLStateToRow(row, u);
    if (u.status === 'done')           batch.succeeded++;
    else if (u.status === 'error')     batch.failed++;
    else if (u.status === 'cancelled') batch.cancelled++;
  });

  if (isActive) subscribeToJob(batch);
}

// applyURLStateToRow reflects a server URLState onto an existing DOM row.
// Used both by the bootstrap restore path and by the EventSource snapshot
// frame for late subscribers — the operation is idempotent.
function applyURLStateToRow(row, u) {
  row.querySelector('.url-row-name').textContent = u.name || u.url;
  const total = Number(u.total) || 0;
  row.dataset.total = String(total);
  row.classList.toggle('url-row-indeterminate', total === 0 && u.status === 'running');
  const fill = row.querySelector('.url-progress-fill');
  switch (u.status) {
    case 'pending':
      setRowStatus(row, 'status-pending', '대기 중');
      fill.style.width = '0%';
      break;
    case 'running': {
      if (total > 0) {
        const received = Number(u.received) || 0;
        const pct = Math.min(100, (received / total) * 100);
        fill.style.width = pct.toFixed(1) + '%';
        setRowStatus(row, 'status-downloading',
          `${formatSize(received)} / ${formatSize(total)} · ${Math.floor(pct)}%`);
      } else {
        setRowStatus(row, 'status-downloading', formatSize(Number(u.received) || 0));
      }
      break;
    }
    case 'done': {
      fill.style.width = '100%';
      const warn = (u.warnings && u.warnings.length > 0) ? ` · ${u.warnings.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(Number(u.received) || 0)})${warn}`);
      break;
    }
    case 'cancelled':
      setRowStatus(row, 'status-cancelled', '취소됨');
      break;
    case 'error': {
      const label = URL_ERROR_LABELS[u.error] || u.error || '알 수 없는 오류';
      setRowStatus(row, 'status-error', '실패 · ' + label);
      break;
    }
  }
}

// applyJobSnapshotToBatch is the EventSource analogue: re-apply the server
// snapshot to every row in the batch AND recompute the batch's terminal
// counters. The recompute is essential — the snapshot is authoritative,
// and a job that finished between bootstrap's GET /jobs and our subscribe
// would otherwise leave succeeded/failed/cancelled at the bootstrap-time
// values, undercounting the badge and summary aggregations downstream.
function applyJobSnapshotToBatch(batch, job) {
  if (!job || !Array.isArray(job.urls)) return;
  let succeeded = 0, failed = 0, cancelled = 0;
  job.urls.forEach((u, i) => {
    const row = ensureURLRow(batch, i, u.url);
    applyURLStateToRow(row, u);
    if (u.status === 'done')           succeeded++;
    else if (u.status === 'error')     failed++;
    else if (u.status === 'cancelled') cancelled++;
  });
  batch.succeeded = succeeded;
  batch.failed = failed;
  batch.cancelled = cancelled;
  // Invariant: URL count is fixed at job Create time. If a future server
  // change ever lets jobs grow URLs mid-flight, the row map would silently
  // hold orphans. Surface that here so the regression is visible in the
  // console rather than as a confusing UI glitch.
  if (batch.rowEls.size !== job.urls.length) {
    console.warn('row count mismatch: rows=%d snapshot.urls=%d (jobId=%s)',
      batch.rowEls.size, job.urls.length, batch.jobId);
  }
}

// removeBatchRows tears down every DOM contribution this batch made — the
// per-URL rows and the header. Used by HTTP-error rollback, dismiss, and
// the J5 removed phase handler.
function removeBatchRows(batch) {
  for (const row of batch.rowEls.values()) row.remove();
  batch.rowEls.clear();
  if (batch.headerEl) {
    batch.headerEl.remove();
    batch.headerEl = null;
  }
}

// appendBatchHeader creates the per-batch label + control bar and inserts
// it ahead of the rows. Always called once per batch (POST or restored) so
// the user has a consistent place to issue batch-level cancel / dismiss.
function appendBatchHeader(batch, label) {
  const header = document.createElement('div');
  header.className = 'url-batch-header';
  header.dataset.batch = String(batch.id);

  const labelEl = document.createElement('span');
  labelEl.className = 'url-batch-label';
  labelEl.textContent = label || '';
  header.appendChild(labelEl);

  const actions = document.createElement('span');
  actions.className = 'url-batch-actions';

  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.className = 'url-batch-cancel-all hidden';
  cancelBtn.textContent = '전체 취소';
  cancelBtn.addEventListener('click', () => cancelBatchAll(batch));
  actions.appendChild(cancelBtn);

  const dismissBtn = document.createElement('button');
  dismissBtn.type = 'button';
  dismissBtn.className = 'url-batch-dismiss hidden';
  dismissBtn.textContent = '닫기';
  dismissBtn.addEventListener('click', () => dismissBatch(batch));
  actions.appendChild(dismissBtn);

  header.appendChild(actions);
  urlRows.appendChild(header);
  batch.headerEl = header;
  updateBatchControls(batch);
}

// updateBatchControls flips the cancel/dismiss buttons based on whether
// the batch is still active. Called from header creation, summary, and
// HTTP-error finalize paths so the visible state never lags reality.
function updateBatchControls(batch) {
  if (!batch.headerEl) return;
  const cancelBtn = batch.headerEl.querySelector('.url-batch-cancel-all');
  const dismissBtn = batch.headerEl.querySelector('.url-batch-dismiss');
  // Without a server-issued jobId we have nothing to call — hide both
  // controls. The POST flow lands jobId in `register`, so the gap is at
  // most a single round-trip.
  const hasJobId = !!batch.jobId;
  cancelBtn.classList.toggle('hidden', batch.done || !hasJobId);
  dismissBtn.classList.toggle('hidden', !batch.done || !hasJobId);
}

// cancelURLAt fires a per-URL cancel against the server. The visible row
// state updates via the SSE error("cancelled") frame the worker emits in
// response — keeping a single source of truth for state transitions.
async function cancelURLAt(batch, index) {
  if (!batch.jobId) return;
  try {
    await fetch(
      '/api/import-url/jobs/' + encodeURIComponent(batch.jobId) +
      '/cancel?index=' + encodeURIComponent(String(index)),
      { method: 'POST' });
  } catch (e) {
    console.warn('cancel url failed', e);
  }
}

async function cancelBatchAll(batch) {
  if (!batch.jobId) return;
  try {
    await fetch(
      '/api/import-url/jobs/' + encodeURIComponent(batch.jobId) + '/cancel',
      { method: 'POST' });
  } catch (e) {
    console.warn('cancel batch failed', e);
  }
}

async function dismissBatch(batch) {
  if (!batch.jobId || !batch.done) return;
  try {
    const res = await fetch(
      '/api/import-url/jobs/' + encodeURIComponent(batch.jobId),
      { method: 'DELETE' });
    if (!res.ok) return;
  } catch (e) {
    console.warn('dismiss failed', e);
    return;
  }
  removeBatchRows(batch);
  if (batch.eventSource) {
    batch.eventSource.close();
    batch.eventSource = null;
  }
  const idx = urlBatches.indexOf(batch);
  if (idx !== -1) urlBatches.splice(idx, 1);
  updateURLBadge();
}

async function dismissAllFinishedBatches() {
  try {
    const res = await fetch('/api/import-url/jobs?status=finished',
      { method: 'DELETE' });
    if (!res.ok) return;
  } catch (e) {
    console.warn('dismiss-all failed', e);
    return;
  }
  // Server tore down every terminal job — mirror that locally. Active
  // batches stay (they were already excluded by the filter).
  const remaining = [];
  for (const batch of urlBatches) {
    if (batch.done) {
      removeBatchRows(batch);
      if (batch.eventSource) {
        batch.eventSource.close();
        batch.eventSource = null;
      }
      continue;
    }
    remaining.push(batch);
  }
  urlBatches.length = 0;
  urlBatches.push(...remaining);
  updateURLBadge();
}

// subscribeToJob opens an EventSource against /api/import-url/jobs/{id}/events
// and routes every frame through the existing handleSSEEvent path. The
// EventSource is closed explicitly on summary so the auto-reconnect default
// does not waste a round-trip on a finished job.
function subscribeToJob(batch) {
  if (!batch.jobId) return;
  const es = new EventSource('/api/import-url/jobs/' + encodeURIComponent(batch.jobId) + '/events');
  batch.eventSource = es;
  es.onmessage = e => {
    let ev;
    try { ev = JSON.parse(e.data); }
    catch (err) { console.warn('bad sse frame', e.data, err); return; }
    handleSSEEvent(batch, ev);
  };
  es.onerror = () => {
    // EventSource auto-reconnects on transient failures. We close
    // explicitly only after summary was processed (in handleSSEEvent),
    // so an onerror here means a real network drop. Let the browser
    // retry; if the server is back we resync via the snapshot frame.
  };
}

// ── TS → MP4 변환 ─────────────────────────────────────────────────────────────
const CONVERT_ERROR_LABELS = {
  invalid_path: '잘못된 경로',
  not_found: '파일 없음',
  not_a_file: '파일이 아님',
  not_ts: 'TS 파일이 아님',
  already_exists: '같은 이름의 MP4 존재',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  ffmpeg_error: '변환 실패 (손상되었거나 비호환 코덱)',
  convert_timeout: '타임아웃 (10분)',
  canceled: '취소됨',
  write_error: '저장 실패',
};

let convertSubmitting = false;
let convertAnySucceeded = false;
let convertPaths = [];
let convertAbort = null;

convertAllBtn.addEventListener('click', () => {
  const paths = visibleTSPaths(applyView(allEntries));
  if (paths.length === 0) return;
  openConvertModal(paths);
});
convertCancelBtn.addEventListener('click', closeConvertModal);
convertConfirmBtn.addEventListener('click', submitConvert);
convertModal.addEventListener('click', e => { if (e.target === convertModal) closeConvertModal(); });
document.addEventListener('keydown', e => {
  if (convertModal.classList.contains('hidden')) return;
  if (e.key === 'Escape') closeConvertModal();
});

function openConvertModal(paths) {
  convertPaths = paths.slice();
  convertError.textContent = '';
  convertError.classList.add('hidden');
  convertRows.innerHTML = '';
  convertSummary.textContent = '';
  convertSummary.className = 'url-summary hidden';
  convertResult.classList.add('hidden');
  convertDeleteOrig.checked = false;
  convertDeleteOrig.disabled = false;
  convertConfirmBtn.disabled = false;
  convertConfirmBtn.textContent = '시작';
  convertAnySucceeded = false;
  // Build the list preview so the user sees exactly what will run.
  convertFileList.innerHTML = paths
    .map(p => `<li>${esc(p)}</li>`)
    .join('');
  convertModal.classList.remove('hidden');
}

function closeConvertModal() {
  if (convertSubmitting && convertAbort) {
    // Client disconnect flows to the handler as r.Context() cancel → backend
    // kills ffmpeg and cleans the temp file.
    convertAbort.abort();
  }
  convertModal.classList.add('hidden');
  if (convertAnySucceeded) {
    convertAnySucceeded = false;
    browse(currentPath, false);
  }
}

async function submitConvert() {
  if (convertSubmitting) return;
  if (convertPaths.length === 0) {
    showConvertError('변환할 파일이 없습니다.');
    return;
  }

  convertError.classList.add('hidden');
  convertRows.innerHTML = '';
  convertSummary.textContent = '';
  convertSummary.className = 'url-summary hidden';
  convertResult.classList.remove('hidden');
  // Pre-create one pending row per path so users see immediate feedback.
  convertPaths.forEach((p, i) => ensureConvertRow(i, p));
  convertSubmitting = true;
  convertConfirmBtn.disabled = true;
  convertConfirmBtn.textContent = '변환 중...';
  convertDeleteOrig.disabled = true;
  convertAbort = new AbortController();

  try {
    const res = await fetch('/api/convert', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({
        paths: convertPaths,
        delete_original: convertDeleteOrig.checked,
      }),
      signal: convertAbort.signal,
    });
    if (!res.ok) {
      let msg = '';
      try { msg = (await res.json()).error || ''; } catch { /* not JSON */ }
      if (!msg) msg = `요청 실패 (${res.status})`;
      showConvertError(msg);
      convertResult.classList.add('hidden');
      return;
    }
    await consumeSSE(res, handleConvertSSEEvent);
  } catch (e) {
    if (e.name !== 'AbortError') {
      showConvertError('요청 실패: ' + e.message);
    }
  } finally {
    convertSubmitting = false;
    convertAbort = null;
    convertConfirmBtn.disabled = false;
    convertConfirmBtn.textContent = '시작';
    // Leave the delete checkbox disabled after a run so the user re-opens the
    // modal (fresh state) rather than re-submitting the same list.
  }
}

function ensureConvertRow(index, fallbackPath) {
  let row = convertRows.querySelector(`[data-index="${index}"]`);
  if (row) return row;
  row = document.createElement('div');
  row.className = 'url-row status-pending';
  row.dataset.index = String(index);
  row.dataset.total = '0';
  row.innerHTML = `
    <div class="url-row-head">
      <span class="url-row-name">${esc(fallbackPath || '')}</span>
      <span class="url-row-status">대기 중</span>
    </div>
    <div class="url-progress-bar"><div class="url-progress-fill"></div></div>
  `;
  convertRows.appendChild(row);
  return row;
}

function handleConvertSSEEvent(ev) {
  switch (ev.phase) {
    case 'start': {
      const row = ensureConvertRow(ev.index, ev.path);
      row.querySelector('.url-row-name').textContent = ev.name || ev.path;
      const total = Number(ev.total) || 0;
      row.dataset.total = String(total);
      const sizeText = total > 0 ? formatSize(total) : '크기 미상';
      setRowStatus(row, 'status-downloading', '변환 중 · ' + sizeText);
      break;
    }
    case 'progress': {
      const row = convertRows.querySelector(`[data-index="${ev.index}"]`);
      if (!row) return;
      const total = Number(row.dataset.total) || 0;
      if (total > 0) {
        // Output MP4 is ≈ src size for stream-copy remux, so received/total
        // is a reasonable progress proxy (not exact).
        const pct = Math.min(100, (ev.received / total) * 100);
        row.querySelector('.url-progress-fill').style.width = pct.toFixed(1) + '%';
        row.querySelector('.url-row-status').textContent =
          `${formatSize(ev.received)} / ${formatSize(total)} · ${Math.floor(pct)}%`;
      } else {
        row.querySelector('.url-row-status').textContent = formatSize(ev.received);
      }
      break;
    }
    case 'done': {
      const row = ensureConvertRow(ev.index, ev.path);
      row.querySelector('.url-row-name').textContent = ev.name || ev.path;
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warns = (ev.warnings || []).map(w =>
        w === 'delete_original_failed' ? '원본 삭제 실패' : w
      );
      const warnText = warns.length ? ` · ${warns.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warnText}`);
      convertAnySucceeded = true;
      break;
    }
    case 'error': {
      const row = ensureConvertRow(ev.index, ev.path);
      const label = CONVERT_ERROR_LABELS[ev.error] || ev.error || '알 수 없는 오류';
      setRowStatus(row, 'status-error', '실패 · ' + label);
      break;
    }
    case 'summary': {
      const cls = ev.failed === 0 ? 'status-done'
                : ev.succeeded === 0 ? 'status-error'
                : 'status-mixed';
      convertSummary.className = 'url-summary ' + cls;
      convertSummary.textContent = `성공 ${ev.succeeded}개 · 실패 ${ev.failed}개`;
      break;
    }
  }
}

function showConvertError(msg) {
  convertError.textContent = msg;
  convertError.classList.remove('hidden');
}

// ── Delete ────────────────────────────────────────────────────────────────────
async function deleteFile(path) {
  if (!confirm(`삭제하시겠습니까?\n${path}`)) return;
  const res = await fetch('/api/file?path=' + encodeURIComponent(path), { method: 'DELETE' });
  if (res.ok) {
    browse(currentPath, false);
  } else {
    alert('삭제 실패');
  }
}

async function deleteFolder(path) {
  if (!confirm(`폴더 안의 모든 파일이 삭제됩니다.\n${path}\n\n계속하시겠습니까?`)) return;
  const res = await fetch('/api/folder?path=' + encodeURIComponent(path), { method: 'DELETE' });
  if (res.ok) {
    browse(currentPath, false);
    loadTree();
  } else {
    alert('폴더 삭제 실패');
  }
}

// ── Drag and Drop (file move) ────────────────────────────────────────────────
function parentDir(p) {
  if (!p || p === '/') return '/';
  const i = p.lastIndexOf('/');
  return i <= 0 ? '/' : p.substring(0, i);
}

function isInternalMove(e) {
  return Array.from(e.dataTransfer.types).includes(DND_MIME);
}

function attachDragHandlers(el, entry) {
  el.draggable = true;
  el.addEventListener('dragstart', e => {
    dragSrcPath = entry.path;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData(DND_MIME, JSON.stringify({ src: entry.path }));
    // Firefox won't initiate a drag without text/plain or text/uri-list set.
    e.dataTransfer.setData('text/plain', entry.path);
    el.classList.add('dragging');
  });
  el.addEventListener('dragend', () => {
    dragSrcPath = null;
    el.classList.remove('dragging');
  });
}

function attachDropHandlers(el, destPath) {
  el.addEventListener('dragenter', e => {
    if (!isInternalMove(e)) return;
    if (dragSrcPath && parentDir(dragSrcPath) === destPath) return;
    e.preventDefault();
    el.classList.add('drop-target');
  });
  el.addEventListener('dragover', e => {
    if (!isInternalMove(e)) return;
    if (dragSrcPath && parentDir(dragSrcPath) === destPath) {
      e.dataTransfer.dropEffect = 'none';
      return;
    }
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  });
  el.addEventListener('dragleave', () => {
    el.classList.remove('drop-target');
  });
  el.addEventListener('drop', e => {
    if (!isInternalMove(e)) return;
    e.preventDefault();
    e.stopPropagation();
    el.classList.remove('drop-target');
    let payload;
    try {
      payload = JSON.parse(e.dataTransfer.getData(DND_MIME));
    } catch {
      return;
    }
    if (!payload || !payload.src) return;
    if (parentDir(payload.src) === destPath) return; // defensive — also blocked by backend
    moveFile(payload.src, destPath);
  });
}

async function moveFile(srcPath, destDir) {
  try {
    const res = await fetch('/api/file?path=' + encodeURIComponent(srcPath), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ to: destDir }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      alert('이동 실패: ' + (data.error || res.statusText));
      return;
    }
    // Folder structure unchanged on file move; only the listing needs a refresh.
    browse(currentPath, false);
  } catch (e) {
    alert('이동 실패: ' + e.message);
  }
}

// ── Rename ────────────────────────────────────────────────────────────────────
let renameTarget = null;
let renameSubmitting = false;

renameCancelBtn.addEventListener('click', closeRenameModal);
renameModal.addEventListener('click', e => { if (e.target === renameModal) closeRenameModal(); });
renameConfirmBtn.addEventListener('click', submitRename);
renameInput.addEventListener('keydown', e => {
  if (e.key === 'Enter') submitRename();
  if (e.key === 'Escape') closeRenameModal();
});

function splitExtension(name) {
  // Mirror the server: filepath.Ext returns the final ".ext" or "".
  // Folder names or extension-less files have ext = ''.
  const dot = name.lastIndexOf('.');
  if (dot <= 0 || dot === name.length - 1) return { base: name, ext: '' };
  return { base: name.slice(0, dot), ext: name.slice(dot) };
}

function openRenameModal(entry) {
  renameTarget = entry;
  renameError.textContent = '';
  renameError.classList.add('hidden');

  const { base, ext } = entry.is_dir ? { base: entry.name, ext: '' } : splitExtension(entry.name);
  renameTitle.textContent = entry.is_dir ? '폴더 이름 변경' : '파일 이름 변경';
  if (ext) {
    renameHint.textContent = `확장자: ${ext} (변경 불가)`;
    renameHint.classList.remove('hidden');
  } else {
    renameHint.classList.add('hidden');
  }
  renameInput.value = base;
  renameModal.classList.remove('hidden');
  renameInput.focus();
  renameInput.select();
}

function closeRenameModal() {
  renameModal.classList.add('hidden');
  renameTarget = null;
}

async function submitRename() {
  if (renameSubmitting || !renameTarget) return;
  const newBase = renameInput.value.trim();
  if (!newBase) {
    showRenameError('이름을 입력하세요.');
    return;
  }
  const entry = renameTarget;
  const url = entry.is_dir
    ? '/api/folder?path=' + encodeURIComponent(entry.path)
    : '/api/file?path=' + encodeURIComponent(entry.path);
  renameSubmitting = true;
  renameConfirmBtn.disabled = true;
  try {
    const res = await fetch(url, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: newBase }),
    });
    if (res.ok) {
      closeRenameModal();
      if (entry.is_dir) {
        const data = await res.json().catch(() => null);
        const newPath = data && data.path ? data.path : entry.path;
        // If the renamed folder is currentPath or an ancestor of it, the
        // browser is sitting on a now-defunct URL — rewrite to the new prefix.
        const target = rewritePathAfterFolderRename(entry.path, newPath, currentPath);
        if (target !== currentPath) {
          browse(target);
        } else {
          browse(currentPath, false);
        }
        loadTree();
      } else {
        browse(currentPath, false);
      }
      return;
    }
    const err = await res.json().catch(() => ({}));
    if (res.status === 409) {
      showRenameError('이미 같은 이름이 있습니다.');
    } else if (res.status === 400 && err.error === 'name unchanged') {
      showRenameError('이름이 같습니다.');
    } else if (res.status === 404) {
      showRenameError('대상을 찾을 수 없습니다.');
    } else {
      showRenameError('유효하지 않은 이름입니다.');
    }
  } finally {
    renameSubmitting = false;
    renameConfirmBtn.disabled = false;
  }
}

function showRenameError(msg) {
  renameError.textContent = msg;
  renameError.classList.remove('hidden');
}

function rewritePathAfterFolderRename(oldPath, newPath, current) {
  if (current === oldPath) return newPath;
  if (current.startsWith(oldPath + '/')) {
    return newPath + current.substring(oldPath.length);
  }
  return current;
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function iconFor(type, isDir) {
  if (isDir) return '📁';
  if (type === 'image') return '🖼';
  if (type === 'video') return '🎬';
  if (type === 'audio') return '🎵';
  return '📄';
}

function formatSize(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

// YouTube-style duration: <1h → "M:SS", ≥1h → "H:MM:SS".
// Returns null when seconds is unknown or non-positive so callers can skip rendering.
function formatDuration(sec) {
  if (sec == null || !Number.isFinite(sec) || sec <= 0) return null;
  const total = Math.floor(sec);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, '0');
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${ss}`;
  return `${m}:${ss}`;
}

function esc(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// ── Folder Tree (sidebar) ────────────────────────────────────────────────────
async function loadTree() {
  treeRoot.setAttribute('aria-busy', 'true');
  treeRoot.innerHTML = '<div class="tree-empty">로딩 중...</div>';
  try {
    const res = await fetch(`/api/tree?path=/&depth=${TREE_INIT_DEPTH}`);
    if (!res.ok) throw new Error(await res.text());
    const root = await res.json();
    treeRoot.innerHTML = '';
    if (!root.has_children) {
      treeRoot.innerHTML = '<div class="tree-empty">폴더가 없습니다.</div>';
      return;
    }
    renderTreeChildren(root.children, treeRoot, 0);
    highlightTreeCurrent();
  } catch (e) {
    showTreeError(e.message);
  } finally {
    treeRoot.setAttribute('aria-busy', 'false');
  }
}

function showTreeError(message) {
  treeRoot.innerHTML = '';
  const wrap = document.createElement('div');
  wrap.className = 'tree-error';
  wrap.setAttribute('role', 'alert');

  const text = document.createElement('span');
  text.textContent = `트리 로드 실패: ${message}`;
  wrap.appendChild(text);

  const retry = document.createElement('button');
  retry.type = 'button';
  retry.className = 'tree-retry';
  retry.textContent = '다시 시도';
  retry.addEventListener('click', loadTree);
  wrap.appendChild(retry);

  treeRoot.appendChild(wrap);
}

function renderTreeChildren(children, container, depth) {
  if (!children) return;
  children.forEach(node => container.appendChild(buildTreeNode(node, depth)));
}

function buildTreeNode(node, depth) {
  const wrap = document.createElement('div');
  wrap.className = 'tree-node';
  wrap.dataset.path = node.path;

  const row = document.createElement('div');
  row.className = 'tree-node-row';
  row.style.paddingLeft = (depth * 14 + 6) + 'px';

  const chevron = document.createElement('button');
  chevron.className = 'tree-chevron';
  chevron.type = 'button';
  if (node.has_children) {
    const expanded = node.children !== null;
    chevron.textContent = expanded ? '▼' : '▶';
    chevron.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    chevron.addEventListener('click', e => {
      e.stopPropagation();
      toggleNode(wrap, node, depth);
    });
  } else {
    chevron.textContent = '·';
    chevron.disabled = true;
  }

  const label = document.createElement('button');
  label.className = 'tree-label';
  label.type = 'button';
  label.textContent = node.name;
  label.title = node.path;
  label.addEventListener('click', () => {
    browse(node.path);
    if (window.matchMedia('(max-width: 600px)').matches) {
      setSidebarOpen(false);
    }
  });

  const renameBtn = document.createElement('button');
  renameBtn.className = 'tree-rename';
  renameBtn.type = 'button';
  renameBtn.title = '이름 변경';
  renameBtn.setAttribute('aria-label', `${node.name} 이름 변경`);
  renameBtn.textContent = '✎';
  renameBtn.addEventListener('click', e => {
    e.stopPropagation();
    openRenameModal({ name: node.name, path: node.path, is_dir: true });
  });

  row.appendChild(chevron);
  row.appendChild(label);
  row.appendChild(renameBtn);
  wrap.appendChild(row);
  attachDropHandlers(row, node.path);

  const kids = document.createElement('div');
  kids.className = 'tree-children';
  if (node.children !== null) {
    renderTreeChildren(node.children, kids, depth + 1);
  } else {
    kids.classList.add('collapsed'); // not loaded yet
  }
  wrap.appendChild(kids);
  return wrap;
}

async function toggleNode(wrapEl, node, depth) {
  const kids = wrapEl.querySelector(':scope > .tree-children');
  const chevron = wrapEl.querySelector(':scope > .tree-node-row > .tree-chevron');
  const collapsed = kids.classList.contains('collapsed');

  // First-time expand of a not-yet-loaded subtree: fetch one level.
  if (collapsed && kids.childElementCount === 0) {
    chevron.textContent = '…';
    try {
      const res = await fetch(`/api/tree?path=${encodeURIComponent(node.path)}&depth=1`);
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      renderTreeChildren(data.children, kids, depth + 1);
      highlightTreeCurrent();
    } catch (e) {
      chevron.textContent = '▶';
      alert('하위 폴더 로드 실패: ' + e.message);
      return;
    }
  }

  if (collapsed) {
    kids.classList.remove('collapsed');
    chevron.textContent = '▼';
    chevron.setAttribute('aria-expanded', 'true');
  } else {
    kids.classList.add('collapsed');
    chevron.textContent = '▶';
    chevron.setAttribute('aria-expanded', 'false');
  }
}

function highlightTreeCurrent() {
  treeRoot.querySelectorAll('.tree-node-row.active')
    .forEach(el => el.classList.remove('active'));
  if (currentPath === '/' || !currentPath) return;
  // CSS.escape handles slashes/quotes safely; required for arbitrary paths.
  const sel = `.tree-node[data-path="${CSS.escape(currentPath)}"] > .tree-node-row`;
  const target = treeRoot.querySelector(sel);
  if (target) target.classList.add('active');
}

// ── Sidebar toggle (mobile) ──────────────────────────────────────────────────
function setSidebarOpen(open) {
  document.body.classList.toggle('sidebar-open', open);
  sidebarToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  sidebarToggle.setAttribute('aria-label', open ? '폴더 메뉴 닫기' : '폴더 메뉴 열기');
  sidebarBackdrop.classList.toggle('hidden', !open);
}

sidebarToggle.addEventListener('click', () => {
  setSidebarOpen(!document.body.classList.contains('sidebar-open'));
});
sidebarBackdrop.addEventListener('click', () => setSidebarOpen(false));

// ── Toolbar (sort/filter) ────────────────────────────────────────────────────
typeButtons.forEach(btn => {
  btn.addEventListener('click', () => {
    if (view.type === btn.dataset.type) return;
    view.type = btn.dataset.type;
    syncURL(false);
    syncToolbarUI();
    renderView();
  });
});
toolbarSearch.addEventListener('input', (e) => {
  view.q = e.target.value.trim();
  syncURL(false);
  renderView();
});
toolbarSort.addEventListener('change', (e) => {
  view.sort = e.target.value;
  syncURL(false);
  renderView();
});

// ── Settings modal (⚙) ───────────────────────────────────────────────────────
const settingsBtn       = document.getElementById('settings-btn');
const settingsModal     = document.getElementById('settings-modal');
const settingsMaxInput  = document.getElementById('settings-max-mib');
const settingsMaxHint   = document.getElementById('settings-max-hint');
const settingsTimeInput = document.getElementById('settings-timeout-min');
const settingsError     = document.getElementById('settings-error');
const settingsCancelBtn = document.getElementById('settings-cancel-btn');
const settingsConfirmBtn = document.getElementById('settings-confirm-btn');

// Client-side bounds mirror the server's — they keep a typo from ever hitting
// the backend, but the server revalidates regardless (defense in depth).
const SETTINGS_MAX_MIB_MIN = 1;
const SETTINGS_MAX_MIB_MAX = 1024 * 1024; // 1 TiB
const SETTINGS_TIMEOUT_MIN = 1;
const SETTINGS_TIMEOUT_MAX = 240;
const SETTINGS_FIELD_LABELS = {
  url_import_max_bytes: '최대 다운로드 크기',
  url_import_timeout_seconds: '다운로드 타임아웃',
};

settingsBtn.addEventListener('click', openSettingsModal);
settingsCancelBtn.addEventListener('click', closeSettingsModal);
settingsConfirmBtn.addEventListener('click', submitSettings);
settingsModal.addEventListener('click', e => { if (e.target === settingsModal) closeSettingsModal(); });
settingsMaxInput.addEventListener('input', updateSettingsMaxHint);
document.addEventListener('keydown', e => {
  if (settingsModal.classList.contains('hidden')) return;
  if (e.key === 'Escape') closeSettingsModal();
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submitSettings(); }
});

async function openSettingsModal() {
  settingsError.textContent = '';
  settingsError.classList.add('hidden');
  settingsMaxInput.value = '';
  settingsTimeInput.value = '';
  settingsMaxHint.textContent = '';
  settingsModal.classList.remove('hidden');
  settingsMaxInput.focus();

  try {
    const res = await fetch('/api/settings');
    if (!res.ok) throw new Error('status ' + res.status);
    const cur = await res.json();
    settingsMaxInput.value = Math.round(cur.url_import_max_bytes / (1024 * 1024));
    settingsTimeInput.value = Math.round(cur.url_import_timeout_seconds / 60);
    updateSettingsMaxHint();
  } catch (e) {
    showSettingsError('설정을 불러오지 못했습니다: ' + e.message);
  }
}

function closeSettingsModal() {
  settingsModal.classList.add('hidden');
}

function updateSettingsMaxHint() {
  const mib = parseInt(settingsMaxInput.value, 10);
  if (!Number.isFinite(mib) || mib <= 0) {
    settingsMaxHint.textContent = '';
    return;
  }
  const gib = mib / 1024;
  // Show MiB as-is for sub-GiB values, GiB with one decimal otherwise.
  settingsMaxHint.textContent = gib < 1
    ? `≈ ${mib} MiB`
    : `≈ ${gib.toFixed(gib >= 10 ? 0 : 1)} GiB`;
}

async function submitSettings() {
  const mib = parseInt(settingsMaxInput.value, 10);
  const minutes = parseInt(settingsTimeInput.value, 10);
  if (!Number.isInteger(mib) || mib < SETTINGS_MAX_MIB_MIN || mib > SETTINGS_MAX_MIB_MAX) {
    showSettingsError(`최대 다운로드 크기는 ${SETTINGS_MAX_MIB_MIN}~${SETTINGS_MAX_MIB_MAX} MiB 범위여야 합니다.`);
    settingsMaxInput.focus();
    return;
  }
  if (!Number.isInteger(minutes) || minutes < SETTINGS_TIMEOUT_MIN || minutes > SETTINGS_TIMEOUT_MAX) {
    showSettingsError(`타임아웃은 ${SETTINGS_TIMEOUT_MIN}~${SETTINGS_TIMEOUT_MAX} 분 범위여야 합니다.`);
    settingsTimeInput.focus();
    return;
  }

  settingsError.classList.add('hidden');
  settingsConfirmBtn.disabled = true;
  settingsConfirmBtn.textContent = '저장 중...';
  try {
    const payload = {
      url_import_max_bytes: mib * 1024 * 1024,
      url_import_timeout_seconds: minutes * 60,
    };
    const res = await fetch('/api/settings', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!res.ok) {
      let msg = `저장 실패 (${res.status})`;
      try {
        const body = await res.json();
        if (body.error === 'out_of_range' && body.field) {
          const label = SETTINGS_FIELD_LABELS[body.field] || body.field;
          msg = `${label} 값이 허용 범위를 벗어났습니다.`;
        } else if (body.error) {
          msg = body.error;
        }
      } catch { /* not JSON */ }
      showSettingsError(msg);
      return;
    }
    closeSettingsModal();
  } catch (e) {
    showSettingsError('저장 실패: ' + e.message);
  } finally {
    settingsConfirmBtn.disabled = false;
    settingsConfirmBtn.textContent = '저장';
  }
}

function showSettingsError(msg) {
  settingsError.textContent = msg;
  settingsError.classList.remove('hidden');
}

// ── Init ──────────────────────────────────────────────────────────────────────
readViewFromURL();
syncToolbarUI();
const initPath = new URLSearchParams(location.search).get('path') || '/';
browse(initPath, false);
loadTree();
// Restore in-progress URL imports from the server (Phase 20 J4). Independent
// of browse/tree — safe to fire-and-forget; the badge appears asynchronously
// when the response arrives.
bootstrapURLJobs();
