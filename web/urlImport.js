// urlImport.js — URL 가져오기 모달 + POST flow + SSE handler + 배치 상태
//
// urlImportJobs.js 에서 cancel/dismiss 함수와 browse 를 setURLImportDeps()로 주입한다
// (urlImport → jobs 의존을 끊어 단방향 import 만 유지).

import { $ } from './dom.js';
import { currentPath } from './state.js';
import { esc, formatSize, consumeSSE } from './util.js';
import { wireModalDismiss } from './modalDismiss.js';

// Labels intentionally omit specific byte/time limits because those are
// configurable at runtime via /api/settings — hardcoding numbers here made
// the UI lie when the user had bumped the cap or timeout.
export const URL_ERROR_LABELS = {
  missing_content_length: 'Content-Length 헤더 없음',
  too_large: '크기 상한 초과',
  unsupported_content_type: '지원하지 않는 미디어 타입',
  invalid_scheme: '지원하지 않는 스킴',
  invalid_url: '잘못된 URL',
  http_error: 'HTTP 응답 에러',
  connect_timeout: '연결 타임아웃',
  download_timeout: '다운로드 타임아웃',
  tls_error: 'TLS 검증 실패',
  too_many_redirects: '리다이렉트 과다',
  network_error: '네트워크 오류',
  write_error: '저장 실패',
  ffmpeg_error: 'HLS 리먹싱 실패',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  hls_playlist_too_large: 'HLS 플레이리스트 크기 초과',
  // `cancelled` is handled as a distinct row state (not a failure) — see
  // handleSSEEvent's error branch and applyURLStateToRow's "cancelled"
  // case. No dictionary entry needed.
};

// Each batch captures one in-flight POST /api/import-url. Several batches
// can coexist: while one is running, reopening the modal turns the confirm
// button into "새 배치 추가" and additional submits append a new batch
// below the existing rows. The server serializes them via `importSem`,
// emitting a `queued` SSE event we surface as a "waiting" row state.
//
// We deliberately do NOT wire up an AbortController per batch — closing
// the modal must keep the fetch running so the user can reopen and still
// see progress. Browser-initiated fetch aborts (tab close / navigation)
// flow to the server as `r.Context()` cancel.
export const urlBatches = [];
let urlBatchSeq = 0;
export function nextBatchId() { return ++urlBatchSeq; }

// True only during the short POST setup window of an in-flight submit —
// prevents accidental double-submits of the same URL list. Once SSE
// consumption starts we flip it off so the user can queue another batch
// without waiting for this one to finish.
let urlSubmittingNow = false;
// When a round finishes with only failures we keep the batch metadata
// around briefly so the badge can still surface the ⚠ marker the user
// might have missed if they were off-screen. Cleared on the next open or
// the next completion round.
let urlBadgeLinger = false;

// Late-bound deps (set by setURLImportDeps before any user interaction).
let _cancelURLAt = null;
let _cancelBatchAll = null;
let _dismissBatch = null;
let _dismissAllFinishedBatches = null;
let _browse = null;

export function setURLImportDeps(deps) {
  _cancelURLAt = deps.cancelURLAt;
  _cancelBatchAll = deps.cancelBatchAll;
  _dismissBatch = deps.dismissBatch;
  _dismissAllFinishedBatches = deps.dismissAllFinishedBatches;
  _browse = deps.browse;
}

export function anyBatchActive() {
  return urlBatches.some(b => !b.done);
}
export function anyBatchSucceeded() {
  return urlBatches.some(b => b.succeeded > 0);
}

