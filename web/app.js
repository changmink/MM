'use strict';

// ── State ────────────────────────────────────────────────────────────────────
let currentPath = '/';
let imageEntries = [];   // images in current dir for lightbox
let videoEntries = [];   // videos in current dir for grid
let lbIndex = 0;
let playlist = [];
let playlistIndex = 0;

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
const folderModal   = document.getElementById('folder-modal');
const folderNameInput = document.getElementById('folder-name-input');
const folderCancelBtn = document.getElementById('folder-cancel-btn');
const folderConfirmBtn = document.getElementById('folder-confirm-btn');
const folderError   = document.getElementById('folder-error');
const urlImportBtn  = document.getElementById('url-import-btn');
const urlModal      = document.getElementById('url-modal');
const urlInput      = document.getElementById('url-input');
const urlError      = document.getElementById('url-error');
const urlRows       = document.getElementById('url-rows');
const urlSummary    = document.getElementById('url-summary');
const urlResult     = document.getElementById('url-result');
const urlCancelBtn  = document.getElementById('url-cancel-btn');
const urlConfirmBtn = document.getElementById('url-confirm-btn');

// ── Routing ───────────────────────────────────────────────────────────────────
window.addEventListener('popstate', () => {
  const p = new URLSearchParams(location.search).get('path') || '/';
  browse(p, false);
});

// ── Browse ────────────────────────────────────────────────────────────────────
async function browse(path, pushState = true) {
  currentPath = path;
  if (pushState) {
    history.pushState({}, '', '?path=' + encodeURIComponent(path));
  }
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

  const entries = data.entries || [];
  imageEntries = entries.filter(e => e.type === 'image');
  videoEntries = entries.filter(e => e.type === 'video');
  playlist = entries.filter(e => e.type === 'audio');

  renderFileList(entries);
}

function renderBreadcrumb(path) {
  breadcrumb.innerHTML = '';

  const home = document.createElement('a');
  home.href = 'javascript:void(0)';
  home.textContent = '홈';
  home.addEventListener('click', () => browse('/'));
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
      breadcrumb.appendChild(a);
    }
  });
}

function renderFileList(entries) {
  fileList.innerHTML = '';

  const dirs   = entries.filter(e => e.is_dir);
  const images = entries.filter(e => e.type === 'image');
  const videos = entries.filter(e => e.type === 'video');
  const audios = entries.filter(e => e.type === 'audio');
  const others = entries.filter(e => e.type === 'other');

  if (dirs.length) {
    fileList.appendChild(sectionTitle('폴더'));
    fileList.appendChild(buildTable(dirs));
  }
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

  if (!entries.length) {
    fileList.innerHTML = '<p style="color:var(--text-dim);padding:20px 0">비어있습니다.</p>';
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
      <button class="delete-btn" title="삭제">✕</button>
    `;
    card.querySelector('img').addEventListener('click', () => openLightboxImage(i));
    card.querySelector('.delete-btn').addEventListener('click', (ev) => {
      ev.stopPropagation();
      deleteFile(entry.path);
    });
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
      ${durBadge}
      <button class="delete-btn" title="삭제">✕</button>
    `;
    card.querySelector('img').addEventListener('click', () => openLightboxVideo(entry));
    card.querySelector('.delete-btn').addEventListener('click', (ev) => {
      ev.stopPropagation();
      deleteFile(entry.path);
    });
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
      <td class="action-cell"><button title="삭제" data-path="${esc(entry.path)}">🗑</button></td>
    `;
    tr.querySelector('.name-cell').addEventListener('click', () => handleClick(entry));
    tr.querySelector('.action-cell button').addEventListener('click', () =>
      entry.is_dir ? deleteFolder(entry.path) : deleteFile(entry.path)
    );
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
uploadZone.addEventListener('dragover', e => {
  e.preventDefault();
  uploadZone.classList.add('drag-over');
});
uploadZone.addEventListener('dragleave', () => uploadZone.classList.remove('drag-over'));
uploadZone.addEventListener('drop', e => {
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
      row.dataset.total = String(ev.total || 0);
      const sizeText = ev.total > 0 ? formatSize(ev.total) : '크기 미상';
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
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warn = (ev.warnings && ev.warnings.length > 0) ? ` · ${ev.warnings.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warn}`);
      urlAnySucceeded = true;
      break;
    }
    case 'error': {
      const row = ensureURLRow(ev.index, ev.url);
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
  } else {
    alert('폴더 삭제 실패');
  }
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

// ── Init ──────────────────────────────────────────────────────────────────────
const initPath = new URLSearchParams(location.search).get('path') || '/';
browse(initPath, false);
