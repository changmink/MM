// urlImportJobs.js — 백그라운드 잡 부트스트랩 + 구독 + cancel/dismiss API
//
// urlImport.js의 row/batch 헬퍼·상태를 단방향으로 import. 반대 방향(urlImport →
// jobs)은 setURLImportDeps()로 주입한다.

import { $ } from './dom.js';
import {
  urlBatches, nextBatchId,
  ensureURLRow, applyURLStateToRow, applyJobSnapshotToBatch,
  appendBatchHeader, removeBatchRows, updateBatchControls,
  updateURLBadge, updateConfirmButton,
  handleSSEEvent,
} from './urlImport.js';

// On every page load we ask the server which import jobs are alive, restore
// rows for them, and (for active jobs) attach an EventSource so live progress
// keeps flowing into the same UI the POST flow uses. A second tab opening
// the page sees the same jobs with no extra ceremony.
export async function bootstrapURLJobs() {
  let body;
  try {
    const res = await fetch('/api/import-url/jobs');
    if (!res.ok) return;
    body = await res.json();
  } catch (e) {
    // Network or parse failure: silently fall through. The user sees no
    // restored progress, but the rest of the app still works.
    console.warn('bootstrapURLJobs failed', e);
    return;
  }
  const active = Array.isArray(body.active) ? body.active : [];
  // Soft cap on restored history. Server keeps every dismissed-but-not-
  // cleared job until restart (single-user, unbounded growth tolerated by
  // the spec); the client renders only the most recent N so a long-running
  // browser session can't bloat the modal DOM. Active jobs are never
  // capped — they're still in flight and dropping any would lose UI.
  const HISTORY_CAP = 50;
  const finished = (Array.isArray(body.finished) ? body.finished : [])
    .slice(-HISTORY_CAP);
  if (active.length === 0 && finished.length === 0) return;

  // Render finished first so they sit at the top of the result area —
  // active progress rows naturally land below as the user-current focus.
  for (const job of finished) restoreJobBatch(job, false);
  for (const job of active) restoreJobBatch(job, true);

  if (urlBatches.length > 0) {
    $.urlResult.classList.remove('hidden');
  }
  updateConfirmButton();
  updateURLBadge();
}

// restoreJobBatch builds a batch from a server JobSnapshot and renders one
// row per URL with the correct status/progress already applied. Restored
// batches carry restored=true so they don't fold into the current session's
// summary aggregation (see maybeFinalize) — they are backdrop history that
// the user can dismiss when they want.
function restoreJobBatch(jobSnap, isActive) {
  const batch = {
    id: nextBatchId(),
    jobId: jobSnap.id,
    rowEls: new Map(),
    headerEl: null,
    succeeded: 0,
    failed: 0,
    cancelled: 0,
    total: jobSnap.urls.length,
    done: !isActive,
    restored: true,
    eventSource: null,
  };
  urlBatches.push(batch);

  // Synthetic tag distinguishes restored rows from freshly submitted ones.
  appendBatchHeader(batch, isActive ? '복원된 배치' : '이전 결과');

  jobSnap.urls.forEach((u, i) => {
    const row = ensureURLRow(batch, i, u.url);
    applyURLStateToRow(row, u);
    if (u.status === 'done')           batch.succeeded++;
    else if (u.status === 'error')     batch.failed++;
    else if (u.status === 'cancelled') batch.cancelled++;
  });

  if (isActive) subscribeToJob(batch);
}

// cancelURLAt fires a per-URL cancel against the server. The visible row
// state updates via the SSE error("cancelled") frame the worker emits in
// response — keeping a single source of truth for state transitions.
export async function cancelURLAt(batch, index) {
  if (!batch.jobId) return;
  try {
    await fetch(
      '/api/import-url/jobs/' + encodeURIComponent(batch.jobId) +
      '/cancel?index=' + encodeURIComponent(String(index)),
      { method: 'POST' });
  } catch (e) {
    console.warn('cancel url failed', e);
  }
}

export async function cancelBatchAll(batch) {
  if (!batch.jobId) return;
  try {
    await fetch(
      '/api/import-url/jobs/' + encodeURIComponent(batch.jobId) + '/cancel',
      { method: 'POST' });
  } catch (e) {
    console.warn('cancel batch failed', e);
  }
}

export async function dismissBatch(batch) {
  if (!batch.jobId || !batch.done) return;
  try {
    const res = await fetch(
      '/api/import-url/jobs/' + encodeURIComponent(batch.jobId),
      { method: 'DELETE' });
    if (!res.ok) return;
  } catch (e) {
    console.warn('dismiss failed', e);
    return;
  }
  removeBatchRows(batch);
  if (batch.eventSource) {
    batch.eventSource.close();
    batch.eventSource = null;
  }
  const idx = urlBatches.indexOf(batch);
  if (idx !== -1) urlBatches.splice(idx, 1);
  updateURLBadge();
}

export async function dismissAllFinishedBatches() {
  try {
    const res = await fetch('/api/import-url/jobs?status=finished',
      { method: 'DELETE' });
    if (!res.ok) return;
  } catch (e) {
    console.warn('dismiss-all failed', e);
    return;
  }
  // Server tore down every terminal job — mirror that locally. Active
  // batches stay (they were already excluded by the filter).
  const remaining = [];
  for (const batch of urlBatches) {
    if (batch.done) {
      removeBatchRows(batch);
      if (batch.eventSource) {
        batch.eventSource.close();
        batch.eventSource = null;
      }
      continue;
    }
    remaining.push(batch);
  }
  urlBatches.length = 0;
  urlBatches.push(...remaining);
  updateURLBadge();
}

// subscribeToJob opens an EventSource against /api/import-url/jobs/{id}/events
// and routes every frame through the existing handleSSEEvent path. The
// EventSource is closed explicitly on summary so the auto-reconnect default
// does not waste a round-trip on a finished job.
function subscribeToJob(batch) {
  if (!batch.jobId) return;
  const es = new EventSource('/api/import-url/jobs/' + encodeURIComponent(batch.jobId) + '/events');
  batch.eventSource = es;
  es.onmessage = e => {
    let ev;
    try { ev = JSON.parse(e.data); }
    catch (err) { console.warn('bad sse frame', e.data, err); return; }
    handleSSEEvent(batch, ev);
  };
  es.onerror = () => {
    // EventSource auto-reconnects on transient failures. We close
    // explicitly only after summary was processed (in handleSSEEvent),
    // so an onerror here means a real network drop. Let the browser
    // retry; if the server is back we resync via the snapshot frame.
  };
}