export function openURLModal() {
  $.urlInput.value = '';
  $.urlError.textContent = '';
  $.urlError.classList.add('hidden');

  // Preserve the modal's contents whenever urlBatches has anything to
  // show. That includes:
  //   - active batches (live progress)
  //   - finished batches restored from bootstrapURLJobs (history rows
  //     the user can dismiss via "닫기" / "완료 항목 모두 지우기")
  //   - finished batches still lingering from an in-session error round
  // Only fully-empty registry counts as a "fresh start" — that's when
  // maybeFinalize already cleared everything after a successful round
  // or after the 3-second linger of an error-only round.
  if (urlBatches.length === 0) {
    urlBadgeLinger = false;
    $.urlRows.innerHTML = '';
    $.urlSummary.textContent = '';
    $.urlSummary.className = 'url-summary hidden';
    $.urlResult.classList.add('hidden');
  }

  $.urlModal.classList.remove('hidden');
  updateURLBadge();
  updateConfirmButton();
  $.urlInput.focus();
}

// Keeps the confirm button in sync with the current registry state so
// its label always answers "what happens if I click now?". We re-enter
// here from every state transition: open, submit entry, SSE started,
// batch settled.
export function updateConfirmButton() {
  if (urlSubmittingNow) {
    $.urlConfirmBtn.disabled = true;
    $.urlConfirmBtn.textContent = '가져오는 중...';
  } else if (anyBatchActive()) {
    $.urlConfirmBtn.disabled = false;
    $.urlConfirmBtn.textContent = '새 배치 추가';
  } else {
    $.urlConfirmBtn.disabled = false;
    $.urlConfirmBtn.textContent = '가져오기';
  }
}

function closeURLModal() {
  // Pure view-hide: fetches keep running, the badge takes over the
  // progress surface. Tab close or navigation still aborts via the
  // browser, which the server handles as r.Context() cancel.
  $.urlModal.classList.add('hidden');
  updateURLBadge();
}

export function updateURLBadge() {
  // The "완료 항목 모두 지우기" button lives inside the modal — keep its
  // visibility in lockstep with the registry so the user sees it as soon
  // as a finished batch is available to dismiss.
  const hasFinished = urlBatches.some(b => b.done);
  $.urlClearFinishedBtn.classList.toggle('hidden', !hasFinished);

  const modalHidden = $.urlModal.classList.contains('hidden');
  const shouldShow = modalHidden && (anyBatchActive() || urlBadgeLinger);
  if (!shouldShow) {
    $.urlBadge.classList.add('hidden');
    $.urlBadge.classList.remove('has-error');
    return;
  }
  // Badge aggregates ONLY active batches (restored or freshly POSTed) so
  // the running progress bar reflects what's still in flight. Restored
  // finished batches are background context, not active progress.
  let completed = 0, total = 0, failed = 0;
  for (const b of urlBatches) {
    if (b.done) continue;
    completed += b.succeeded + b.failed + (b.cancelled || 0);
    total     += b.total;
    failed    += b.failed;
  }
  $.urlBadge.classList.remove('hidden');
  $.urlBadge.classList.toggle('has-error', failed > 0);
  $.urlBadge.textContent = `URL ↓ ${completed}/${total}` + (failed > 0 ? ' ⚠' : '');
}

