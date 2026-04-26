// browse.js — 디렉토리 조회 + 정렬/필터/렌더 + lightbox + 오디오 플레이어
//
// 가장 큰 도메인. fileOps · tree · convert 의 export 를 직접 import (모두 단방향).

import { $ } from './dom.js';
import {
  currentPath, setCurrentPath,
  allEntries, setAllEntries,
  imageEntries, setImageEntries,
  videoEntries, setVideoEntries,
  visibleFilePaths, setVisibleFilePaths,
  lbIndex, setLbIndex,
  playlist, setPlaylist,
  playlistIndex, setPlaylistIndex,
  selectedPaths, view,
  CLIP_MAX_BYTES, CLIP_MAX_DURATION_SEC,
} from './state.js';
import { esc, iconFor, formatSize, formatDuration } from './util.js';
import { syncURL } from './router.js';
import {
  attachDragHandlers, attachDropHandlers,
  openRenameModal, deleteFile, deleteFolder,
} from './fileOps.js';
import { highlightTreeCurrent } from './tree.js';
import { openConvertModal } from './convert.js';

export async function browse(path, pushState = true) {
  setCurrentPath(path);
  if (pushState) syncURL(true);
  renderBreadcrumb(path);

  let data;
  try {
    const res = await fetch('/api/browse?path=' + encodeURIComponent(path));
    if (!res.ok) throw new Error(await res.text());
    data = await res.json();
  } catch (e) {
    $.fileList.innerHTML = `<p class="error">오류: ${e.message}</p>`;
    return;
  }

  setAllEntries(data.entries || []);
  renderView();
  highlightTreeCurrent();
}

// Apply sort/filter to allEntries and render. Split from browse() so the
// toolbar can re-render without refetching. Keeps lightbox/playlist arrays
// in sync with the visible set so prev/next don't land on hidden entries.
export function renderView() {
  const visible = applyView(allEntries);
  syncSelectionWithVisible(visible);
  setImageEntries(visible.filter(e => e.type === 'image'));
  setVideoEntries(visible.filter(e => e.type === 'video'));
  setPlaylist(visible.filter(e => e.type === 'audio'));
  renderBrowseSummary(visible);
  renderFileList(visible);
  updateConvertAllBtn(visible);
  renderSelectionControls();
}

export function visibleTSPaths(visible) {
  return visible
    .filter(e => !e.is_dir && e.type === 'video' && e.name.toLowerCase().endsWith('.ts'))
    .map(e => e.path);
}

function updateConvertAllBtn(visible) {
  const paths = visibleTSPaths(visible);
  if (paths.length === 0) {
    $.convertAllBtn.hidden = true;
    return;
  }
  $.convertAllBtn.hidden = false;
  $.convertAllBtn.textContent = `모든 TS 변환 (${paths.length})`;
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

export function applyView(entries) {
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
    $.browseSummary.textContent = '';
    return;
  }
  const total = files.reduce((s, e) => s + (e.size || 0), 0);
  $.browseSummary.textContent = `파일 ${files.length}개 · ${formatSize(total)}`;
}

function renderBreadcrumb(path) {
  $.breadcrumb.innerHTML = '';

  const home = document.createElement('a');
  home.href = 'javascript:void(0)';
  home.textContent = '홈';
  home.addEventListener('click', () => browse('/'));
  attachDropHandlers(home, '/');
  $.breadcrumb.appendChild(home);

  const parts = path.split('/').filter(Boolean);
  let accumulated = '';
  parts.forEach((part, i) => {
    const sep = document.createElement('span');
    sep.textContent = '/';
    $.breadcrumb.appendChild(sep);

    accumulated += '/' + part;
    const isLast = i === parts.length - 1;
    if (isLast) {
      const span = document.createElement('span');
      span.textContent = part;
      $.breadcrumb.appendChild(span);
    } else {
      const a = document.createElement('a');
      a.href = 'javascript:void(0)';
      a.textContent = part;
      const p = accumulated;
      a.addEventListener('click', () => browse(p));
      attachDropHandlers(a, p);
      $.breadcrumb.appendChild(a);
    }
  });
}

