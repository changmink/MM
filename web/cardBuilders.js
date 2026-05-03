// cardBuilders.js — 카드 그리드/테이블 DOM 빌더.
//
// renderFileList 가 type 별로 호출. 카드 click 분기(라이트박스 / 오디오
// 플레이리스트 / 폴더 진입)는 browse.js 의 orchestrator 도메인이라
// build* 호출 시점에 onOpen 콜백을 매번 인자로 받는다(plan §3 BD-4 권장).
// wire-time 모듈 mutable state 대신 매-호출-주입 — 호출 시점 의존성을
// 명시적으로 만든다.
//
// 모듈 의존: clipPlayback(움짤 분류·hover 부착), selection(체크박스 바인딩),
// fileOps/convert*(카드별 액션 버튼), util.

import { esc, iconFor, formatSize, formatDuration } from './util.js';
import {
  attachDragHandlers, openRenameModal, deleteFile, deleteFolder,
} from './fileOps.js';
import { openConvertModal } from './convert.js';
import { openConvertImageModal } from './convertImage.js';
import { openConvertWebPModal } from './convertWebp.js';
import { attachClipHoverPlayback, isClip } from './clipPlayback.js';
import { bindEntrySelection } from './selection.js';

export function sectionTitle(text) {
  const el = document.createElement('div');
  el.className = 'section-title';
  el.textContent = text;
  return el;
}

export function buildImageGrid(images, onOpen) {
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
    card.querySelector('img').addEventListener('click', () => onOpen(i));
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

export function buildVideoGrid(videos, onOpen) {
  const grid = document.createElement('div');
  grid.className = 'image-grid';
  videos.forEach(entry => {
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
    card.querySelector('img').addEventListener('click', () => onOpen(entry));
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

export function buildTable(entries, onOpen) {
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
    tr.querySelector('.name-cell').addEventListener('click', () => onOpen(entry));
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
