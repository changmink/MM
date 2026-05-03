// selection.js — 카드 selection 상태 + selection 종속 UI 갱신.
//
// selectedPaths Set 변경 시 visible 카드의 .selected/checkbox 상태와
// toolbar(상단 인디케이터·일괄 변환 버튼)를 동기화한다. 핵심 invariant:
// renderView 전체 재구축을 거치지 않아 GIF/WebP 자동재생 리셋과
// listener 재할당을 피한다 (SPEC §2.5.6).
//
// applyView/allEntries 는 browse.js 도메인 — 단방향 의존을 위해
// wireSelection 시점에 computeVisible closure 로 주입받는다.

import { $ } from './dom.js';
import {
  selectedPaths,
  visibleFilePaths,
  setVisibleFilePaths,
} from './state.js';
import {
  updateConvertPNGAllBtn,
  updateConvertWebPAllBtn,
} from './visiblePaths.js';
import { updateDownloadSelectionBtn } from './download.js';
import { updateDeleteSelectionBtn } from './fileOps.js';

// computeVisible: () => Entry[]  — applyView(allEntries) 결과. wireSelection
// 시점에 browse.js 가 주입. 미주입 시 refreshSelectionUI 는 빈 visible 로
// 동작(테스트 격리 시나리오 안전망).
let _computeVisible = null;

export function wireSelection({ computeVisible }) {
  _computeVisible = computeVisible;

  // selection 툴바 — 카드 DOM은 건드리지 않고 영향받는 카드의 class/checkbox만
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
}

export function syncSelectionWithVisible(entries) {
  setVisibleFilePaths(entries.filter(e => !e.is_dir).map(e => e.path));
  const visibleSet = new Set(visibleFilePaths);
  for (const path of Array.from(selectedPaths)) {
    if (!visibleSet.has(path)) selectedPaths.delete(path);
  }
}

export function renderSelectionControls() {
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
export function updateCardSelection(path, selected) {
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
export function refreshSelectionUI() {
  const visible = _computeVisible ? _computeVisible() : [];
  updateConvertPNGAllBtn(visible);
  updateConvertWebPAllBtn(visible);
  updateDownloadSelectionBtn();
  updateDeleteSelectionBtn();
  renderSelectionControls();
}

export function setSelected(path, selected) {
  if (selected) selectedPaths.add(path);
  else selectedPaths.delete(path);
  updateCardSelection(path, selected);
  refreshSelectionUI();
}

export function bindEntrySelection(container, entry) {
  const checkbox = container.querySelector('input[type="checkbox"]');
  // 폴더는 절대 다중 선택되지 않는다 — 폴더 이동은 항상 단일 대상 연산이다.
  // 잔재 selection 셋이 폴더를 일괄 파일 이동에 끌고 들어가지 않도록
  // checkbox를 완전히 숨긴다.
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
