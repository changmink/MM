// urlImport.js — URL 가져오기 모달 + POST flow + SSE handler + 배치 상태
//
// urlImportJobs.js 에서 cancel/dismiss 함수와 browse 를 setURLImportDeps()로 주입한다
// (urlImport → jobs 의존을 끊어 단방향 import 만 유지).

import { $ } from './dom.js';
import { currentPath } from './state.js';
import { esc, formatSize, consumeSSE } from './util.js';
import { wireModalDismiss } from './modalDismiss.js';

// 라벨은 일부러 구체적인 바이트/시간 한도를 적지 않는다 — 이 값들은
// /api/settings를 통해 런타임에 설정 가능하므로, 여기 숫자를 하드코딩하면
// 사용자가 한도나 타임아웃을 올렸을 때 UI가 거짓을 말하게 된다.
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
  // `cancelled`는 실패가 아니라 별도의 행 상태로 처리된다 —
  // handleSSEEvent의 error 분기와 applyURLStateToRow의 "cancelled" 케이스
  // 참조. dictionary 엔트리는 필요 없다.
};

// 각 batch는 진행 중인 POST /api/import-url 한 개를 캡처한다. 여러 batch가
// 공존할 수 있다 — 하나가 진행 중일 때 모달을 다시 열면 confirm 버튼이
// "새 배치 추가"로 바뀌고, 추가 submit은 기존 행 아래로 새 batch를 덧붙인다.
// 서버는 `importSem`으로 직렬화하며, "대기" 행 상태로 표면화하는 `queued`
// SSE 이벤트를 발행한다.
//
// 의도적으로 batch별 AbortController를 두지 않는다 — 모달을 닫더라도
// fetch가 계속 돌아야, 사용자가 다시 열었을 때 진행 상황을 볼 수 있다.
// 브라우저가 시작한 fetch abort(탭 닫기 / 네비게이션)는 서버에서
// `r.Context()` 취소로 흘러간다.
export const urlBatches = [];
let urlBatchSeq = 0;
export function nextBatchId() { return ++urlBatchSeq; }

// 진행 중 submit의 짧은 POST 셋업 창 동안만 true다 — 같은 URL 리스트의
// 우발적 double-submit을 막는다. SSE 소비가 시작되면 false로 뒤집어, 사용자가
// 이 batch가 끝날 때까지 기다리지 않고 다음 batch를 대기열에 넣을 수 있게 한다.
let urlSubmittingNow = false;
// 라운드가 실패만으로 끝났을 때, badge가 ⚠ 마커를 잠시 더 표시해 화면을
// 벗어나 있던 사용자도 놓치지 않도록 batch 메타데이터를 잠깐 유지한다.
// 다음 열기나 다음 완료 라운드 시 정리된다.
let urlBadgeLinger = false;

// 늦게 바인딩되는 의존성 (사용자 상호작용 전에 setURLImportDeps로 설정됨).
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

  // urlBatches에 보여줄 게 있으면 모달 내용을 그대로 둔다. 다음을 포함:
  //   - active batch (라이브 진행)
  //   - bootstrapURLJobs로 복원된 finished batch ("닫기" / "완료 항목
  //     모두 지우기"로 사용자가 dismiss 가능한 history 행)
  //   - 같은 세션의 error 라운드에서 아직 머물고 있는 finished batch
  // 레지스트리가 완전히 비어 있을 때만 "fresh start"로 본다 — 이때는
  // maybeFinalize가 이미 성공 라운드 후 모두 정리했거나 error-only
  // 라운드의 3초 linger 후 모두 정리한 상태다.
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

// confirm 버튼을 현재 레지스트리 상태와 동기화 유지해, 라벨이 항상 "지금
// 클릭하면 무슨 일이 일어나는가?"에 답하도록 한다. 모든 상태 전이에서 이
// 함수를 다시 호출한다 — 열기, submit 진입, SSE 시작, batch 종결.
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
  // 순수 뷰 숨김 — fetch는 계속 돌고, badge가 진행 표면 역할을 이어받는다.
  // 탭 닫기·네비게이션은 여전히 브라우저가 abort 시키며, 서버는 이를
  // r.Context() 취소로 처리한다.
  $.urlModal.classList.add('hidden');
  updateURLBadge();
}

