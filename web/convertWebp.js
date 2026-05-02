// convertWebp.js — 움짤 → animated WebP 변환 모달 + SSE consumer
//
// 동작은 convert.js (TS → MP4) 와 평행. wire 시 browse 콜백을 주입받아
// 변환 성공 후 폴더를 새로고침한다.

import { $ } from './dom.js';
import { currentPath } from './state.js';
import { esc, formatSize, consumeSSE } from './util.js';

const CONVERT_WEBP_ERROR_LABELS = {
  invalid_path: '잘못된 경로',
  not_found: '파일 없음',
  not_a_file: '파일이 아님',
  unsupported_input: '지원하지 않는 입력 (이미지·오디오·기타)',
  not_clip: '움짤 조건 미충족 (50 MiB 또는 30s 초과)',
  duration_unknown: '동영상 길이 확인 실패',
  already_exists: '같은 이름의 WebP 존재',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  ffmpeg_error: '인코딩 실패 (손상되었거나 비호환 코덱)',
  convert_timeout: '타임아웃 (5분)',
  canceled: '취소됨',
  write_error: '저장 실패',
};

const CONVERT_WEBP_WARN_LABELS = {
  audio_dropped: '오디오 제거됨',
  delete_original_failed: '원본 삭제 실패',
};

let convertWebpSubmitting = false;
let convertWebpAnySucceeded = false;
let convertWebpPaths = [];
let convertWebpAbort = null;
let _browse = null;

export function openConvertWebPModal(paths) {
  convertWebpPaths = paths.slice();
  $.convertWebpError.textContent = '';
  $.convertWebpError.classList.add('hidden');
  $.convertWebpRows.innerHTML = '';
  $.convertWebpSummary.textContent = '';
  $.convertWebpSummary.className = 'url-summary hidden';
  $.convertWebpResult.classList.add('hidden');
  $.convertWebpDeleteOrig.checked = false;
  $.convertWebpDeleteOrig.disabled = false;
  $.convertWebpConfirmBtn.disabled = false;
  $.convertWebpConfirmBtn.textContent = '시작';
  convertWebpAnySucceeded = false;
  $.convertWebpFileList.innerHTML = paths
    .map(p => `<li>${esc(p)}</li>`)
    .join('');
  $.convertWebpModal.classList.remove('hidden');
}

function closeConvertWebPModal() {
  if (convertWebpSubmitting && convertWebpAbort) {
    // Client disconnect → r.Context() cancel → backend kills ffmpeg + cleans tmp.
    convertWebpAbort.abort();
  }
  $.convertWebpModal.classList.add('hidden');
  if (convertWebpAnySucceeded) {
    convertWebpAnySucceeded = false;
    _browse(currentPath, false);
  }
}

async function submitConvertWebP() {
  if (convertWebpSubmitting) return;
  if (convertWebpPaths.length === 0) {
    showConvertWebPError('변환할 파일이 없습니다.');
    return;
  }

  $.convertWebpError.classList.add('hidden');
  $.convertWebpRows.innerHTML = '';
  $.convertWebpSummary.textContent = '';
  $.convertWebpSummary.className = 'url-summary hidden';
  $.convertWebpResult.classList.remove('hidden');
  // Pre-create one pending row per path so the modal feels responsive.
  convertWebpPaths.forEach((p, i) => ensureWebpRow(i, p));
  convertWebpSubmitting = true;
  $.convertWebpConfirmBtn.disabled = true;
  $.convertWebpConfirmBtn.textContent = '변환 중...';
  $.convertWebpDeleteOrig.disabled = true;
  convertWebpAbort = new AbortController();

  try {
    const res = await fetch('/api/convert-webp', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({
        paths: convertWebpPaths,
        delete_original: $.convertWebpDeleteOrig.checked,
      }),
      signal: convertWebpAbort.signal,
    });
    if (!res.ok) {
      let msg = '';
      try { msg = (await res.json()).error || ''; } catch { /* not JSON */ }
      if (!msg) msg = `요청 실패 (${res.status})`;
      showConvertWebPError(msg);
      $.convertWebpResult.classList.add('hidden');
      return;
    }
    await consumeSSE(res, handleConvertWebPSSEEvent);
  } catch (e) {
    if (e.name !== 'AbortError') {
      showConvertWebPError('요청 실패: ' + e.message);
    }
  } finally {
    convertWebpSubmitting = false;
    convertWebpAbort = null;
    $.convertWebpConfirmBtn.disabled = false;
    $.convertWebpConfirmBtn.textContent = '시작';
  }
}

