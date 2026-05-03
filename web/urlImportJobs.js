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

// 페이지가 로드될 때마다 서버에 어떤 import Job이 살아 있는지 묻고, 그에
// 해당하는 행을 복원한다. active Job에는 EventSource를 붙여 라이브 진행이
// POST 흐름과 동일한 UI로 계속 흘러들게 한다. 두 번째 탭이 페이지를 열면
// 별도 작업 없이 같은 Job들을 본다.
export async function bootstrapURLJobs() {
  let body;
  try {
    const res = await fetch('/api/import-url/jobs');
    if (!res.ok) return;
    body = await res.json();
  } catch (e) {
    // 네트워크 또는 파싱 실패: 조용히 흘려보낸다. 사용자는 복원된 진행을
    // 보지 못하지만 앱의 나머지는 정상 동작한다.
    console.warn('bootstrapURLJobs failed', e);
    return;
  }
  const active = Array.isArray(body.active) ? body.active : [];
  // 복원된 history의 soft cap. 서버는 dismiss는 됐지만 정리되지 않은 모든
  // Job을 재시작 전까지 보관한다(단일 사용자, spec이 무한 증가를 허용).
  // 클라이언트는 가장 최근 N개만 렌더링해, 오래 켜둔 브라우저 세션이 모달
  // DOM을 부풀리지 않게 한다. active Job은 cap을 두지 않는다 — 진행 중이라
  // 하나라도 떨어뜨리면 UI를 잃는다.
  const HISTORY_CAP = 50;
  const finished = (Array.isArray(body.finished) ? body.finished : [])
    .slice(-HISTORY_CAP);
  if (active.length === 0 && finished.length === 0) return;

  // finished를 먼저 렌더해 결과 영역의 위쪽에 두고, active 진행 행이 자연스럽게
  // 아래에 배치되어 사용자 현재 포커스가 된다.
  for (const job of finished) restoreJobBatch(job, false);
  for (const job of active) restoreJobBatch(job, true);

  if (urlBatches.length > 0) {
    $.urlResult.classList.remove('hidden');
  }
  updateConfirmButton();
  updateURLBadge();
}

// restoreJobBatch는 서버 JobSnapshot으로 batch를 만들고 URL마다 행 하나를
// — 올바른 상태/진행이 이미 반영된 채로 — 렌더링한다. 복원된 batch는
// restored=true를 달아, 현재 세션의 summary 집계(maybeFinalize 참조)에
// 합쳐지지 않게 한다 — 이들은 사용자가 원할 때 dismiss할 수 있는 배경
// history다.
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

  // 합성 태그가 복원된 행을 새로 submit된 행과 구분짓는다.
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

// cancelURLAt은 서버에 URL 단위 cancel을 발사한다. 가시 행 상태는 워커가
// 응답으로 발행하는 SSE error("cancelled") 프레임을 통해 갱신된다 — 상태
// 전이의 단일 진실 원천을 유지한다.
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
  // 서버가 모든 terminal Job을 정리했으니 로컬에서도 그대로 미러링한다.
  // active batch는 유지된다(서버 필터가 이미 제외했다).
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

// subscribeToJob은 /api/import-url/jobs/{id}/events에 EventSource를 열고
// 모든 프레임을 기존 handleSSEEvent 경로로 보낸다. summary 시 EventSource를
// 명시적으로 close해, 기본 auto-reconnect가 끝난 Job을 상대로 round-trip을
// 낭비하지 않도록 한다.
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
    // EventSource는 일시적 실패에 자동 재연결한다. summary가 처리된
    // 뒤(handleSSEEvent에서)에만 명시적으로 close하므로, 여기 onerror는
    // 진짜 네트워크 단절을 의미한다. 브라우저가 재시도하도록 두고, 서버가
    // 돌아오면 snapshot 프레임으로 재동기화한다.
  };
}