export function updateURLBadge() {
  // "완료 항목 모두 지우기" 버튼은 모달 안에 있다 — 가시성을 레지스트리와
  // 동기화해, dismiss 가능한 finished batch가 생기는 즉시 사용자에게 보이도록 한다.
  const hasFinished = urlBatches.some(b => b.done);
  $.urlClearFinishedBtn.classList.toggle('hidden', !hasFinished);

  const modalHidden = $.urlModal.classList.contains('hidden');
  const shouldShow = modalHidden && (anyBatchActive() || urlBadgeLinger);
  if (!shouldShow) {
    $.urlBadge.classList.add('hidden');
    $.urlBadge.classList.remove('has-error');
    return;
  }
  // badge는 active batch(복원된 것이든 새로 POST된 것이든)만 집계한다 — 그래야
  // 실행 중인 progress bar가 진행 중인 것만 반영한다. 복원된 finished batch는
  // 배경 컨텍스트지 active 진행이 아니다.
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

// maybeFinalize는 batch가 종결될 때마다 실행된다(정상 완료, abort, 네트워크
// 에러, HTTP 에러). 요약 범위를 "이 세션의 라운드"로 제한한다 — 이 세션에서
// submit한 batch이면서 서버 history 스냅샷에서 로드되지 않은 것들. 복원된
// batch는 배경 UI다 — 라운드 요약에 합치면 이전 세션의 결과가 현재 작업의
// 합계에 섞여 들어간다.
function maybeFinalize() {
  // 라운드 = 이 세션에서 submit한 batch(복원된 것은 history).
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

  // 서버 status 우선순위 미러(SPEC §2.6): succeeded≥1 → 일부가 실패/취소돼도
  // completed; cancelled만 → cancelled; 그 외 → error.
  let cls;
  if (succeeded > 0 && failed === 0 && cancelled === 0)      cls = 'status-done';
  else if (succeeded > 0)                                    cls = 'status-mixed';
  else if (failed === 0 && cancelled > 0)                    cls = 'status-cancelled';
  else                                                       cls = 'status-error';

  // round.length>0 + 모든 batch.done 전제 조건이 세 카운터 중 적어도 하나는
  // non-zero임을 보장하므로 parts.join이 비어 있을 수 없다.
  const parts = [];
  if (succeeded > 0) parts.push(`성공 ${succeeded}`);
  if (failed > 0)    parts.push(`실패 ${failed}`);
  if (cancelled > 0) parts.push(`취소 ${cancelled}`);
  $.urlSummary.className = 'url-summary ' + cls;
  $.urlSummary.textContent = parts.join(' · ');

  if (succeeded > 0) {
    // 성공: 이번 라운드만 정리한다(복원된 history는 유지). browse 새로고침이
    // 새 파일을 노출하며, 모달은 사용자가 원할 때 dismiss할 수 있도록
    // history 행을 유지한다.
    clearRoundBatches(round);
    urlBadgeLinger = false;
    updateURLBadge();
    _browse(currentPath, false);
    return;
  }

  // 성공 없음(전부 실패 또는 전부 취소): 사용자가 클릭해 들여다볼 수 있도록
  // badge를 잠시 유지한 뒤 이번 라운드를 정리한다(클로저에 `round`를 캡처해
  // 그 사이에 시작된 새 라운드에는 영향이 없다).
  urlBadgeLinger = true;
  updateURLBadge();
  setTimeout(() => {
    clearRoundBatches(round);
    urlBadgeLinger = false;
    updateURLBadge();
  }, 3000);
}

// clearRoundBatches는 각 batch의 DOM을 분해하고 urlBatches에서 제거하되,
// 레지스트리의 나머지는 그대로 둔다. maybeFinalize가 사용해, 완료된 라운드는
// active 상태에서 빠져나가되 복원된 history는 사용자가 dismiss할 때까지
// 가시 상태로 유지된다.
function clearRoundBatches(batches) {
  for (const b of batches) {
    removeBatchRows(b);
    const idx = urlBatches.indexOf(b);
    if (idx !== -1) urlBatches.splice(idx, 1);
  }
}

async function submitURLImport() {
  // POST가 진행 중일 때만 거부한다 — SSE가 흐르기 시작하면 사용자는 그 위에
  // 또 다른 batch를 자유롭게 대기열에 넣을 수 있다.
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
  // 레지스트리가 진짜 비어 있을 때만 DOM을 지운다. 복원된 history나(아직
  // 정리 중인 이전 라운드)는 가시 상태로 유지된다 — urlBatches에 남아 있는
  // 동안 여기서 지우면 DOM이 내부 카운터와 어긋난다.
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

  // 위치 라벨은 현재 라운드의 batch만 계수한다 — 복원된 history는 자신만의
  // 라벨("복원된 배치"/"이전 결과")이 있으므로 "배치 N" 번호를 올려서는 안
  // 된다. 새 라운드의 첫 batch는 라벨이 없다.
  const roundCount = urlBatches.filter(b => !b.restored).length;
  appendBatchHeader(batch, roundCount > 1 ? `배치 ${roundCount}` : '');

  // URL마다 pending 행을 미리 만들어, 첫 SSE 이벤트 도착 전에도 사용자가
  // 즉시 피드백을 받게 한다.
  urls.forEach((u, i) => ensureURLRow(batch, i, u));
  // textarea를 비워, 사용자가 select-all-and-delete 없이도 곧장 다음 batch를
  // 타이핑할 수 있게 한다.
  $.urlInput.value = '';

  urlSubmittingNow = true;
  updateConfirmButton();

  // SSE 스트림이 실제로 열렸는지 추적한다. HTTP-error 분기는 이 값을 뒤집기
  // 전에 batch를 pop하고 반환하므로, 아래 orphan-finalize는 실제로 열렸다가
  // 끊긴 스트림에 대해서만 실행된다.
  let sseOpened = false;

  try {
    const res = await fetch('/api/import-url?path=' + encodeURIComponent(currentPath), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({ urls }),
    });
    if (!res.ok) {
      let msg = '';
      try { msg = (await res.json()).error || ''; } catch { /* JSON이 아님 */ }
      if (!msg) msg = `요청 실패 (${res.status})`;
      showURLError(msg);
      // 어떤 경우든 이 batch의 DOM을 분해한다 — 그러지 않으면 미리 만든
      // 행이 멈춘 "대기 중" placeholder로 남는다. 레지스트리에 (active든
      // restored든) 아무것도 없으면 result 패널 전체를 숨기고, 형제가 있으면
      // 가시 상태를 유지한다.
      removeBatchRows(batch);
      const idx = urlBatches.indexOf(batch);
      if (idx !== -1) urlBatches.splice(idx, 1);
      if (urlBatches.length === 0) {
        $.urlResult.classList.add('hidden');
      }
      return;
    }
    // SSE 연결됨. 사용자가 이 batch가 끝날 때까지 기다리지 않고 다음 batch를
    // 대기열에 넣을 수 있도록 버튼을 풀어준다.
    sseOpened = true;
    urlSubmittingNow = false;
    updateConfirmButton();
    await consumeSSE(res, ev => handleSSEEvent(batch, ev));
  } catch (e) {
    // 우리가 더 이상 AbortController를 두지 않더라도 브라우저가 시작한
    // 취소(탭 네비게이션)에서 AbortError가 도착할 수 있다 — 문서의
    // 라이프사이클 시그널이 pending fetch를 abort 한다. 페이지가 어차피
    // 사라지므로 조용히 삼킨다.
    if (e.name !== 'AbortError') {
      showURLError('요청 실패: ' + e.message);
    }
  } finally {
    // 서버 계약은 URL마다 start → done|error에 최종 summary다. 그러므로
    // pending/downloading 상태로 남아 있는 URL 행은 스트림이 끊긴 것을 의미한다
    // (네트워크 단절, proxy 타임아웃, 서버 crash). 명시적 실패로 변환해야
    // — 그렇지 않으면 badge·집계 summary가 undercount 되고 라운드가 거짓으로
    // "0 failed"를 보고할 수 있다. sseOpened일 때만 실행 — HTTP 에러에서는
    // 위에서 이미 batch를 레지스트리에서 pop했다.
    if (sseOpened) finalizeOrphanRows(batch);
    batch.done = true;
    urlSubmittingNow = false;
    updateBatchControls(batch);
    maybeFinalize();
    updateConfirmButton();
    updateURLBadge();
  }
}

