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
  lbCurrentVideoPath, setLbCurrentVideoPath,
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
import { openConvertImageModal } from './convertImage.js';
import { openConvertWebPModal } from './convertWebp.js';

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
  updateConvertPNGAllBtn(visible);
  updateConvertWebPAllBtn(visible);
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

export function visiblePNGPaths(visible) {
  return visible
    .filter(e => !e.is_dir && e.mime === 'image/png')
    .map(e => e.path);
}

// selectedPaths ∩ visible 중 PNG만. visible 인자로 한 번 더 필터해
// stale selection(syncSelectionWithVisible 직전 state)도 안전하게 거른다.
export function selectedVisiblePNGPaths(visible) {
  return visible
    .filter(e => !e.is_dir && e.mime === 'image/png' && selectedPaths.has(e.path))
    .map(e => e.path);
}

function updateConvertPNGAllBtn(visible) {
  const useSelection = selectedPaths.size > 0;
  const paths = useSelection
    ? selectedVisiblePNGPaths(visible)
    : visiblePNGPaths(visible);
  if (paths.length === 0) {
    $.convertPNGAllBtn.hidden = true;
    $.convertPNGAllBtn.dataset.paths = '';
    return;
  }
  $.convertPNGAllBtn.hidden = false;
  $.convertPNGAllBtn.textContent = useSelection
    ? `선택 PNG 변환 (${paths.length}개)`
    : `모든 PNG 변환 (${paths.length}개)`;
  // Stash the current target PNG list on the button so the convertImage
  // module's click handler can read it without a second filter pass.
  $.convertPNGAllBtn.dataset.paths = JSON.stringify(paths);
}

// SPEC §2.5.6 — GIF/WebP 카드 자동재생 throttle. hover-capable 디바이스는
// hover 시만 stream src 부착, 그 외(모바일 등)는 IntersectionObserver 로
// viewport 안에서만 활성. 모듈 lifetime 동안 IO 인스턴스 1개를 공유한다.
const HOVER_CAPABLE = typeof window !== 'undefined'
  && window.matchMedia
  && window.matchMedia('(hover: hover)').matches;

let _clipIO = null;
function clipIOInstance() {
  if (_clipIO) return _clipIO;
  _clipIO = new IntersectionObserver(entries => {
    for (const ent of entries) {
      const card = ent.target;
      const img = card.querySelector('img');
      if (!img) continue;
      const desired = ent.isIntersecting ? 'stream' : 'thumb';
      if (card.dataset.clipState === desired) continue;
      card.dataset.clipState = desired;
      img.src = desired === 'stream' ? img.dataset.streamSrc : img.dataset.thumbSrc;
    }
  }, { rootMargin: '0px', threshold: 0.1 });
  return _clipIO;
}

function attachClipHoverPlayback(card) {
  const img = card.querySelector('img');
  if (!img || !img.dataset.streamSrc) return;
  if (HOVER_CAPABLE) {
    let current = 'thumb';
    card.addEventListener('mouseenter', () => {
      if (current === 'stream') return;
      current = 'stream';
      img.src = img.dataset.streamSrc;
    });
    card.addEventListener('mouseleave', () => {
      if (current === 'thumb') return;
      current = 'thumb';
      img.src = img.dataset.thumbSrc;
    });
  } else {
    card.dataset.clipState = 'thumb';
    clipIOInstance().observe(card);
  }
}

// 움짤 분류 — GIF·WebP 는 무조건 움짤(SPEC §2.5.3); video 는 짧고 작을
// 때만 (null duration 제외 — 길이를 모르면 보수적으로 비-움짤).
// 분류와 변환 입력 자격은 다르다: webp 는 §2.9 변환 결과물이므로 입력
// 자격에서 빠진다 (isClipConvertable).
export function isClip(e) {
  if (e.mime === 'image/gif' || e.mime === 'image/webp') return true;
  if (e.type === 'video') {
    return e.size <= CLIP_MAX_BYTES
      && e.duration_sec != null
      && e.duration_sec <= CLIP_MAX_DURATION_SEC;
  }
  return false;
}