function renderFileList(entries) {
  $.fileList.innerHTML = '';

  // Folders intentionally omitted from the main list — the sidebar tree is
  // the single navigation surface. Files-only sections below.
  const images = entries.filter(e => e.type === 'image');
  const videos = entries.filter(e => e.type === 'video');
  const audios = entries.filter(e => e.type === 'audio');
  const others = entries.filter(e => e.type === 'other');

  if (images.length) {
    $.fileList.appendChild(sectionTitle('이미지'));
    $.fileList.appendChild(buildImageGrid(images));
  }
  if (videos.length) {
    $.fileList.appendChild(sectionTitle('동영상'));
    $.fileList.appendChild(buildVideoGrid(videos));
  }
  if (audios.length) {
    $.fileList.appendChild(sectionTitle('음악'));
    $.fileList.appendChild(buildTable(audios));
  }
  if (others.length) {
    $.fileList.appendChild(sectionTitle('기타'));
    $.fileList.appendChild(buildTable(others));
  }

  const fileCount = images.length + videos.length + audios.length + others.length;
  if (!fileCount) {
    const msg = (view.q || view.type !== 'all')
      ? '검색 결과가 없습니다.'
      : '파일이 없습니다.';
    $.fileList.innerHTML = `<p style="color:var(--text-dim);padding:20px 0">${msg}</p>`;
  }
}

function syncSelectionWithVisible(entries) {
  setVisibleFilePaths(entries.filter(e => !e.is_dir).map(e => e.path));
  const visibleSet = new Set(visibleFilePaths);
  for (const path of Array.from(selectedPaths)) {
    if (!visibleSet.has(path)) selectedPaths.delete(path);
  }
}

function renderSelectionControls() {
  const total = visibleFilePaths.length;
  const selected = visibleFilePaths.filter(path => selectedPaths.has(path)).length;
  $.selectAllFiles.disabled = total === 0;
  $.selectAllFiles.checked = total > 0 && selected === total;
  $.selectAllFiles.indeterminate = selected > 0 && selected < total;
  $.selectionSummary.textContent = selected
    ? `선택 ${selected}개 / ${total}개`
    : `선택 0개${total ? ` / ${total}개` : ''}`;
  $.clearSelectionBtn.hidden = selected === 0;
}

