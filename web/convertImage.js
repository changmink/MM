// convertImage.js — PNG → JPG 변환 모달 (동기 JSON 응답)
//
// browse 의존은 wireConvertImage(deps)에서 주입한다 (성공 시 reload).
// SSE 아닌 단일 fetch 응답이라 별도 row 갱신 루프가 없다 — 응답을 받아
// 결과 요약을 렌더하고 사용자가 닫기 누를 때 browse를 재조회한다.

import { $ } from './dom.js';
import { currentPath } from './state.js';
import { esc } from './util.js';
import { wireModalDismiss } from './modalDismiss.js';

const CONVERT_IMAGE_ERROR_LABELS = {
  invalid_path:  '잘못된 경로',
  not_found:     '파일 없음',
  not_a_file:    '폴더는 변환 불가',
  not_png:       'PNG 파일이 아님',
  already_exists:'대상 JPG가 이미 존재',
  decode_failed: 'PNG 디코드 실패',
  encode_failed: 'JPEG 인코드 실패',
  write_failed:  '저장 실패',
  convert_timeout:'변환 시간 초과',
  canceled:      '취소됨',
};

let convertImageSubmitting = false;
let convertImageCompleted = false; // post-response, confirm button becomes "닫기"
let convertImageAnySucceeded = false;
let convertImagePaths = [];
let convertImageAbort = null;
let _browse = null;

export function openConvertImageModal(paths) {
  convertImagePaths = paths.slice();
  $.convertImageError.textContent = '';
  $.convertImageError.classList.add('hidden');
  $.convertImageRows.innerHTML = '';
  $.convertImageSummary.textContent = '';
  $.convertImageResult.classList.add('hidden');
  $.convertImageDeleteOrig.checked = false;
  $.convertImageDeleteOrig.disabled = false;
  $.convertImageConfirmBtn.disabled = false;
  $.convertImageConfirmBtn.textContent = '시작';
  convertImageAnySucceeded = false;
  convertImageCompleted = false;
  $.convertImageFileList.innerHTML = paths
    .map(p => `<li>${esc(p)}</li>`)
    .join('');
  $.convertImageModal.classList.remove('hidden');
}

function closeConvertImageModal() {
  if (convertImageSubmitting && convertImageAbort) {
    // r.Context() cancel propagates → handler returns canceled stubs for
    // remaining paths; in-flight conversion finishes its temp cleanup.
    convertImageAbort.abort();
  }
  $.convertImageModal.classList.add('hidden');
  if (convertImageAnySucceeded) {
    convertImageAnySucceeded = false;
    _browse(currentPath, false);
  }
}

async function onConfirmClick() {
  if (convertImageSubmitting) return;
  if (convertImageCompleted) {
    closeConvertImageModal();
    return;
  }
  if (convertImagePaths.length === 0) {
    showConvertImageError('변환할 파일이 없습니다.');
    return;
  }

  $.convertImageError.classList.add('hidden');
  $.convertImageRows.innerHTML = '';
  $.convertImageSummary.textContent = '';
  $.convertImageResult.classList.add('hidden');
  convertImageSubmitting = true;
  $.convertImageConfirmBtn.disabled = true;
  $.convertImageConfirmBtn.textContent = '변환 중...';
  $.convertImageDeleteOrig.disabled = true;
  convertImageAbort = new AbortController();

  try {
    const res = await fetch('/api/convert-image', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        paths: convertImagePaths,
        delete_original: $.convertImageDeleteOrig.checked,
      }),
      signal: convertImageAbort.signal,
    });
    if (!res.ok) {
      let msg = `요청 실패 (${res.status})`;
      try {
        const body = await res.json();
        if (body.error) msg = `${msg}: ${body.error}`;
      } catch {}
      showConvertImageError(msg);
      $.convertImageConfirmBtn.textContent = '시작';
      $.convertImageConfirmBtn.disabled = false;
      return;
    }
    const body = await res.json();
    renderConvertImageResult(body);
    if (body.succeeded > 0) convertImageAnySucceeded = true;
    convertImageCompleted = true;
    $.convertImageConfirmBtn.textContent = '닫기';
    $.convertImageConfirmBtn.disabled = false;
  } catch (e) {
    if (e.name !== 'AbortError') {
      showConvertImageError('네트워크 오류: ' + e.message);
      $.convertImageConfirmBtn.textContent = '시작';
      $.convertImageConfirmBtn.disabled = false;
    }
  } finally {
    convertImageSubmitting = false;
    convertImageAbort = null;
    $.convertImageDeleteOrig.disabled = false;
  }
}

function renderConvertImageResult(body) {
  $.convertImageResult.classList.remove('hidden');
  $.convertImageSummary.textContent =
    `성공 ${body.succeeded}개 / 실패 ${body.failed}개`;
  $.convertImageSummary.className =
    body.failed > 0 ? 'url-summary url-summary-warn' : 'url-summary url-summary-ok';
  // Show every result row so the user sees what failed and why; success
  // rows are concise (filename → output) and failure rows include the
  // localized error label.
  $.convertImageRows.innerHTML = body.results.map(r => {
    if (r.error) {
      const label = CONVERT_IMAGE_ERROR_LABELS[r.error] || r.error;
      return `<li class="url-row url-row-error">
        <span class="url-row-name">${esc(r.path)}</span>
        <span class="url-row-status">실패: ${esc(label)}</span>
      </li>`;
    }
    const warns = (r.warnings || []).length
      ? ` (${r.warnings.map(w => CONVERT_IMAGE_ERROR_LABELS[w] || w).join(', ')})`
      : '';
    return `<li class="url-row url-row-done">
      <span class="url-row-name">${esc(r.path)}</span>
      <span class="url-row-status">→ ${esc(r.name)}${esc(warns)}</span>
    </li>`;
  }).join('');
}

function showConvertImageError(msg) {
  $.convertImageError.textContent = msg;
  $.convertImageError.classList.remove('hidden');
}

export function wireConvertImage(deps) {
  _browse = deps.browse;

  $.convertImageCancelBtn.addEventListener('click', closeConvertImageModal);
  $.convertImageConfirmBtn.addEventListener('click', onConfirmClick);
  wireModalDismiss($.convertImageModal, closeConvertImageModal);

  // Toolbar batch trigger — populated by browse.js's renderView via the
  // hidden #convert-png-all-btn data attribute.
  $.convertPNGAllBtn.addEventListener('click', () => {
    const paths = $.convertPNGAllBtn.dataset.paths
      ? JSON.parse($.convertPNGAllBtn.dataset.paths)
      : [];
    if (paths.length === 0) return;
    openConvertImageModal(paths);
  });
}