// finalizeOrphanRows는 batch에서 종결되지 않은 행(아직 pending이거나
// downloading)을 "연결 끊김" 실패로 승격시킨다. SSE 스트림이 열렸지만 모든
// URL에 대한 종료 이벤트 없이 끝난 경우, submitURLImport의 finally에서
// 호출된다.
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
  // CSS가 terminal 상태에서 이 버튼을 숨긴다. 그 이전의 클릭은 서버에
  // URL 단위 cancel을 발사한다. SSE가 가시 상태 전이를 구동하도록 두어,
  // 네트워크 실패 시 retry용 버튼이 깔끔하게 남게 한다.
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
      // POST 응답의 첫 프레임 — 서버가 jobId를 넘겨주므로 새로 고침 시
      // GET /jobs/{id}/events로 재바인딩할 수 있다. 복원된 batch는 bootstrap
      // fetch에서 이미 jobId를 갖고 있어 이 phase를 보지 않는다. jobId가
      // 생기면 batch 단위 cancel/dismiss 컨트롤이 의미를 가지므로 가시성을 갱신한다.
      if (!batch.jobId) batch.jobId = ev.jobId;
      updateBatchControls(batch);
      break;
    }
    case 'snapshot': {
      // EventSource 구독의 첫 프레임 — 서버 상태를 모든 행에 다시 적용한다.
      // 멱등적이다: bootstrapURLJobs가 GET /jobs에서 이미 행을 복원했다면
      // 같은 값으로 덮어쓸 뿐이다. `ev` 안의 job은 JobSnapshot wire 형태를
      // 그대로 따른다.
      applyJobSnapshotToBatch(batch, ev.job);
      // race 창: bootstrap의 GET /jobs와 우리 subscribe 사이에 Job이
      // terminal로 전이됐을 수 있다. 그 경우 서버 스트림은 깔끔하게 닫힌다
      // (Subscribe는 terminal Job의 채널을 미리 close하고 summary를
      // 발행하지 않는다). 이 가드가 없으면 브라우저의 기본 EventSource
      // 자동 재연결이 끝난 Job을 상대로 ~3초마다 도는 — 연결을 낭비하고
      // 같은 스냅샷을 무한히 재인코딩한다. 여기서 terminal 상태를 감지해
      // 라이브 summary 경로처럼 로컬에서 finalize 한다.
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
      // 서버가 POST를 수락했지만 아직 batch 세마포어를 획득하지 못했다 —
      // 다른 batch가 진행 중이다. 사용자가 일반 "대기 중"을 영원히 보게
      // 두지 않도록 이 batch의 행을 별도의 "다른 batch 뒤에서 대기" 상태로
      // 뒤집는다. 세마포어가 풀리면 `start` 이벤트가 이를 "downloading"으로
      // 덮어쓴다.
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
      // HLS import는 `total`을 보내지 않는다(길이 미상) — CSS가 0%에 바를
      // 고정하지 않고 shuttle 애니메이션을 돌리도록 행을 indeterminate
      // 모드로 뒤집는다.
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
      // 취소는 사용자가 의도한 행동이지 실패가 아니다 — 별도의
      // status-cancelled 클래스(muted)로 렌더하고 cancelled 카운터를 올려,
      // summary 텍스트와 badge가 의도를 반영하도록 한다.
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
      // batch별 summary는 더 이상 직접 렌더하지 않는다 — 여러 batch가
      // 동시에 진행 중일 수 있어 연속된 summary가 예측 불가하게 서로를
      // 덮어쓰게 된다. 마지막 batch가 종결되면 maybeFinalize()가 라운드의
      // 모든 batch를 집계한다.
      //
      // EventSource로 구독된 batch(bootstrap이나 두 번째 탭으로 복원된
      // 경우)에는 POST submitURLImport의 finally가 실행되지 않는다 —
      // 그래서 여기서 스트림을 닫고 직접 finalize 한다. POST 구동 batch는
      // eventSource가 없어 그대로 흘러간다 — 자기 finally가 처리한다.
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

// applyURLStateToRow는 서버 URLState를 기존 DOM 행에 반영한다. bootstrap
// 복원 경로와 늦은 구독자를 위한 EventSource 스냅샷 프레임 둘 다에서
// 사용하며, 연산은 멱등적이다.
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

// applyJobSnapshotToBatch는 EventSource 쪽 짝이다 — batch의 모든 행에
// 서버 스냅샷을 다시 적용하고 batch의 종결 카운터를 재계산한다. 재계산이
// 핵심이다 — 스냅샷이 권위 있는 진실이며, bootstrap의 GET /jobs와 우리
// subscribe 사이에 끝난 Job은 succeeded/failed/cancelled를 bootstrap
// 시점 값으로 둘 텐데, 이는 downstream의 badge와 summary 집계를
// undercount 하게 만든다.
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
  // 불변식: URL 개수는 Job Create 시점에 고정된다. 향후 서버 변경이 진행
  // 중 URL 추가를 허용하게 되면 행 맵에 미아가 조용히 남는다. 혼란스러운
  // UI 결함이 아니라 콘솔에서 회귀가 보이도록 여기서 표면화한다.
  if (batch.rowEls.size !== job.urls.length) {
    console.warn('row count mismatch: rows=%d snapshot.urls=%d (jobId=%s)',
      batch.rowEls.size, job.urls.length, batch.jobId);
  }
}

