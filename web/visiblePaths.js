// visiblePaths.js — visible 필터링 + 일괄 변환 toolbar 갱신.
//
// renderView 가 카드 그리드를 그린 뒤 update*Btn 들이 toolbar 의
// dataset.paths 에 현재 변환 대상 path 리스트를 stash 한다 — 클릭
// 핸들러가 두 번째 필터 패스 없이 그 리스트를 그대로 쓴다.
// selectedPaths 가 있으면 selection ∩ visible, 없으면 visible 전체.

import { $ } from './dom.js';
import { selectedPaths, view } from './state.js';
import { isClipConvertable } from './clipPlayback.js';

export function visibleTSPaths(visible) {
  return visible
    .filter(e => !e.is_dir && e.type === 'video' && e.name.toLowerCase().endsWith('.ts'))
    .map(e => e.path);
}

export function updateConvertAllBtn(visible) {
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

export function updateConvertPNGAllBtn(visible) {
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
  // 현재 대상 PNG 목록을 버튼에 stash해, convertImage 모듈의 click 핸들러가
  // 두 번째 필터 패스 없이 읽을 수 있게 한다.
  $.convertPNGAllBtn.dataset.paths = JSON.stringify(paths);
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
export function updateConvertWebPAllBtn(visible) {
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