function ensureWebpRow(index, fallbackPath) {
  let row = $.convertWebpRows.querySelector(`[data-index="${index}"]`);
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
  $.convertWebpRows.appendChild(row);
  return row;
}

function setRowStatus(row, statusClass, statusText) {
  row.classList.remove('status-pending', 'status-downloading', 'status-done', 'status-error', 'status-cancelled');
  row.classList.add(statusClass);
  row.querySelector('.url-row-status').textContent = statusText;
}

function handleConvertWebPSSEEvent(ev) {
  switch (ev.phase) {
    case 'start': {
      const row = ensureWebpRow(ev.index, ev.path);
      row.querySelector('.url-row-name').textContent = ev.name || ev.path;
      const total = Number(ev.total) || 0;
      row.dataset.total = String(total);
      const sizeText = total > 0 ? formatSize(total) : '크기 미상';
      setRowStatus(row, 'status-downloading', '변환 중 · ' + sizeText);
      break;
    }
    case 'progress': {
      const row = $.convertWebpRows.querySelector(`[data-index="${ev.index}"]`);
      if (!row) return;
      const total = Number(row.dataset.total) || 0;
      if (total > 0) {
        // Output WebP can be larger or smaller than the source mp4; pct
        // capped at 100 keeps the bar from overflowing for upsized cases.
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
      const row = ensureWebpRow(ev.index, ev.path);
      row.querySelector('.url-row-name').textContent = ev.name || ev.path;
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warns = (ev.warnings || []).map(w => CONVERT_WEBP_WARN_LABELS[w] || w);
      const warnText = warns.length ? ` · ${warns.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warnText}`);
      convertWebpAnySucceeded = true;
      break;
    }
    case 'error': {
      const row = ensureWebpRow(ev.index, ev.path);
      const label = CONVERT_WEBP_ERROR_LABELS[ev.error] || ev.error || '알 수 없는 오류';
      setRowStatus(row, 'status-error', '실패 · ' + label);
      break;
    }
    case 'summary': {
      const cls = ev.failed === 0 ? 'status-done'
                : ev.succeeded === 0 ? 'status-error'
                : 'status-mixed';
      $.convertWebpSummary.className = 'url-summary ' + cls;
      $.convertWebpSummary.textContent = `성공 ${ev.succeeded}개 · 실패 ${ev.failed}개`;
      break;
    }
  }
}

function showConvertWebPError(msg) {
  $.convertWebpError.textContent = msg;
  $.convertWebpError.classList.remove('hidden');
}

export function wireConvertWebP(deps) {
  _browse = deps.browse;

  $.convertWebpCancelBtn.addEventListener('click', closeConvertWebPModal);
  $.convertWebpConfirmBtn.addEventListener('click', submitConvertWebP);
  $.convertWebpModal.addEventListener('click', e => {
    if (e.target === $.convertWebpModal) closeConvertWebPModal();
  });
  document.addEventListener('keydown', e => {
    if ($.convertWebpModal.classList.contains('hidden')) return;
    if (e.key === 'Escape') closeConvertWebPModal();
  });

  // 툴바 일괄 버튼 — browse.js 의 updateConvertWebPAllBtn 이 dataset.paths 에
  // 현재 대상 목록을 직렬화해 둔다. 클릭은 그걸 파싱해 모달 열기.
  $.convertWebpAllBtn.addEventListener('click', () => {
    const paths = $.convertWebpAllBtn.dataset.paths
      ? JSON.parse($.convertWebpAllBtn.dataset.paths)
      : [];
    if (paths.length === 0) return;
    openConvertWebPModal(paths);
  });
}