// removeBatchRows는 이 batch가 기여한 모든 DOM(URL 단위 행과 header)을
// 분해한다. HTTP-error 롤백, dismiss, J5 removed phase 핸들러가 사용한다.
export function removeBatchRows(batch) {
  for (const row of batch.rowEls.values()) row.remove();
  batch.rowEls.clear();
  if (batch.headerEl) {
    batch.headerEl.remove();
    batch.headerEl = null;
  }
}

// appendBatchHeader는 batch별 라벨 + 컨트롤 바를 만들어 행 앞에 끼워 넣는다.
// batch당 한 번씩 항상 호출되므로(POST든 복원이든) 사용자가 batch 단위
// cancel / dismiss를 발사할 일관된 위치를 갖게 된다.
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

// updateBatchControls는 batch가 여전히 active인지에 따라 cancel/dismiss
// 버튼을 뒤집는다. header 생성, summary, HTTP-error finalize 경로에서
// 호출되어 가시 상태가 실제 상태에 뒤처지지 않게 한다.
export function updateBatchControls(batch) {
  if (!batch.headerEl) return;
  const cancelBtn = batch.headerEl.querySelector('.url-batch-cancel-all');
  const dismissBtn = batch.headerEl.querySelector('.url-batch-dismiss');
  // 서버가 발급한 jobId가 없으면 호출할 대상이 없다 — 두 컨트롤을 모두
  // 숨긴다. POST 흐름은 `register`로 jobId를 전달하므로 그 격차는 길어야
  // 한 번의 round-trip 정도다.
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
