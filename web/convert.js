// convert.js — TS → MP4 ffmpeg remux 모달 + SSE consumer
//
// browse 의존은 wireConvert(deps)에서 주입한다 (closeConvertModal 후 reload).

import { $ } from './dom.js';
import { currentPath } from './state.js';
import { esc, formatSize, consumeSSE } from './util.js';

const CONVERT_ERROR_LABELS = {
  invalid_path: '잘못된 경로',
  not_found: '파일 없음',
  not_a_file: '파일이 아님',
  not_ts: 'TS 파일이 아님',
  already_exists: '같은 이름의 MP4 존재',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  ffmpeg_error: '변환 실패 (손상되었거나 비호환 코덱)',
  convert_timeout: '타임아웃 (10분)',
  canceled: '취소됨',
  write_error: '저장 실패',
};

let convertSubmitting = false;
let convertAnySucceeded = false;
let convertPaths = [];
let convertAbort = null;
let _browse = null;

export function openConvertModal(paths) {
  convertPaths = paths.slice();
  $.convertError.textContent = '';
  $.convertError.classList.add('hidden');
  $.convertRows.innerHTML = '';
  $.convertSummary.textContent = '';
  $.convertSummary.className = 'url-summary hidden';
  $.convertResult.classList.add('hidden');
  $.convertDeleteOrig.checked = false;
  $.convertDeleteOrig.disabled = false;
  $.convertConfirmBtn.disabled = false;
  $.convertConfirmBtn.textContent = '시작';
  convertAnySucceeded = false;
  // Build the list preview so the user sees exactly what will run.
  $.convertFileList.innerHTML = paths
    .map(p => `<li>${esc(p)}</li>`)
    .join('');
  $.convertModal.classList.remove('hidden');
}

function closeConvertModal() {
  if (convertSubmitting && convertAbort) {
    // Client disconnect flows to the handler as r.Context() cancel → backend
    // kills ffmpeg and cleans the temp file.
    convertAbort.abort();
  }
  $.convertModal.classList.add('hidden');
  if (convertAnySucceeded) {
    convertAnySucceeded = false;
    _browse(currentPath, false);
  }
}

async function submitConvert() {
  if (convertSubmitting) return;
  if (convertPaths.length === 0) {
    showConvertError('변환할 파일이 없습니다.');
    return;
  }

  $.convertError.classList.add('hidden');
  $.convertRows.innerHTML = '';
  $.convertSummary.textContent = '';
  $.convertSummary.className = 'url-summary hidden';
  $.convertResult.classList.remove('hidden');
  // Pre-create one pending row per path so users see immediate feedback.
  convertPaths.forEach((p, i) => ensureConvertRow(i, p));
  convertSubmitting = true;
  $.convertConfirmBtn.disabled = true;
  $.convertConfirmBtn.textContent = '변환 중...';
  $.convertDeleteOrig.disabled = true;
  convertAbort = new AbortController();

  try {
    const res = await fetch('/api/convert', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({
        paths: convertPaths,
        delete_original: $.convertDeleteOrig.checked,
      }),
      signal: convertAbort.signal,
    });
    if (!res.ok) {
      let msg = '';
      try { msg = (await res.json()).error || ''; } catch { /* not JSON */ }
      if (!msg) msg = `요청 실패 (${res.status})`;
      showConvertError(msg);
      $.convertResult.classList.add('hidden');
      return;
    }
    await consumeSSE(res, handleConvertSSEEvent);
  } catch (e) {
    if (e.name !== 'AbortError') {
      showConvertError('요청 실패: ' + e.message);
    }
  } finally {
    convertSubmitting = false;
    convertAbort = null;
    $.convertConfirmBtn.disabled = false;
    $.convertConfirmBtn.textContent = '시작';
    // Leave the delete checkbox disabled after a run so the user re-opens the
    // modal (fresh state) rather than re-submitting the same list.
  }
}

function ensureConvertRow(index, fallbackPath) {
  let row = $.convertRows.querySelector(`[data-index="${index}"]`);
  if (row) return row;
  row = document.createElement('div');
  row.className = 'url-row status-pending';
  row.dataset.index = String(index);
  row.dataset.total = '0';
  row.innerHTML = `
    <div class="url-row-head">
      <span class="url-row-name">${esc(fallbackPath || '')}</span>
      <span class="url-row-status">대기 중</span>
    </div>
    <div class="url-progress-bar"><div class="url-progress-fill"></div></div>
  `;
  $.convertRows.appendChild(row);
  return row;
}

function setRowStatus(row, statusClass, statusText) {
  row.classList.remove('status-pending', 'status-downloading', 'status-done', 'status-error', 'status-cancelled');
  row.classList.add(statusClass);
  row.querySelector('.url-row-status').textContent = statusText;
}

function handleConvertSSEEvent(ev) {
  switch (ev.phase) {
    case 'start': {
      const row = ensureConvertRow(ev.index, ev.path);
      row.querySelector('.url-row-name').textContent = ev.name || ev.path;
      const total = Number(ev.total) || 0;
      row.dataset.total = String(total);
      const sizeText = total > 0 ? formatSize(total) : '크기 미상';
      setRowStatus(row, 'status-downloading', '변환 중 · ' + sizeText);
      break;
    }
    case 'progress': {
      const row = $.convertRows.querySelector(`[data-index="${ev.index}"]`);
      if (!row) return;
      const total = Number(row.dataset.total) || 0;
      if (total > 0) {
        // Output MP4 is ≈ src size for stream-copy remux, so received/total
        // is a reasonable progress proxy (not exact).
        const pct = Math.min(100, (ev.received / total) * 100);
        row.querySelector('.url-progress-fill').style.width = pct.toFixed(1) + '%';
        row.querySelector('.url-row-status').textContent =
          `${formatSize(ev.received)} / ${formatSize(total)} · ${Math.floor(pct)}%`;
      } else {
        row.querySelector('.url-row-status').textContent = formatSize(ev.received);
      }
      break;
    }
    case 'done': {
      const row = ensureConvertRow(ev.index, ev.path);
      row.querySelector('.url-row-name').textContent = ev.name || ev.path;
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warns = (ev.warnings || []).map(w =>
        w === 'delete_original_failed' ? '원본 삭제 실패' : w
      );
      const warnText = warns.length ? ` · ${warns.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warnText}`);
      convertAnySucceeded = true;
      break;
    }
    case 'error': {
      const row = ensureConvertRow(ev.index, ev.path);
      const label = CONVERT_ERROR_LABELS[ev.error] || ev.error || '알 수 없는 오류';
      setRowStatus(row, 'status-error', '실패 · ' + label);
      break;
    }
    case 'summary': {
      const cls = ev.failed === 0 ? 'status-done'
                : ev.succeeded === 0 ? 'status-error'
                : 'status-mixed';
      $.convertSummary.className = 'url-summary ' + cls;
      $.convertSummary.textContent = `성공 ${ev.succeeded}개 · 실패 ${ev.failed}개`;
      break;
    }
  }
}

function showConvertError(msg) {
  $.convertError.textContent = msg;
  $.convertError.classList.remove('hidden');
}

export function wireConvert(deps) {
  _browse = deps.browse;

  $.convertCancelBtn.addEventListener('click', closeConvertModal);
  $.convertConfirmBtn.addEventListener('click', submitConvert);
  $.convertModal.addEventListener('click', e => {
    if (e.target === $.convertModal) closeConvertModal();
  });
  document.addEventListener('keydown', e => {
    if ($.convertModal.classList.contains('hidden')) return;
    if (e.key === 'Escape') closeConvertModal();
  });
}
