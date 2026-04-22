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
const sidebarToggle = document.getElementById('sidebar-toggle');
const sidebarBackdrop = document.getElementById('sidebar-backdrop');
const treeRoot       = document.getElementById('tree-root');

// Initial tree fetch depth — root + children + grandchildren in one round trip
// per user spec (Q1=opt3). Deeper nodes lazy-load on chevron click.
const TREE_INIT_DEPTH = 2;

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
  highlightTreeCurrent();
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
    fileList.innerHTML = '<p style="color:var(--text-dim);padding:20px 0">파일이 없습니다.</p>';
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
    treeRoot.innerHTML = `<div class="tree-error">트리 로드 실패: ${esc(e.message)}</div>`;
  }
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

  row.appendChild(chevron);
  row.appendChild(label);
  wrap.appendChild(row);

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

// ── Init ──────────────────────────────────────────────────────────────────────
const initPath = new URLSearchParams(location.search).get('path') || '/';
browse(initPath, false);
loadTree();