// maybeFinalize runs whenever a batch settles (normal completion, abort,
// network error, or HTTP error). It scopes summarization to "this session's
// round" — batches submitted in this session AND not loaded from the
// server's history snapshot. Restored batches are backdrop UI; aggregating
// them into the round summary would mix prior-session results into the
// current operation's totals.
function maybeFinalize() {
  // Round = batches submitted in this session (restored ones are history).
  const round = urlBatches.filter(b => !b.restored);
  if (round.length === 0) {
    updateURLBadge();
    return;
  }
  if (round.some(b => !b.done)) return;

  let succeeded = 0, failed = 0, cancelled = 0;
  for (const b of round) {
    succeeded += b.succeeded;
    failed    += b.failed;
    cancelled += b.cancelled || 0;
  }

  // Mirror server status precedence (SPEC §2.6): succeeded≥1 → completed
  // even if some failed/cancelled; cancelled-only → cancelled; else error.
  let cls;
  if (succeeded > 0 && failed === 0 && cancelled === 0)      cls = 'status-done';
  else if (succeeded > 0)                                    cls = 'status-mixed';
  else if (failed === 0 && cancelled > 0)                    cls = 'status-cancelled';
  else                                                       cls = 'status-error';

  // The round.length>0 + every-batch-done preconditions guarantee at
  // least one of the three counters is non-zero; parts.join cannot be
  // empty.
  const parts = [];
  if (succeeded > 0) parts.push(`성공 ${succeeded}`);
  if (failed > 0)    parts.push(`실패 ${failed}`);
  if (cancelled > 0) parts.push(`취소 ${cancelled}`);
  $.urlSummary.className = 'url-summary ' + cls;
  $.urlSummary.textContent = parts.join(' · ');

  if (succeeded > 0) {
    // Success: clear ONLY this round (restored history stays). Browse
    // refresh will surface the new files; the modal can keep history rows
    // for the user to dismiss when they want.
    clearRoundBatches(round);
    urlBadgeLinger = false;
    updateURLBadge();
    _browse(currentPath, false);
    return;
  }

  // No success (all failed or all cancelled): keep the badge up briefly so
  // the user can click in to inspect, then clear THIS round (we capture
  // `round` in closure so a new round started in the meantime is unaffected).
  urlBadgeLinger = true;
  updateURLBadge();
  setTimeout(() => {
    clearRoundBatches(round);
    urlBadgeLinger = false;
    updateURLBadge();
  }, 3000);
}

// clearRoundBatches tears down the DOM for each batch and removes it from
// urlBatches, while leaving the rest of the registry alone. Used by
// maybeFinalize so completed rounds graduate out of the active state but
// restored history stays visible until the user dismisses it.
function clearRoundBatches(batches) {
  for (const b of batches) {
    removeBatchRows(b);
    const idx = urlBatches.indexOf(b);
    if (idx !== -1) urlBatches.splice(idx, 1);
  }
}