// isClipConvertable — §2.9 변환 입력 자격. isClip 의 부분집합으로 webp 를
// 제외한다 (변환 결과물이라 재변환 의도 없음). 서버도 동일 결정 — webp
// 입력은 unsupported_input 으로 거부.
function isClipConvertable(e) {
  return isClip(e) && e.mime !== 'image/webp';
}

export function visibleClipPaths(visible) {
  return visible
    .filter(e => !e.is_dir && isClipConvertable(e))
    .map(e => e.path);
}

// selectedPaths ∩ visible 중 변환가능 움짤만. visible 인자로 stale selection
// 방어 — PNG 일괄 패턴(selectedVisiblePNGPaths) 미러.
export function selectedVisibleClipPaths(visible) {
  return visible
    .filter(e => !e.is_dir && isClipConvertable(e) && selectedPaths.has(e.path))
    .map(e => e.path);
}

// 일괄 버튼은 움짤 탭 활성 시에만 노출 — SPEC §2.9. 다른 탭에서는
// visible 에 움짤이 섞여 있어도 버튼을 띄우지 않는다 (의도 모호함 방지).
function updateConvertWebPAllBtn(visible) {
  if (view.type !== 'clip') {
    $.convertWebpAllBtn.hidden = true;
    $.convertWebpAllBtn.dataset.paths = '';
    return;
  }
  const useSelection = selectedPaths.size > 0;
  const paths = useSelection
    ? selectedVisibleClipPaths(visible)
    : visibleClipPaths(visible);
  if (paths.length === 0) {
    $.convertWebpAllBtn.hidden = true;
    $.convertWebpAllBtn.dataset.paths = '';
    return;
  }
  $.convertWebpAllBtn.hidden = false;
  $.convertWebpAllBtn.textContent = useSelection
    ? `선택 움짤 WebP로 변환 (${paths.length}개)`
    : `모든 움짤 WebP로 변환 (${paths.length}개)`;
  $.convertWebpAllBtn.dataset.paths = JSON.stringify(paths);
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
    const grid = buildImageGrid(images);
    if (view.type === 'clip') grid.classList.add('image-grid-clip');
    $.fileList.appendChild(grid);
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

// updateCardSelection은 path 한 건의 카드 .selected class + checkbox 상태만
// 갱신한다. selection 종속 UI(상단 indicator, convert 버튼)는 갱신하지 않는다 —
// 호출자가 한 번에 묶어 부르도록 분리.
function updateCardSelection(path, selected) {
  const card = $.fileList.querySelector(`[data-path="${CSS.escape(path)}"]`);
  if (!card) return;
  card.classList.toggle('selected', selected);
  const cb = card.querySelector('input[type="checkbox"]');
  if (cb && cb.checked !== selected) cb.checked = selected;
}

// syncCardSelectionStates는 selectedPaths를 진실값으로 삼아 visible 모든 카드의
// .selected/checkbox 상태와 selection 종속 UI를 동기화한다. dragSelect의
// rubber-band 처럼 한 번에 다중 path가 토글되는 경로가 renderView 대신 호출 —
// 카드 DOM 재구성과 listener 재할당, GIF/WebP 자동재생 리셋을 피한다.
export function syncCardSelectionStates() {
  $.fileList.querySelectorAll('[data-path]').forEach(card => {
    const path = card.dataset.path;
    const selected = selectedPaths.has(path);
    card.classList.toggle('selected', selected);
    const cb = card.querySelector('input[type="checkbox"]');
    if (cb && cb.checked !== selected) cb.checked = selected;
  });
  refreshSelectionUI();
}

// refreshSelectionUI는 카드 DOM은 건드리지 않고 selection 종속 UI만 다시
// 그린다. 200+ 카드 디렉터리에서 체크박스 토글 시 renderView 전체 재구축은
// GIF/WebP 자동재생을 리셋하고 jank를 만들기 때문에, setSelected 같은 hot
// path가 이걸로 우회한다.
function refreshSelectionUI() {
  const visible = applyView(allEntries);
  updateConvertPNGAllBtn(visible);
  updateConvertWebPAllBtn(visible);
  renderSelectionControls();
}

function setSelected(path, selected) {
  if (selected) selectedPaths.add(path);
  else selectedPaths.delete(path);
  updateCardSelection(path, selected);
  refreshSelectionUI();
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
    card.dataset.path = entry.path;

    const thumbURL = '/api/thumb?path=' + encodeURIComponent(entry.path);
    const streamURL = '/api/stream?path=' + encodeURIComponent(entry.path);
    const isPNG = entry.mime === 'image/png';
    const isGIF = entry.mime === 'image/gif';
    const isWebP = entry.mime === 'image/webp';
    // GIF/WebP 카드는 평시 정적 첫 프레임(/api/thumb, lazy 생성 backstop)
    // 을 표시하고 hover/IntersectionObserver 시에만 stream URL 로 토글한다
    // (§2.5.6). 비-움짤 이미지는 기존 thumb_available 폴백 유지.
    const isAnimatedClip = isGIF || isWebP;
    const initialSrc = isAnimatedClip
      ? thumbURL
      : (entry.thumb_available ? thumbURL : streamURL);

    const pngConvertBtn = isPNG
      ? `<button class="png-convert-btn" title="JPG로 변환" aria-label="JPG로 변환">JPG</button>`
      : '';
    const webpConvertBtn = isGIF
      ? `<button class="webp-convert-btn" title="WebP로 변환" aria-label="WebP로 변환">WEBP</button>`
      : '';

    card.innerHTML = `
      <label class="select-check" title="선택">
        <input type="checkbox" aria-label="${esc(entry.name)} 선택">
      </label>
      <img src="${esc(initialSrc)}" alt="${esc(entry.name)}" loading="lazy">
      <div class="thumb-name">${esc(entry.name)}</div>
      <span class="size-badge">${esc(formatSize(entry.size))}</span>
      ${pngConvertBtn}
      ${webpConvertBtn}
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
    if (isPNG) {
      card.querySelector('.png-convert-btn').addEventListener('click', (ev) => {
        ev.stopPropagation();
        openConvertImageModal([entry.path]);
      });
    }
    if (isGIF) {
      card.querySelector('.webp-convert-btn').addEventListener('click', (ev) => {
        ev.stopPropagation();
        openConvertWebPModal([entry.path]);
      });
    }
    if (isAnimatedClip) {
      const img = card.querySelector('img');
      img.dataset.thumbSrc = thumbURL;
      img.dataset.streamSrc = streamURL;
      attachClipHoverPlayback(card);
    }
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
    card.dataset.path = entry.path;

    const thumbSrc = '/api/thumb?path=' + encodeURIComponent(entry.path);
    const dur = formatDuration(entry.duration_sec);
    const durBadge = dur ? `<span class="duration-badge">${esc(dur)}</span>` : '';
    const isTS = entry.name.toLowerCase().endsWith('.ts');
    const convertBtn = isTS
      ? `<button class="convert-btn" title="MP4로 변환" aria-label="MP4로 변환">MP4</button>`
      : '';
    const isClipVideo = isClip(entry);
    const webpConvertBtn = isClipVideo
      ? `<button class="webp-convert-btn" title="WebP로 변환" aria-label="WebP로 변환">WEBP</button>`
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
      ${webpConvertBtn}
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
    if (isClipVideo) {
      card.querySelector('.webp-convert-btn').addEventListener('click', (ev) => {
        ev.stopPropagation();
        openConvertWebPModal([entry.path]);
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
    tr.dataset.path = entry.path;
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
    attachDragHandlers(tr, entry);
    tbody.appendChild(tr);
  });

  table.appendChild(tbody);
  return table;
}

function bindEntrySelection(container, entry) {
  const checkbox = container.querySelector('input[type="checkbox"]');
  // Folders are never multi-selected — moving a folder is always a single-target
  // operation. Hide the checkbox entirely so a stale selection set can't pull
  // a folder into a bulk file move.
  if (entry.is_dir) {
    const cell = container.querySelector('.select-cell, .select-check');
    if (cell) cell.style.visibility = 'hidden';
    return;
  }
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
  setLbCurrentVideoPath(null);
  const entry = imageEntries[lbIndex];
  $.lbContent.innerHTML = `<img src="/api/stream?path=${encodeURIComponent(entry.path)}" alt="${esc(entry.name)}">`;
  $.lightbox.classList.remove('hidden');
}

function openLightboxVideo(entry) {
  setLbCurrentVideoPath(entry.path);
  const mime = entry.path.toLowerCase().endsWith('.ts') ? 'video/mp4' : (entry.mime || 'video/mp4');
  $.lbContent.innerHTML = `
    <video controls autoplay>
      <source src="/api/stream?path=${encodeURIComponent(entry.path)}" type="${esc(mime)}">
    </video>`;
  $.lightbox.classList.remove('hidden');
}

// 닫기 트리거(✕ / 배경 클릭 / Esc)는 모두 이 함수를 거치게 해서
// lbCurrentVideoPath 리셋이 한 곳에 모이게 한다 — 누락 시 다음 이미지
// 라이트박스에서 stale path가 살아남아 삭제 분기가 동영상으로 새는 버그 발생.
function closeLightbox() {
  $.lightbox.classList.add('hidden');
  $.lbContent.innerHTML = '';
  setLbCurrentVideoPath(null);
}

async function deleteCurrentLightboxItem() {
  if (lbCurrentVideoPath) {
    const ok = await deleteFile(lbCurrentVideoPath, { skipBrowse: true });
    if (!ok) return;
    closeLightbox();
    browse(currentPath, false);
  } else if (imageEntries.length) {
    const entry = imageEntries[lbIndex];
    const ok = await deleteFile(entry.path, { skipBrowse: true });
    if (!ok) return;
    imageEntries.splice(lbIndex, 1);
    if (imageEntries.length === 0) {
      closeLightbox();
    } else {
      setLbIndex(lbIndex % imageEntries.length);
      openLightboxImage(lbIndex);
    }
    browse(currentPath, false);
  }
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
  // Selection toolbar — 카드 DOM은 건드리지 않고 영향 카드의 class/checkbox만
  // 갱신해 GIF/WebP 자동재생 리셋과 listener 재할당을 피한다.
  $.selectAllFiles.addEventListener('change', () => {
    const on = $.selectAllFiles.checked;
    visibleFilePaths.forEach(path => {
      if (on) selectedPaths.add(path);
      else selectedPaths.delete(path);
      updateCardSelection(path, on);
    });
    refreshSelectionUI();
  });
  $.clearSelectionBtn.addEventListener('click', () => {
    const previouslySelected = Array.from(selectedPaths);
    selectedPaths.clear();
    previouslySelected.forEach(path => updateCardSelection(path, false));
    refreshSelectionUI();
  });

  // Lightbox controls
  $.lbClose.addEventListener('click', closeLightbox);
  $.lbDelete.addEventListener('click', deleteCurrentLightboxItem);
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
    if (e.target === $.lightbox) closeLightbox();
  });
  document.addEventListener('keydown', e => {
    if ($.lightbox.classList.contains('hidden')) return;
    if (e.key === 'Escape') closeLightbox();
    if (e.key === 'ArrowLeft') $.lbPrev.click();
    if (e.key === 'ArrowRight') $.lbNext.click();
    if (e.key === 'Delete') deleteCurrentLightboxItem();
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
