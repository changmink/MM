// sseConvertModal.js — POST/SSE 일괄 변환 모달 팩토리.
//
// /api/convert (TS → MP4) 와 /api/convert-webp (움짤 → WebP) 두 흐름이 모달
// 골격·SSE 이벤트 스키마(start/progress/done/error/summary)·진행 행 UI 를
// 공유한다. 차이는 endpoint, 에러/경고 라벨 사전, DOM 타깃, 선택적 툴바
// 일괄 버튼뿐이라 각 차이를 옵션으로 받는 팩토리로 묶었다.
//
// URL 임포트(/api/import-url)는 batch 레지스트리·per-URL 취소·orphan
// finalize 가 얽혀 있어 이 팩토리 적용 대상이 아니다 (urlImport.js 별도).

import { currentPath } from './state.js';
import { esc, formatSize, consumeSSE } from './util.js';

export function createConvertModal({
  endpoint,
  errorLabels,
  warnLabels,
  dom,
  startBtnLabel = '시작',
  busyBtnLabel = '변환 중...',
}) {
  let submitting = false;
  let anySucceeded = false;
  let paths = [];
  let abort = null;
  let _browse = null;

  function open(items) {
    paths = items.slice();
    dom.error.textContent = '';
    dom.error.classList.add('hidden');
    dom.rows.innerHTML = '';
    dom.summary.textContent = '';
    dom.summary.className = 'url-summary hidden';
    dom.result.classList.add('hidden');
    dom.deleteOrig.checked = false;
    dom.deleteOrig.disabled = false;
    dom.confirmBtn.disabled = false;
    dom.confirmBtn.textContent = startBtnLabel;
    anySucceeded = false;
    dom.fileList.innerHTML = paths.map(p => `<li>${esc(p)}</li>`).join('');
    dom.modal.classList.remove('hidden');
  }

  function close() {
    if (submitting && abort) {
      // 클라이언트 abort → r.Context() cancel → 서버가 ffmpeg 종료 + tmp 정리.
      abort.abort();
    }
    dom.modal.classList.add('hidden');
    if (anySucceeded) {
      anySucceeded = false;
      _browse(currentPath, false);
    }
  }

  async function submit() {
    if (submitting) return;
    if (paths.length === 0) {
      showError('변환할 파일이 없습니다.');
      return;
    }
    dom.error.classList.add('hidden');
    dom.rows.innerHTML = '';
    dom.summary.textContent = '';
    dom.summary.className = 'url-summary hidden';
    dom.result.classList.remove('hidden');
    paths.forEach((p, i) => ensureRow(i, p));
    submitting = true;
    dom.confirmBtn.disabled = true;
    dom.confirmBtn.textContent = busyBtnLabel;
    dom.deleteOrig.disabled = true;
    abort = new AbortController();

    try {
      const res = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
        body: JSON.stringify({
          paths,
          delete_original: dom.deleteOrig.checked,
        }),
        signal: abort.signal,
      });
      if (!res.ok) {
        let msg = '';
        try { msg = (await res.json()).error || ''; } catch { /* not JSON */ }
        if (!msg) msg = `요청 실패 (${res.status})`;
        showError(msg);
        dom.result.classList.add('hidden');
        return;
      }
      await consumeSSE(res, handleEvent);
    } catch (e) {
      if (e.name !== 'AbortError') {
        showError('요청 실패: ' + e.message);
      }
    } finally {
      submitting = false;
      abort = null;
      dom.confirmBtn.disabled = false;
      dom.confirmBtn.textContent = startBtnLabel;
    }
  }

  function ensureRow(index, fallbackPath) {
    let row = dom.rows.querySelector(`[data-index="${index}"]`);
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
    dom.rows.appendChild(row);
    return row;
  }

  function setRowStatus(row, statusClass, statusText) {
    row.classList.remove('status-pending', 'status-downloading', 'status-done', 'status-error', 'status-cancelled');
    row.classList.add(statusClass);
    row.querySelector('.url-row-status').textContent = statusText;
  }

  function handleEvent(ev) {
    switch (ev.phase) {
      case 'start': {
        const row = ensureRow(ev.index, ev.path);
        row.querySelector('.url-row-name').textContent = ev.name || ev.path;
        const total = Number(ev.total) || 0;
        row.dataset.total = String(total);
        const sizeText = total > 0 ? formatSize(total) : '크기 미상';
        setRowStatus(row, 'status-downloading', '변환 중 · ' + sizeText);
        break;
      }
      case 'progress': {
        const row = dom.rows.querySelector(`[data-index="${ev.index}"]`);
        if (!row) return;
        const total = Number(row.dataset.total) || 0;
        if (total > 0) {
          // 출력 크기는 입력 크기 근사로 진행률을 가늠 — 정확치 않아 100%로 cap.
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
        const row = ensureRow(ev.index, ev.path);
        row.querySelector('.url-row-name').textContent = ev.name || ev.path;
        row.querySelector('.url-progress-fill').style.width = '100%';
        const warns = (ev.warnings || []).map(w => warnLabels[w] || w);
        const warnText = warns.length ? ` · ${warns.join(', ')}` : '';
        setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warnText}`);
        anySucceeded = true;
        break;
      }
      case 'error': {
        const row = ensureRow(ev.index, ev.path);
        const label = errorLabels[ev.error] || ev.error || '알 수 없는 오류';
        setRowStatus(row, 'status-error', '실패 · ' + label);
        break;
      }
      case 'summary': {
        const cls = ev.failed === 0 ? 'status-done'
                  : ev.succeeded === 0 ? 'status-error'
                  : 'status-mixed';
        dom.summary.className = 'url-summary ' + cls;
        dom.summary.textContent = `성공 ${ev.succeeded}개 · 실패 ${ev.failed}개`;
        break;
      }
    }
  }

  function showError(msg) {
    dom.error.textContent = msg;
    dom.error.classList.remove('hidden');
  }

  function wire(deps) {
    _browse = deps.browse;
    dom.cancelBtn.addEventListener('click', close);
    dom.confirmBtn.addEventListener('click', submit);
    dom.modal.addEventListener('click', e => {
      if (e.target === dom.modal) close();
    });
    document.addEventListener('keydown', e => {
      if (dom.modal.classList.contains('hidden')) return;
      if (e.key === 'Escape') close();
    });
    if (dom.allBtn) {
      // 툴바 일괄 버튼 — browse.js 가 dataset.paths 에 현재 대상 목록을
      // 직렬화해 둔다. 클릭은 그걸 파싱해 모달 열기.
      dom.allBtn.addEventListener('click', () => {
        const list = dom.allBtn.dataset.paths
          ? JSON.parse(dom.allBtn.dataset.paths)
          : [];
        if (list.length === 0) return;
        open(list);
      });
    }
  }

  return { open, wire };
}