async function submitURLImport() {
  // Reject only while a POST is mid-flight — once SSE is flowing the user
  // is free to queue another batch on top.
  if (urlSubmittingNow) return;
  const urls = $.urlInput.value
    .split('\n')
    .map(s => s.trim())
    .filter(Boolean);

  if (urls.length === 0) {
    showURLError('URL을 한 줄에 하나씩 입력하세요.');
    return;
  }
  if (urls.length > 50) {
    showURLError('한 번에 최대 50개까지 입력할 수 있습니다.');
    return;
  }

  $.urlError.classList.add('hidden');
  // Wipe DOM only when the registry is truly empty. Restored history (or
  // a still-clearing prior round) stays visible — clearing them here while
  // they remain in urlBatches would desync DOM from internal counters.
  if (urlBatches.length === 0) {
    $.urlRows.innerHTML = '';
    $.urlSummary.textContent = '';
    $.urlSummary.className = 'url-summary hidden';
  }
  $.urlResult.classList.remove('hidden');

  const batch = {
    id: nextBatchId(),
    jobId: null,
    rowEls: new Map(),
    headerEl: null,
    succeeded: 0,
    failed: 0,
    cancelled: 0,
    total: urls.length,
    done: false,
    restored: false,
    eventSource: null,
  };
  urlBatches.push(batch);

  // Position label counts only current-round batches — restored history
  // has its own labels ("복원된 배치"/"이전 결과") so it shouldn't bump the
  // "배치 N" numbering. The first batch in a fresh round gets no label.
  const roundCount = urlBatches.filter(b => !b.restored).length;
  appendBatchHeader(batch, roundCount > 1 ? `배치 ${roundCount}` : '');

  // Pre-create one pending row per URL so users see immediate feedback even
  // before the first SSE event arrives.
  urls.forEach((u, i) => ensureURLRow(batch, i, u));
  // Clear the textarea so the user can immediately type another batch
  // without having to select-all-and-delete first.
  $.urlInput.value = '';

  urlSubmittingNow = true;
  updateConfirmButton();

  // Tracks whether the SSE stream actually opened. The HTTP-error branch
  // pops the batch and returns before flipping this, so orphan-finalize
  // (below) only runs for streams that did open and were then cut.
  let sseOpened = false;

  try {
    const res = await fetch('/api/import-url?path=' + encodeURIComponent(currentPath), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({ urls }),
    });
    if (!res.ok) {
      let msg = '';
      try { msg = (await res.json()).error || ''; } catch { /* not JSON */ }
      if (!msg) msg = `요청 실패 (${res.status})`;
      showURLError(msg);
      // Tear down this batch's DOM regardless — its pre-created rows
      // would otherwise linger as stuck "대기 중" placeholders. If
      // nothing else (active or restored) is in the registry, hide the
      // whole result panel; otherwise leave it visible for siblings.
      removeBatchRows(batch);
      const idx = urlBatches.indexOf(batch);
      if (idx !== -1) urlBatches.splice(idx, 1);
      if (urlBatches.length === 0) {
        $.urlResult.classList.add('hidden');
      }
      return;
    }
    // SSE connected. Free the button so the user can queue another batch
    // without having to wait for this one to finish.
    sseOpened = true;
    urlSubmittingNow = false;
    updateConfirmButton();
    await consumeSSE(res, ev => handleSSEEvent(batch, ev));
  } catch (e) {
    // AbortError can still arrive on browser-initiated cancels (tab
    // navigation) even though we no longer wire up an AbortController —
    // the document's lifecycle signal aborts pending fetches. Swallow
    // those silently; the page is going away anyway.
    if (e.name !== 'AbortError') {
      showURLError('요청 실패: ' + e.message);
    }
  } finally {
    // The server's contract is start → done|error per URL plus a final
    // summary, so any URL row still in pending/downloading state means the
    // stream was cut (network drop, proxy timeout, server crash). Convert
    // those into explicit failures — otherwise the badge / aggregate
    // summary undercount and the round can falsely report "0 failed".
    // Only run when the stream actually opened (sseOpened) — for HTTP
    // errors the batch was already popped from the registry above.
    if (sseOpened) finalizeOrphanRows(batch);
    batch.done = true;
    urlSubmittingNow = false;
    updateBatchControls(batch);
    maybeFinalize();
    updateConfirmButton();
    updateURLBadge();
  }
}

// finalizeOrphanRows promotes any non-terminal row in the batch (still
// pending or downloading) into a "연결 끊김" failure. Called from
// submitURLImport's finally when the SSE stream opened but ended without
// terminal events for every URL.
function finalizeOrphanRows(batch) {
  for (const row of batch.rowEls.values()) {
    if (row.classList.contains('status-done') ||
        row.classList.contains('status-error') ||
        row.classList.contains('status-cancelled')) {
      continue;
    }
    row.classList.remove('url-row-indeterminate');
    setRowStatus(row, 'status-error', '실패 · 연결 끊김');
    batch.failed++;
  }
}

export function ensureURLRow(batch, index, fallbackUrl) {
  let row = batch.rowEls.get(index);
  if (row) return row;
  row = document.createElement('div');
  row.className = 'url-row status-pending';
  row.dataset.batch = String(batch.id);
  row.dataset.index = String(index);
  row.dataset.total = '0';
  row.innerHTML = `
    <div class="url-row-head">
      <span class="url-row-name">${esc(fallbackUrl || '')}</span>
      <span class="url-row-status">대기 중</span>
      <button class="url-row-cancel" type="button" aria-label="이 URL 취소" title="취소">✕</button>
    </div>
    <div class="url-progress-bar"><div class="url-progress-fill"></div></div>
  `;
  // CSS hides this on terminal states; clicks before that fire a per-URL
  // cancel against the server. We let SSE drive the visible state change so
  // a network failure cleanly leaves the button intact for retry.
  row.querySelector('.url-row-cancel')
    .addEventListener('click', () => _cancelURLAt(batch, index));
  $.urlRows.appendChild(row);
  batch.rowEls.set(index, row);
  return row;
}

