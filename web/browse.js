// browse.js — 디렉토리 조회 + 정렬/필터/렌더 orchestrator.
//
// 진입점. fileOps · tree · convert · 분리 모듈(clipPlayback / visiblePaths /
// selection / cardBuilders / lightbox)을 직접 import (모두 단방향).

import { $ } from './dom.js';
import {
  currentPath, setCurrentPath,
  allEntries, setAllEntries,
  imageEntries, setImageEntries,
  videoEntries, setVideoEntries,
  setPlaylist,
  view,
} from './state.js';
import { formatSize } from './util.js';
import { syncURL } from './router.js';
import { attachDropHandlers } from './fileOps.js';
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
import {
  wireLightbox,
  openLightboxImage,
  openLightboxVideo,
  playAudio,
} from './lightbox.js';

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

// allEntries에 정렬/필터를 적용해 렌더링한다. browse()에서 분리해 툴바가
// 재fetch 없이 다시 렌더할 수 있게 한다. lightbox/playlist 배열을 visible
// 셋과 동기화 유지해, prev/next가 숨은 항목에 닿지 않게 한다.
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
  // image/video/clip은 상호 배타적이다 — clip은 image나 video 탭에 나타나지
  // 않는다. "전체" 탭은 모든 파일을 자연스러운 섹션 안에 유지해, 명시적
  // 필터 없이 숨겨지는 것이 없도록 한다.
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

  // 폴더는 메인 목록에서 의도적으로 제외한다 — 사이드바 트리가
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

export function wireBrowse() {
  // Selection toolbar — selection.js 내부에 closure 형태로 등록. computeVisible
  // 은 applyView/allEntries 도메인을 selection.js 가 import 하지 않게 하는
  // 단방향 의존 회피용(plan §6 #4 라이트박스 deps 패턴 미러).
  wireSelection({ computeVisible: () => applyView(allEntries) });

  // Lightbox + audio — onAfterDelete 콜백으로 폴더 새로고침 주입(lightbox.js
  // 가 browse.js 를 import 하지 않게 하는 사이클 회피, plan §6 #4).
  wireLightbox({ onAfterDelete: () => browse(currentPath, false) });

  // "모든 TS 변환" toolbar button — wired here because the handler needs
  // applyView/visibleTSPaths/allEntries (browse internals) plus convert's
  // openConvertModal.
  $.convertAllBtn.addEventListener('click', () => {
    const paths = visibleTSPaths(applyView(allEntries));
    if (paths.length === 0) return;
    openConvertModal(paths);
  });
}
