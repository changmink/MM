// dragSelect.js — Rubber-band 영역 선택 (SPEC §2.5.4).
//
// 빈 영역에서 시작한 마우스 드래그로 사각형을 그려 그 안의 visible 카드를
// 일괄 선택한다. selectedPaths Set과 카드 .selected 클래스 토글은 기존
// browse.js의 setSelected → renderView 흐름을 재사용한다.
//
// 빈 영역 시작 vs 카드 시작 분기로 기존 폴더 이동 DnD와 충돌하지 않는다 —
// HTML5 dragstart는 카드 위 mousedown에서만 발화하므로 우리 핸들러가
// "카드 위에서 시작"을 closest()로 거른다.

import { selectedPaths } from './state.js';
import { renderView } from './browse.js';

const MOVE_THRESHOLD = 5;
const MOBILE_MAX_WIDTH = 600;

// 인터랙티브 요소 위에서 시작한 mousedown은 기존 동작에 위임 (rubber-band 미발생).
const INTERACTIVE_SELECTOR =
  '.thumb-card, tr, button, a, input, label, ' +
  '.lightbox, .modal-overlay, .audio-player';

let active = null;

export function wireDragSelect() {
  const main = document.querySelector('main');
  if (!main) return;
  main.addEventListener('mousedown', onMouseDown);
}

function onMouseDown(e) {
  if (e.button !== 0) return;
  if (window.innerWidth <= MOBILE_MAX_WIDTH) return;
  if (e.target.closest(INTERACTIVE_SELECTOR)) return;

  const cards = collectVisibleCards();
  if (cards.length === 0) return;

  active = {
    startX: e.clientX,
    startY: e.clientY,
    overlay: null,
    cards,
    additive: e.ctrlKey || e.metaKey || e.shiftKey,
    snapshot: new Set(selectedPaths),
  };

  document.addEventListener('mousemove', onMouseMove);
  document.addEventListener('mouseup', onMouseUp);
  document.addEventListener('keydown', onKeyDown);
}

// mousedown 시점 1회만 호출 — 드래그 중 layout이 변하지 않는다고 가정한다.
// (selection 변경이 renderView를 호출하지만 카드 DOM은 재생성되지 않고
// .selected 클래스만 토글된다.)
function collectVisibleCards() {
  const list = document.querySelector('#file-list');
  if (!list) return [];
  const out = [];
  list.querySelectorAll('.thumb-card[data-path], tr[data-path]').forEach(el => {
    // 폴더 카드는 .select-check / .select-cell 의 visibility:hidden 으로 식별.
    // bindEntrySelection이 폴더는 체크박스를 숨겨 selectedPaths 대상이 아니다.
    const cell = el.querySelector('.select-cell, .select-check');
    if (cell && cell.style.visibility === 'hidden') return;
    out.push({ el, path: el.dataset.path, rect: el.getBoundingClientRect() });
  });
  return out;
}

function onMouseMove(e) {
  if (!active) return;
  const dx = e.clientX - active.startX;
  const dy = e.clientY - active.startY;

  if (!active.overlay && Math.hypot(dx, dy) < MOVE_THRESHOLD) return;

  if (!active.overlay) {
    document.body.classList.add('drag-selecting');
    active.overlay = document.createElement('div');
    active.overlay.className = 'drag-select-overlay';
    document.body.appendChild(active.overlay);
  }

  // 사각형 (clientX/Y 좌표계 — getBoundingClientRect와 일치).
  const rect = {
    left:   Math.min(active.startX, e.clientX),
    top:    Math.min(active.startY, e.clientY),
    right:  Math.max(active.startX, e.clientX),
    bottom: Math.max(active.startY, e.clientY),
  };

  // overlay는 page 좌표계 (document.body 기준 absolute) — scrollX/Y 보정.
  active.overlay.style.left   = (rect.left + window.scrollX) + 'px';
  active.overlay.style.top    = (rect.top  + window.scrollY) + 'px';
  active.overlay.style.width  = (rect.right  - rect.left)    + 'px';
  active.overlay.style.height = (rect.bottom - rect.top)     + 'px';

  applySelection(rect);
}

function applySelection(rect) {
  // 기본: 시작 시점 selection 클리어 후 사각형 결과로 대체.
  // additive(Ctrl/Shift): 시작 시점 selection 위에 사각형 결과를 추가.
  selectedPaths.clear();
  if (active.additive) {
    active.snapshot.forEach(p => selectedPaths.add(p));
  }
  for (const c of active.cards) {
    if (rectsIntersect(rect, c.rect)) selectedPaths.add(c.path);
  }
  renderView();
}

function rectsIntersect(a, b) {
  return a.left <= b.right && a.right >= b.left
      && a.top  <= b.bottom && a.bottom >= b.top;
}

function onMouseUp() {
  cleanup();
}

function onKeyDown(e) {
  if (e.key !== 'Escape' || !active) return;
  // 시작 시점 selection 복원.
  selectedPaths.clear();
  active.snapshot.forEach(p => selectedPaths.add(p));
  renderView();
  cleanup();
}

function cleanup() {
  if (!active) return;
  if (active.overlay) {
    active.overlay.remove();
    document.body.classList.remove('drag-selecting');
  }
  document.removeEventListener('mousemove', onMouseMove);
  document.removeEventListener('mouseup', onMouseUp);
  document.removeEventListener('keydown', onKeyDown);
  active = null;
}