export function setRowStatus(row, statusClass, statusText) {
  row.classList.remove('status-pending', 'status-downloading', 'status-done', 'status-error', 'status-cancelled');
  row.classList.add(statusClass);
  row.querySelector('.url-row-status').textContent = statusText;
}

export function handleSSEEvent(batch, ev) {
  switch (ev.phase) {
    case 'register': {
      // First frame on POST responses — server hands us the jobId so a
      // refresh can rebind via GET /jobs/{id}/events. Restored batches
      // already have jobId from the bootstrap fetch and never see this
      // phase. Once we have jobId the per-batch cancel/dismiss controls
      // become meaningful, so refresh their visibility.
      if (!batch.jobId) batch.jobId = ev.jobId;
      updateBatchControls(batch);
      break;
    }
    case 'snapshot': {
      // First frame on EventSource subscriptions — re-apply server state
      // to every row. Idempotent: if bootstrapURLJobs already restored
      // the rows from GET /jobs, this just overwrites with the same
      // values. The job inside `ev` mirrors the JobSnapshot wire shape.
      applyJobSnapshotToBatch(batch, ev.job);
      // Race window: the job may have transitioned to terminal between
      // bootstrap's GET /jobs and our subscribe. The server's stream
      // closes cleanly in that case (Subscribe pre-closes the channel
      // for terminal jobs and never publishes summary), so without this
      // guard the browser's default EventSource auto-reconnect would
      // spin against a finished job every ~3s — wasting connections and
      // re-encoding the same snapshot indefinitely. Detect the terminal
      // status here and finalize locally just like the live summary
      // path would have.
      const status = ev.job && ev.job.status;
      if (status === 'completed' || status === 'failed' || status === 'cancelled') {
        if (batch.eventSource) {
          batch.eventSource.close();
          batch.eventSource = null;
        }
        if (!batch.done) {
          batch.done = true;
          updateBatchControls(batch);
          maybeFinalize();
          updateURLBadge();
        }
      }
      break;
    }
    case 'queued': {
      // Server accepted the POST but has not yet acquired the batch
      // semaphore — another batch is still running. Flip this batch's
      // rows to a distinct "waiting behind another batch" status so the
      // user isn't left staring at a stale generic "대기 중" forever.
      // The `start` event will overwrite this to "downloading" once the
      // semaphore clears.
      for (const row of batch.rowEls.values()) {
        setRowStatus(row, 'status-pending', '대기 중 (순서 대기)');
      }
      break;
    }
    case 'start': {
      const row = ensureURLRow(batch, ev.index, ev.url);
      row.querySelector('.url-row-name').textContent = ev.name || ev.url;
      const total = Number(ev.total) || 0;
      row.dataset.total = String(total);
      // HLS imports omit `total` (unknown length) — flip the row into
      // indeterminate mode so CSS runs the shuttle animation instead of
      // pinning the bar at 0%.
      row.classList.toggle('url-row-indeterminate', total === 0);
      const sizeText = total > 0 ? formatSize(total) : '크기 미상';
      const typePart = ev.type ? `${ev.type} · ` : '';
      setRowStatus(row, 'status-downloading', typePart + sizeText);
      break;
    }
    case 'progress': {
      const row = batch.rowEls.get(ev.index);
      if (!row) return;
      const total = Number(row.dataset.total) || 0;
      if (total > 0) {
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
      const row = ensureURLRow(batch, ev.index, ev.url);
      row.querySelector('.url-row-name').textContent = ev.name || ev.url;
      row.classList.remove('url-row-indeterminate');
      row.querySelector('.url-progress-fill').style.width = '100%';
      const warn = (ev.warnings && ev.warnings.length > 0) ? ` · ${ev.warnings.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(ev.size)})${warn}`);
      batch.succeeded++;
      updateURLBadge();
      break;
    }
    case 'error': {
      const row = ensureURLRow(batch, ev.index, ev.url);
      row.classList.remove('url-row-indeterminate');
      // Cancellation is a deliberate user action, not a failure — render
      // it with the dedicated status-cancelled class (muted) and bump
      // the cancelled counter so summary text and badge reflect intent.
      if (ev.error === 'cancelled') {
        setRowStatus(row, 'status-cancelled', '취소됨');
        batch.cancelled = (batch.cancelled || 0) + 1;
      } else {
        const label = URL_ERROR_LABELS[ev.error] || ev.error || '알 수 없는 오류';
        setRowStatus(row, 'status-error', '실패 · ' + label);
        batch.failed++;
      }
      updateURLBadge();
      break;
    }
    case 'summary': {
      // Per-batch summary is no longer rendered directly — with multiple
      // batches possibly in flight, consecutive summaries would overwrite
      // each other unpredictably. maybeFinalize() aggregates across every
      // batch in the round once the last one settles.
      //
      // For EventSource-subscribed batches (restored via bootstrap or a
      // second tab) the POST submitURLImport finally never runs, so we
      // close the stream and finalize here instead. POST-driven batches
      // have no eventSource and fall through — their finally handles it.
      if (batch.eventSource) {
        batch.eventSource.close();
        batch.eventSource = null;
        batch.done = true;
        updateBatchControls(batch);
        maybeFinalize();
        updateURLBadge();
      }
      break;
    }
  }
}

