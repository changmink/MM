// download.js — 폴더 / 선택 다운로드 ZIP. 툴바 "폴더 다운로드" 버튼은 항상
// 노출되어 currentPath 전체를 GET ZIP으로 받고, 선택 다운로드 버튼은
// 선택 ≥ 1일 때만 노출된다. N=1이면 ZIP을 우회하고 /api/stream으로 직접
// 단일 파일을 다운로드한다 (SPEC §2.10).

import { $ } from './dom.js';
import { currentPath, selectedPaths } from './state.js';

// triggerHrefDownload는 동일 출처 URL을 anchor download 트리거로 변환한다.
// download 속성은 hint이며 서버가 Content-Disposition을 보내면 그 파일명이
// 우선한다 — anchor.download는 헤더가 없는 케이스의 fallback.
function triggerHrefDownload(href, suggestedName) {
  const a = document.createElement('a');
  a.href = href;
  if (suggestedName) a.download = suggestedName;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// triggerBlobDownload는 fetch 결과 Blob을 ObjectURL로 받아 다운로드시킨다.
// blob URL은 즉시 revoke 하지 않고 click 직후 micro-tick에서 풀어야 일부
// 브라우저(Firefox)에서 다운로드가 끊기지 않는다.
function triggerBlobDownload(blob, suggestedName) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  if (suggestedName) a.download = suggestedName;
  document.body.appendChild(a);
  a.click();
  setTimeout(() => {
    URL.revokeObjectURL(url);
    a.remove();
  }, 0);
}

function downloadFolderHref(path) {
  return `/api/download-folder?path=${encodeURIComponent(path || '/')}`;
}

async function downloadSelection() {
  const paths = Array.from(selectedPaths);
  if (paths.length === 0) return;

  // N=1: ZIP을 거치지 않고 stream 엔드포인트로 직접 다운로드 — 사용자가
  // ZIP을 풀어야 하는 마찰을 줄인다.
  if (paths.length === 1) {
    const p = paths[0];
    const name = p.split('/').pop() || 'file';
    triggerHrefDownload(`/api/stream?path=${encodeURIComponent(p)}`, name);
    return;
  }

  // N≥2: POST 후 blob — 단일 사용자 LAN 가정에서 메모리 적재 트레이드오프
  // 수용. 큰 다중 선택은 폴더 다운로드(GET 스트리밍)를 권하는 식으로
  // 자연 분할.
  const btn = $.downloadSelectionBtn;
  const originalText = btn.textContent;
  btn.disabled = true;
  btn.textContent = '준비 중...';
  try {
    const res = await fetch(downloadFolderHref(currentPath), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: paths }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      alert(`다운로드 실패 (${res.status}): ${text}`);
      return;
    }
    const blob = await res.blob();
    const fallback = (currentPath.split('/').filter(Boolean).pop() || 'files')
      + `-selected-${paths.length}.zip`;
    triggerBlobDownload(blob, fallback);
  } catch (err) {
    alert(`다운로드 실패: ${err.message || err}`);
  } finally {
    btn.disabled = false;
    btn.textContent = originalText;
  }
}

// updateDownloadSelectionBtn은 selection 종속 가시성을 갱신한다.
// 폴더는 selectedPaths에 들어가지 않는다(bindEntrySelection이 폴더 체크박스
// 자체를 숨김) — 따라서 selectedPaths.size 만으로 안전하게 카운트.
export function updateDownloadSelectionBtn() {
  const n = selectedPaths.size;
  if (n === 0) {
    $.downloadSelectionBtn.hidden = true;
    return;
  }
  $.downloadSelectionBtn.hidden = false;
  $.downloadSelectionBtn.textContent = `선택 다운로드 (${n}개)`;
}

export function wireDownload() {
  $.downloadFolderBtn.addEventListener('click', () => {
    triggerHrefDownload(downloadFolderHref(currentPath));
  });
  $.downloadSelectionBtn.addEventListener('click', () => {
    downloadSelection();
  });
}
