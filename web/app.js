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

    card.innerHTML = `
      <img src="${esc(thumbSrc)}" alt="${esc(entry.name)}" loading="lazy">
      <div class="thumb-name">${esc(entry.name)}</div>
      <span class="size-badge">${esc(formatSize(entry.size))}</span>
      ${durBadge}
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
const URL_ERROR_LABELS = {
  missing_content_length: 'Content-Length 헤더 없음',
  too_large: '2GB 초과',
  unsupported_content_type: '지원하지 않는 미디어 타입',
  invalid_scheme: '지원하지 않는 스킴',
  invalid_url: '잘못된 URL',
  http_error: 'HTTP 응답 에러',
  connect_timeout: '연결 타임아웃',
  download_timeout: '다운로드 타임아웃 (10분)',
  tls_error: 'TLS 검증 실패',
  too_many_redirects: '리다이렉트 과다',
  network_error: '네트워크 오류',
  write_error: '저장 실패',
  ffmpeg_error: 'HLS 리먹싱 실패',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  hls_playlist_too_large: 'HLS 플레이리스트 크기 초과',
};

let urlSubmitting = false;
let urlAnySucceeded = false;

urlImportBtn.addEventListener('click', openURLModal);
urlCancelBtn.addEventListener('click', closeURLModal);
urlConfirmBtn.addEventListener('click', submitURLImport);
urlModal.addEventListener('click', e => { if (e.target === urlModal) closeURLModal(); });
document.addEventListener('keydown', e => {
  if (urlModal.classList.contains('hidden')) return;
  if (e.key === 'Escape') closeURLModal();
});

function openURLModal() {
  urlInput.value = '';
  urlError.textContent = '';
  urlError.classList.add('hidden');
  urlRows.innerHTML = '';
  urlSummary.textContent = '';
  urlSummary.className = 'url-summary hidden';
  urlResult.classList.add('hidden');
  urlAnySucceeded = false;
  urlModal.classList.remove('hidden');
  urlInput.focus();
}

function closeURLModal() {
  urlModal.classList.add('hidden');
  if (urlAnySucceeded) {
    urlAnySucceeded = false;
    browse(currentPath, false);
  }
}

async function submitURLImport() {
  if (urlSubmitting) return;
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
  urlRows.innerHTML = '';
  urlSummary.textContent = '';
  urlSummary.className = 'url-summary hidden';
  urlResult.classList.remove('hidden');
  // Pre-create one pending row per URL so users see immediate feedback even
  // before the first SSE event arrives.
  urls.forEach((u, i) => ensureURLRow(i, u));
  urlSubmitting = true;
  urlConfirmBtn.disabled = true;
  urlConfirmBtn.textContent = '가져오는 중...';

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
      urlResult.classList.add('hidden');
      return;
    }
    await consumeSSE(res);
  } catch (e) {
    showURLError('요청 실패: ' + e.message);
  } finally {
    urlSubmitting = false;
    urlConfirmBtn.disabled = false;
    urlConfirmBtn.textContent = '가져오기';
  }
}

// consumeSSE reads the response body as a stream of `data: {json}\n\n` frames
// and dispatches each parsed event to handleSSEEvent. A trailing partial frame
// (no terminating blank line) is intentionally dropped — the server always
// flushes complete frames, so anything left over is corruption we ignore.
async function consumeSSE(res) {
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
        handleSSEEvent(JSON.parse(payload));
      } catch (e) {
        console.warn('bad sse frame', payload, e);
      }
    }
  }
}

function ensureURLRow(index, fallbackUrl) {
  let row = urlRows.querySelector(`[data-index="${index}"]`);
  if (row) return row;
  row = document.createElement('div');
  row.className = 'url-row status-pending';
  row.dataset.index = String(index);
  row.dataset.total = '0';
  row.innerHTML = `
    <div class="url-row-head">
      <span class="url-row-name">${esc(fallbackUrl || '')}</span>
      <span class="url-row-status">대기 중</span>
    </div>
    <div class="url-progress-bar"><div class="url-progress-fill"></div></div>
  `;
  urlRows.appendChild(row);
  return row;
}

function setRowStatus(row, statusClass, statusText) {
  row.classList.remove('status-pending', 'status-downloading', 'status-done', 'status-error');
  row.classList.add(statusClass);
  row.querySelector('.url-row-status').textContent = statusText;
}

function handleSSEEvent(ev) {
  switch (ev.phase) {
    case 'start': {
      const row = ensureURLRow(ev.index, ev.url);
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
      const row = urlRows.querySelector(`[data-index="${ev.index}"]`);
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
      const row = ensureURLRow(ev.index, ev.url);
      row.querySelector('.url-row-name').textContent = ev.name || ev.url;
      row.classList.remove('url-row-indeterminate');
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warn = (ev.warnings && ev.warnings.length > 0) ? ` · ${ev.warnings.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warn}`);
      urlAnySucceeded = true;
      break;
    }
    case 'error': {
      const row = ensureURLRow(ev.index, ev.url);
      row.classList.remove('url-row-indeterminate');
      const label = URL_ERROR_LABELS[ev.error] || ev.error || '알 수 없는 오류';
      setRowStatus(row, 'status-error', '실패 · ' + label);
      break;
    }
    case 'summary': {
      const cls = ev.failed === 0 ? 'status-done'
                : ev.succeeded === 0 ? 'status-error'
                : 'status-mixed';
      urlSummary.className = 'url-summary ' + cls;
      urlSummary.textContent = `성공 ${ev.succeeded}개 · 실패 ${ev.failed}개`;
      break;
    }
  }
}

function showURLError(msg) {
  urlError.textContent = msg;
  urlError.classList.remove('hidden');
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

// ── Init ──────────────────────────────────────────────────────────────────────
readViewFromURL();
syncToolbarUI();
const initPath = new URLSearchParams(location.search).get('path') || '/';
browse(initPath, false);
loadTree();