// applyURLStateToRow reflects a server URLState onto an existing DOM row.
// Used both by the bootstrap restore path and by the EventSource snapshot
// frame for late subscribers — the operation is idempotent.
export function applyURLStateToRow(row, u) {
  row.querySelector('.url-row-name').textContent = u.name || u.url;
  const total = Number(u.total) || 0;
  row.dataset.total = String(total);
  row.classList.toggle('url-row-indeterminate', total === 0 && u.status === 'running');
  const fill = row.querySelector('.url-progress-fill');
  switch (u.status) {
    case 'pending':
      setRowStatus(row, 'status-pending', '대기 중');
      fill.style.width = '0%';
      break;
    case 'running': {
      if (total > 0) {
        const received = Number(u.received) || 0;
        const pct = Math.min(100, (received / total) * 100);
        fill.style.width = pct.toFixed(1) + '%';
        setRowStatus(row, 'status-downloading',
          `${formatSize(received)} / ${formatSize(total)} · ${Math.floor(pct)}%`);
      } else {
        setRowStatus(row, 'status-downloading', formatSize(Number(u.received) || 0));
      }
      break;
    }
    case 'done': {
      fill.style.width = '100%';
      const warn = (u.warnings && u.warnings.length > 0) ? ` · ${u.warnings.join(', ')}` : '';
      setRowStatus(row, 'status-done', `완료 (${formatSize(Number(u.received) || 0)})${warn}`);
      break;
    }
    case 'cancelled':
      setRowStatus(row, 'status-cancelled', '취소됨');
      break;
    case 'error': {
      const label = URL_ERROR_LABELS[u.error] || u.error || '알 수 없는 오류';
      setRowStatus(row, 'status-error', '실패 · ' + label);
      break;
    }
  }
}