function setSelected(path, selected) {
  if (selected) selectedPaths.add(path);
  else selectedPaths.delete(path);
  renderView();
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
      <label class="select-check" title="선택">
        <input type="checkbox" aria-label="${esc(entry.name)} 선택">
      </label>
      <img src="${esc(thumbSrc)}" alt="${esc(entry.name)}" loading="lazy">
      <div class="thumb-name">${esc(entry.name)}</div>
      <span class="size-badge">${esc(formatSize(entry.size))}</span>
      <button class="rename-btn" title="이름 변경" aria-label="이름 변경">✎</button>
      <button class="delete-btn" title="삭제" aria-label="삭제">✕</button>
    `;
    bindEntrySelection(card, entry);
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
      <label class="select-check" title="선택">
        <input type="checkbox" aria-label="${esc(entry.name)} 선택">
      </label>
      <img src="${esc(thumbSrc)}" alt="${esc(entry.name)}" loading="lazy">
      <div class="thumb-name">${esc(entry.name)}</div>
      <span class="size-badge">${esc(formatSize(entry.size))}</span>
      ${durBadge}
      ${convertBtn}
      <button class="rename-btn" title="이름 변경" aria-label="이름 변경">✎</button>
      <button class="delete-btn" title="삭제" aria-label="삭제">✕</button>
    `;
    bindEntrySelection(card, entry);
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
    <th class="select-cell"></th>
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
      <td class="select-cell"><input type="checkbox" aria-label="${esc(entry.name)} 선택"></td>
      <td class="name-cell"><span class="icon">${icon}</span>${esc(entry.name)}</td>
      <td class="size-cell">${size}</td>
      <td class="action-cell">
        <button class="rename-action" title="이름 변경" aria-label="이름 변경">✎</button>
        <button class="delete-action" title="삭제" aria-label="삭제">🗑</button>
      </td>
    `;
    bindEntrySelection(tr, entry);
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

function bindEntrySelection(container, entry) {
  const checkbox = container.querySelector('input[type="checkbox"]');
  checkbox.checked = selectedPaths.has(entry.path);
  container.classList.toggle('selected', checkbox.checked);
  checkbox.addEventListener('click', ev => ev.stopPropagation());
  checkbox.addEventListener('change', () => setSelected(entry.path, checkbox.checked));
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
  setLbIndex(index);
  const entry = imageEntries[lbIndex];
  $.lbContent.innerHTML = `<img src="/api/stream?path=${encodeURIComponent(entry.path)}" alt="${esc(entry.name)}">`;
  $.lightbox.classList.remove('hidden');
}

function openLightboxVideo(entry) {
  const mime = entry.path.toLowerCase().endsWith('.ts') ? 'video/mp4' : (entry.mime || 'video/mp4');
  $.lbContent.innerHTML = `
    <video controls autoplay>
      <source src="/api/stream?path=${encodeURIComponent(entry.path)}" type="${esc(mime)}">
    </video>`;
  $.lightbox.classList.remove('hidden');
}

// ── Audio Player ──────────────────────────────────────────────────────────────
function playAudio(entry) {
  setPlaylistIndex(playlist.findIndex(e => e.path === entry.path));
  if (playlistIndex < 0) setPlaylistIndex(0);
  loadPlaylistTrack(playlistIndex);
  $.audioPlayer.classList.remove('hidden');
  renderPlaylist();
}

function loadPlaylistTrack(index) {
  const entry = playlist[index];
  $.audioEl.src = '/api/stream?path=' + encodeURIComponent(entry.path);
  $.audioTitle.textContent = entry.name;
  $.audioEl.play();
  renderPlaylist();
}

function renderPlaylist() {
  $.playlistEl.innerHTML = '';
  playlist.forEach((entry, i) => {
    const item = document.createElement('div');
    item.className = 'playlist-item' + (i === playlistIndex ? ' active' : '');
    item.textContent = entry.name;
    item.addEventListener('click', () => {
      setPlaylistIndex(i);
      loadPlaylistTrack(i);
    });
    $.playlistEl.appendChild(item);
  });
}

export function wireBrowse() {
  // Selection toolbar
  $.selectAllFiles.addEventListener('change', () => {
    if ($.selectAllFiles.checked) {
      visibleFilePaths.forEach(path => selectedPaths.add(path));
    } else {
      visibleFilePaths.forEach(path => selectedPaths.delete(path));
    }
    renderView();
  });
  $.clearSelectionBtn.addEventListener('click', () => {
    selectedPaths.clear();
    renderView();
  });

  // Lightbox controls
  $.lbClose.addEventListener('click', () => {
    $.lightbox.classList.add('hidden');
    $.lbContent.innerHTML = '';
  });
  $.lbPrev.addEventListener('click', () => {
    if (!imageEntries.length) return;
    setLbIndex((lbIndex - 1 + imageEntries.length) % imageEntries.length);
    openLightboxImage(lbIndex);
  });
  $.lbNext.addEventListener('click', () => {
    if (!imageEntries.length) return;
    setLbIndex((lbIndex + 1) % imageEntries.length);
    openLightboxImage(lbIndex);
  });
  $.lightbox.addEventListener('click', e => {
    if (e.target === $.lightbox) {
      $.lightbox.classList.add('hidden');
      $.lbContent.innerHTML = '';
    }
  });
  document.addEventListener('keydown', e => {
    if ($.lightbox.classList.contains('hidden')) return;
    if (e.key === 'Escape') $.lbClose.click();
    if (e.key === 'ArrowLeft') $.lbPrev.click();
    if (e.key === 'ArrowRight') $.lbNext.click();
  });

  // Audio auto-advance — module mode imports are read-only bindings, so the
  // original `playlistIndex++` in this listener silently TypeError'd after
  // FM-1. Fix by going through the setter.
  $.audioEl.addEventListener('ended', () => {
    if (playlistIndex < playlist.length - 1) {
      setPlaylistIndex(playlistIndex + 1);
      loadPlaylistTrack(playlistIndex);
    }
  });

  // "모든 TS 변환" toolbar button — wired here because the handler needs
  // applyView/visibleTSPaths/allEntries (browse internals) plus convert's
  // openConvertModal.
  $.convertAllBtn.addEventListener('click', () => {
    const paths = visibleTSPaths(applyView(allEntries));
    if (paths.length === 0) return;
    openConvertModal(paths);
  });
}
