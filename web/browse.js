// browse.js — 디렉토리 조회 + 정렬/필터/렌더 + lightbox + 오디오 플레이어
//
// 가장 큰 도메인. fileOps · tree · convert 의 export 를 직접 import (모두 단방향).

import { $ } from './dom.js';
import {
  currentPath, setCurrentPath,
  allEntries, setAllEntries,
  imageEntries, setImageEntries,
  videoEntries, setVideoEntries,
  lbIndex, setLbIndex,
  lbCurrentVideoPath, setLbCurrentVideoPath,
  playlist, setPlaylist,
  playlistIndex, setPlaylistIndex,
  view,
} from './state.js';
import { esc, formatSize } from './util.js';
import { syncURL } from './router.js';
import { attachDropHandlers, deleteFile } from './fileOps.js';
import { highlightTreeCurrent } from './tree.js';
import { openConvertModal } from './convert.js';
import { isClip } from './clipPlayback.js';
import {
  visibleTSPaths,
  updateConvertAllBtn,
  visiblePNGPaths,
  selectedVisiblePNGPaths,
  updateConvertPNGAllBtn,
  visibleClipPaths,
  selectedVisibleClipPaths,
  updateConvertWebPAllBtn,
} from './visiblePaths.js';
import {
  wireSelection,
  syncSelectionWithVisible,
  renderSelectionControls,
} from './selection.js';
import {
  sectionTitle,
  buildImageGrid,
  buildVideoGrid,
  buildTable,
} from './cardBuilders.js';

// browse.js 외부 surface 보존 — 기존 export 함수들이 외부에서 직접
// import 되던 사실은 없지만 (현 grep 0건), 회귀 차단을 위한 보수적
// re-export. BD-1·BD-2 plan §6 #8. (syncCardSelectionStates 는 BD-3
// 에서 dragSelect.js import 경로를 selection.js 로 갱신해 외부 import
// 를 끊었으므로 re-export 불필요.)
export { isClip };
export {
  visibleTSPaths,
  visiblePNGPaths,
  selectedVisiblePNGPaths,
  visibleClipPaths,
  selectedVisibleClipPaths,
};

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
    const grid = buildImageGrid(images, openLightboxImage);
    if (view.type === 'clip') grid.classList.add('image-grid-clip');
    $.fileList.appendChild(grid);
  }
  if (videos.length) {
    $.fileList.appendChild(sectionTitle('동영상'));
    $.fileList.appendChild(buildVideoGrid(videos, openLightboxVideo));
  }
  if (audios.length) {
    $.fileList.appendChild(sectionTitle('음악'));
    $.fileList.appendChild(buildTable(audios, handleClick));
  }
  if (others.length) {
    $.fileList.appendChild(sectionTitle('기타'));
    $.fileList.appendChild(buildTable(others, handleClick));
  }

  const fileCount = images.length + videos.length + audios.length + others.length;
  if (!fileCount) {
    const msg = (view.q || view.type !== 'all')
      ? '검색 결과가 없습니다.'
      : '파일이 없습니다.';
    $.fileList.innerHTML = `<p style="color:var(--text-dim);padding:20px 0">${msg}</p>`;
  }
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
  // Selection toolbar — selection.js 내부에 closure 형태로 등록. computeVisible
  // 은 applyView/allEntries 도메인을 selection.js 가 import 하지 않게 하는
  // 단방향 의존 회피용(plan §6 #4 라이트박스 deps 패턴 미러).
  wireSelection({ computeVisible: () => applyView(allEntries) });

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