// applyJobSnapshotToBatch is the EventSource analogue: re-apply the server
// snapshot to every row in the batch AND recompute the batch's terminal
// counters. The recompute is essential — the snapshot is authoritative,
// and a job that finished between bootstrap's GET /jobs and our subscribe
// would otherwise leave succeeded/failed/cancelled at the bootstrap-time
// values, undercounting the badge and summary aggregations downstream.
export function applyJobSnapshotToBatch(batch, job) {
  if (!job || !Array.isArray(job.urls)) return;
  let succeeded = 0, failed = 0, cancelled = 0;
  job.urls.forEach((u, i) => {
    const row = ensureURLRow(batch, i, u.url);
    applyURLStateToRow(row, u);
    if (u.status === 'done')           succeeded++;
    else if (u.status === 'error')     failed++;
    else if (u.status === 'cancelled') cancelled++;
  });
  batch.succeeded = succeeded;
  batch.failed = failed;
  batch.cancelled = cancelled;
  // Invariant: URL count is fixed at job Create time. If a future server
  // change ever lets jobs grow URLs mid-flight, the row map would silently
  // hold orphans. Surface that here so the regression is visible in the
  // console rather than as a confusing UI glitch.
  if (batch.rowEls.size !== job.urls.length) {
    console.warn('row count mismatch: rows=%d snapshot.urls=%d (jobId=%s)',
      batch.rowEls.size, job.urls.length, batch.jobId);
  }
}

// removeBatchRows tears down every DOM contribution this batch made — the
// per-URL rows and the header. Used by HTTP-error rollback, dismiss, and
// the J5 removed phase handler.
export function removeBatchRows(batch) {
  for (const row of batch.rowEls.values()) row.remove();
  batch.rowEls.clear();
  if (batch.headerEl) {
    batch.headerEl.remove();
    batch.headerEl = null;
  }
}

// appendBatchHeader creates the per-batch label + control bar and inserts
// it ahead of the rows. Always called once per batch (POST or restored) so
// the user has a consistent place to issue batch-level cancel / dismiss.
export function appendBatchHeader(batch, label) {
  const header = document.createElement('div');
  header.className = 'url-batch-header';
  header.dataset.batch = String(batch.id);

  const labelEl = document.createElement('span');
  labelEl.className = 'url-batch-label';
  labelEl.textContent = label || '';
  header.appendChild(labelEl);

  const actions = document.createElement('span');
  actions.className = 'url-batch-actions';

  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.className = 'url-batch-cancel-all hidden';
  cancelBtn.textContent = '전체 취소';
  cancelBtn.addEventListener('click', () => _cancelBatchAll(batch));
  actions.appendChild(cancelBtn);

  const dismissBtn = document.createElement('button');
  dismissBtn.type = 'button';
  dismissBtn.className = 'url-batch-dismiss hidden';
  dismissBtn.textContent = '닫기';
  dismissBtn.addEventListener('click', () => _dismissBatch(batch));
  actions.appendChild(dismissBtn);

  header.appendChild(actions);
  $.urlRows.appendChild(header);
  batch.headerEl = header;
  updateBatchControls(batch);
}

// updateBatchControls flips the cancel/dismiss buttons based on whether
// the batch is still active. Called from header creation, summary, and
// HTTP-error finalize paths so the visible state never lags reality.
export function updateBatchControls(batch) {
  if (!batch.headerEl) return;
  const cancelBtn = batch.headerEl.querySelector('.url-batch-cancel-all');
  const dismissBtn = batch.headerEl.querySelector('.url-batch-dismiss');
  // Without a server-issued jobId we have nothing to call — hide both
  // controls. The POST flow lands jobId in `register`, so the gap is at
  // most a single round-trip.
  const hasJobId = !!batch.jobId;
  cancelBtn.classList.toggle('hidden', batch.done || !hasJobId);
  dismissBtn.classList.toggle('hidden', !batch.done || !hasJobId);
}

function showURLError(msg) {
  $.urlError.textContent = msg;
  $.urlError.classList.remove('hidden');
}

export function wireURLImport() {
  $.urlImportBtn.addEventListener('click', openURLModal);
  $.urlCancelBtn.addEventListener('click', closeURLModal);
  $.urlConfirmBtn.addEventListener('click', submitURLImport);
  $.urlClearFinishedBtn.addEventListener('click', () => _dismissAllFinishedBatches());
  $.urlBadge.addEventListener('click', openURLModal);
  wireModalDismiss($.urlModal, closeURLModal);
}
