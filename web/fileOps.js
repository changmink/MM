// fileOps.js — 파일 mutation: upload + folder create + delete + DnD move + rename
//
// browse / loadTree 의존은 wireFileOps(deps)에서 주입한다.

import { $ } from './dom.js';
import {
  currentPath, selectedPaths,
  dragSrcPath, setDragSrcPath,
  dragSrcPaths, setDragSrcPaths,
  DND_MIME,
} from './state.js';
import {
  esc, splitExtension, parentDir, rewritePathAfterFolderRename,
  validateRenameInput,
} from './util.js';

// 진행 중 drag가 단일 폴더인지 추적한다. 폴더는 별도의 PATCH 엔드포인트와
// 자기/후손 거부 규칙을 사용하므로, drop 핸들러가 dataTransfer 페이로드를
// 읽기 전에도 이 정보가 필요하다.
let dragSrcIsDir = false;

let _browse = null;
let _loadTree = null;

// ── 업로드 ───────────────────────────────────────────────────────────────────
// 내부 카드 drag는 커스텀 MIME을 갖지만 Files는 없다. 외부 OS drag는 Files를
// 갖는다. Files로 게이트해, 내부 파일을 업로드 zone 위로 끌었을 때 zone이
// 강조되지 않게 한다.
function isExternalFileDrag(e) {
  return Array.from(e.dataTransfer.types).includes('Files');
}

function uploadFiles(files) {
  Array.from(files).forEach(file => uploadOne(file));
}

// annotateUploadResult는 업로드 응답을 검사해 PNG → JPG 자동 변환 결과
// (SPEC §2.8.1)에 대한 인라인 메모를 덧붙인다. 진행 행이 제거되기 전 머무는
// 시간을 반환한다 — 사용자가 읽을 내용이 있으면 더 길게.
function annotateUploadResult(container, responseText) {
  let resp;
  try { resp = JSON.parse(responseText); } catch { return 1500; }
  const warnings = Array.isArray(resp.warnings) ? resp.warnings : [];
  const notes = [];
  if (resp.converted) notes.push({ text: `PNG → JPG로 변환됨 (${resp.name})`, kind: 'ok' });
  if (warnings.includes('convert_failed')) notes.push({ text: 'PNG 변환 실패, 원본으로 저장됨', kind: 'warn' });
  if (warnings.includes('renamed')) notes.push({ text: `파일명 자동 변경: ${resp.name}`, kind: 'warn' });
  if (notes.length === 0) return 1500;
  for (const n of notes) {
    const note = document.createElement('div');
    note.className = 'progress-note progress-note-' + n.kind;
    note.textContent = n.text;
    container.appendChild(note);
  }
  return 4000;
}

function uploadOne(file) {
  const container = document.createElement('div');
  container.className = 'progress-item';
  container.innerHTML = `
    <span>${esc(file.name)}</span>
    <div class="bar"><div class="bar-fill" style="width:0%"></div></div>
  `;
  $.uploadProgress.appendChild(container);
  const fill = container.querySelector('.bar-fill');

  const xhr = new XMLHttpRequest();
  xhr.upload.addEventListener('progress', e => {
    if (e.lengthComputable) {
      fill.style.width = Math.round((e.loaded / e.total) * 100) + '%';
    }
  });
  xhr.addEventListener('load', () => {
    if (xhr.status === 201) {
      fill.style.width = '100%';
      const linger = annotateUploadResult(container, xhr.responseText);
      setTimeout(() => container.remove(), linger);
      _browse(currentPath, false);
    } else {
      container.style.color = 'var(--danger)';
    }
  });
  xhr.addEventListener('error', () => {
    container.style.color = 'var(--danger)';
  });

  const form = new FormData();
  form.append('file', file);
  xhr.open('POST', '/api/upload?path=' + encodeURIComponent(currentPath));
  xhr.send(form);
}

// ── 폴더 생성 ────────────────────────────────────────────────────────────────
function openFolderModal() {
  $.folderNameInput.value = '';
  $.folderError.textContent = '';
  $.folderError.classList.add('hidden');
  $.folderModal.classList.remove('hidden');
  $.folderNameInput.focus();
}

function closeFolderModal() {
  $.folderModal.classList.add('hidden');
}

let folderSubmitting = false;

async function submitCreateFolder() {
  if (folderSubmitting) return;
  const name = $.folderNameInput.value.trim();
  if (!name) {
    showFolderError('폴더 이름을 입력하세요.');
    return;
  }
  const nameErr = validateRenameInput(name);
  if (nameErr) {
    showFolderError(nameErr);
    return;
  }
  folderSubmitting = true;
  $.folderConfirmBtn.disabled = true;
  try {
    const res = await fetch('/api/folder?path=' + encodeURIComponent(currentPath), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    if (res.status === 201) {
      closeFolderModal();
      _browse(currentPath, false);
      _loadTree();
    } else if (res.status === 409) {
      showFolderError('이미 존재하는 폴더입니다.');
    } else {
      showFolderError('유효하지 않은 이름입니다.');
    }
  } finally {
    folderSubmitting = false;
    $.folderConfirmBtn.disabled = false;
  }
}

function showFolderError(msg) {
  $.folderError.textContent = msg;
  $.folderError.classList.remove('hidden');
}

// ── 삭제 ─────────────────────────────────────────────────────────────────────
// opts.skipBrowse=true면 호출자(라이트박스)가 자체 mutation 후 _browse를
// 직접 호출하므로 중복 fetch를 피한다. return: 성공 true / 취소·실패 false.
export async function deleteFile(path, opts = {}) {
  if (!confirm(`삭제하시겠습니까?\n${path}`)) return false;
  const res = await fetch('/api/file?path=' + encodeURIComponent(path), { method: 'DELETE' });
  if (res.ok) {
    if (!opts.skipBrowse) _browse(currentPath, false);
    return true;
  }
  alert('삭제 실패');
  return false;
}

// deleteSelectedFiles는 selectedPaths 전체를 병렬로 DELETE한다. selection은
// 항상 파일만 담고 있다(bindEntrySelection이 폴더 체크박스를 숨김). 부분 실패는
// allSettled로 끝까지 시도한 뒤 실패 path 목록을 alert에 모아 보여준다 — 성공한
// 항목은 그대로 삭제된 상태로 둔다. browse refresh가 syncSelectionWithVisible을
// 통해 잔존 selection을 정리한다.
export async function deleteSelectedFiles() {
  const paths = Array.from(selectedPaths);
  if (paths.length === 0) return;
  const previewLines = paths.slice(0, 3).join('\n')
    + (paths.length > 3 ? `\n…외 ${paths.length - 3}개` : '');
  if (!confirm(`선택한 ${paths.length}개 항목을 삭제하시겠습니까?\n\n${previewLines}`)) return;

  const btn = $.deleteSelectionBtn;
  const originalText = btn.textContent;
  btn.disabled = true;
  btn.textContent = '삭제 중...';
  try {
    const results = await Promise.allSettled(
      paths.map(p => fetch('/api/file?path=' + encodeURIComponent(p), { method: 'DELETE' }))
    );
    const failed = [];
    results.forEach((r, i) => {
      if (r.status === 'fulfilled' && r.value.ok) {
        selectedPaths.delete(paths[i]);
      } else {
        failed.push(paths[i]);
      }
    });
    if (failed.length > 0) {
      const head = failed.slice(0, 5).join('\n');
      const tail = failed.length > 5 ? `\n…외 ${failed.length - 5}개` : '';
      alert(`${failed.length}개 삭제 실패:\n${head}${tail}`);
    }
    _browse(currentPath, false);
  } finally {
    btn.disabled = false;
    btn.textContent = originalText;
  }
}

// updateDeleteSelectionBtn은 selection 종속 가시성을 갱신한다. selection.js의
// refreshSelectionUI가 download 버튼과 함께 호출 — 단일 출처에서 배치 갱신.
export function updateDeleteSelectionBtn() {
  const n = selectedPaths.size;
  if (n === 0) {
    $.deleteSelectionBtn.hidden = true;
    return;
  }
  $.deleteSelectionBtn.hidden = false;
  $.deleteSelectionBtn.textContent = `선택 삭제 (${n}개)`;
}

export async function deleteFolder(path) {
  if (!confirm(`폴더 안의 모든 파일이 삭제됩니다.\n${path}\n\n계속하시겠습니까?`)) return;
  const res = await fetch('/api/folder?path=' + encodeURIComponent(path), { method: 'DELETE' });
  if (res.ok) {
    // currentPath가 삭제된 폴더 내부였거나 그 폴더 자체였다면, browse()는
    // 더 이상 존재하지 않는 경로에 대해 404를 받는다. 사용자가 유효한
    // 위치에 안착하도록 부모로 폴백한다.
    if (currentPath === path || currentPath.startsWith(path + '/')) {
      _browse(parentDir(path));
    } else {
      _browse(currentPath, false);
    }
    _loadTree();
  } else {
    alert('폴더 삭제 실패');
  }
}

// ── Drag and Drop (파일 이동) ────────────────────────────────────────────────
function isInternalMove(e) {
  return Array.from(e.dataTransfer.types).includes(DND_MIME);
}

function selectedMovePathsFor(entry) {
  if (selectedPaths.has(entry.path)) return Array.from(selectedPaths);
  return [entry.path];
}

export function attachDragHandlers(el, entry) {
  el.draggable = true;
  el.addEventListener('dragstart', e => {
    // 폴더는 항상 단일 대상이다. 파일은 활성 selection을 함께 운반할 수 있다.
    const isDir = !!entry.is_dir;
    const paths = isDir ? [entry.path] : selectedMovePathsFor(entry);
    setDragSrcPath(entry.path);
    setDragSrcPaths(paths);
    dragSrcIsDir = isDir;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData(DND_MIME, JSON.stringify({ src: entry.path, paths, isDir }));
    // Firefox는 text/plain이나 text/uri-list가 설정되지 않으면 drag를 시작하지 않는다.
    e.dataTransfer.setData('text/plain', paths.join('\n'));
    el.classList.add('dragging');
  });
  el.addEventListener('dragend', () => {
    setDragSrcPath(null);
    setDragSrcPaths([]);
    dragSrcIsDir = false;
    el.classList.remove('dragging');
  });
}

function canDropMoveTo(destPath) {
  const paths = dragSrcPaths.length ? dragSrcPaths : (dragSrcPath ? [dragSrcPath] : []);
  if (!paths.length) return true;
  // 폴더 drag: 자기 자신이나 후손에 drop하는 것을 금지한다. separator 경계로
  // /a/b가 /a/bc 안에 있다고 잘못 인식되는 것을 막는다. media.MoveDir 미러.
  if (dragSrcIsDir) {
    for (const p of paths) {
      if (destPath === p) return false;
      if (destPath.startsWith(p + '/')) return false;
    }
  }
  return paths.some(path => parentDir(path) !== destPath);
}

export function attachDropHandlers(el, destPath) {
  el.addEventListener('dragenter', e => {
    if (!isInternalMove(e)) return;
    if (!canDropMoveTo(destPath)) return;
    e.preventDefault();
    el.classList.add('drop-target');
  });
  el.addEventListener('dragover', e => {
    if (!isInternalMove(e)) return;
    if (!canDropMoveTo(destPath)) {
      e.dataTransfer.dropEffect = 'none';
      return;
    }
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  });
  el.addEventListener('dragleave', () => {
    el.classList.remove('drop-target');
  });
  el.addEventListener('drop', e => {
    if (!isInternalMove(e)) return;
    e.preventDefault();
    e.stopPropagation();
    el.classList.remove('drop-target');
    let payload;
    try {
      payload = JSON.parse(e.dataTransfer.getData(DND_MIME));
    } catch {
      return;
    }
    if (!payload || !payload.src) return;
    if (payload.isDir) {
      moveFolder(payload.src, destPath);
      return;
    }
    const paths = Array.isArray(payload.paths) && payload.paths.length
      ? payload.paths
      : [payload.src];
    moveFiles(paths, destPath);
  });
}

async function moveFolder(srcPath, destDir) {
  // 방어적 가드 — 서버가 강제하는 것과 같은 규칙. UI race로 drop이 새어
  // 나갔을 때 round-trip 없이 친절한 메시지를 표면화한다.
  if (parentDir(srcPath) === destDir) return;
  if (destDir === srcPath || destDir.startsWith(srcPath + '/')) return;
  try {
    const res = await fetch('/api/folder?path=' + encodeURIComponent(srcPath), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ to: destDir }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      const label = FOLDER_MOVE_ERROR_LABELS[data.error] || data.error || res.statusText;
      alert('폴더 이동 실패: ' + label);
      return;
    }
    const data = await res.json().catch(() => null);
    const newPath = data && data.path ? data.path : srcPath;
    // 이동된 폴더가 currentPath나 그 조상이었다면 URL은 이제 stale 상태다.
    const target = rewritePathAfterFolderRename(srcPath, newPath, currentPath);
    if (target !== currentPath) {
      _browse(target);
    } else {
      _browse(currentPath, false);
    }
    _loadTree();
  } catch (e) {
    alert('폴더 이동 실패: ' + e.message);
  }
}

const FOLDER_MOVE_ERROR_LABELS = {
  'already exists': '대상 위치에 같은 이름의 폴더가 있습니다',
  'invalid destination': '이동할 수 없는 위치입니다',
  'same directory': '같은 폴더입니다',
  'cannot move root': '루트는 이동할 수 없습니다',
  'not a directory': '폴더가 아닙니다',
  'not found': '원본을 찾을 수 없습니다',
  'cross_device': '다른 볼륨으로는 이동할 수 없습니다',
};

async function moveFiles(srcPaths, destDir) {
  const paths = Array.from(new Set(srcPaths)).filter(path => parentDir(path) !== destDir);
  if (paths.length === 0) return; // 방어적 — 백엔드도 어차피 막는다
  const failed = [];
  try {
    for (const path of paths) {
      const res = await fetch('/api/file?path=' + encodeURIComponent(path), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ to: destDir }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        failed.push(`${path}: ${data.error || res.statusText}`);
        continue;
      }
      selectedPaths.delete(path);
    }
    // 파일 이동에서는 폴더 구조가 바뀌지 않는다. 목록만 새로고침하면 된다.
    _browse(currentPath, false);
    if (failed.length) {
      alert(`이동 실패 ${failed.length}개\n` + failed.join('\n'));
    }
  } catch (e) {
    alert('이동 실패: ' + e.message);
  }
}

// ── 이름 변경 ────────────────────────────────────────────────────────────────
let renameTarget = null;
let renameSubmitting = false;

export function openRenameModal(entry) {
  renameTarget = entry;
  $.renameError.textContent = '';
  $.renameError.classList.add('hidden');

  const { base, ext } = entry.is_dir ? { base: entry.name, ext: '' } : splitExtension(entry.name);
  $.renameTitle.textContent = entry.is_dir ? '폴더 이름 변경' : '파일 이름 변경';
  if (ext) {
    $.renameHint.textContent = `확장자: ${ext} (변경 불가)`;
    $.renameHint.classList.remove('hidden');
  } else {
    $.renameHint.classList.add('hidden');
  }
  $.renameInput.value = base;
  $.renameModal.classList.remove('hidden');
  $.renameInput.focus();
  $.renameInput.select();
}

function closeRenameModal() {
  $.renameModal.classList.add('hidden');
  renameTarget = null;
}

async function submitRename() {
  if (renameSubmitting || !renameTarget) return;
  const newBase = $.renameInput.value.trim();
  if (!newBase) {
    showRenameError('이름을 입력하세요.');
    return;
  }
  const nameErr = validateRenameInput(newBase);
  if (nameErr) {
    showRenameError(nameErr);
    return;
  }
  const entry = renameTarget;
  const url = entry.is_dir
    ? '/api/folder?path=' + encodeURIComponent(entry.path)
    : '/api/file?path=' + encodeURIComponent(entry.path);
  renameSubmitting = true;
  $.renameConfirmBtn.disabled = true;
  try {
    const res = await fetch(url, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: newBase }),
    });
    if (res.ok) {
      closeRenameModal();
      if (entry.is_dir) {
        const data = await res.json().catch(() => null);
        const newPath = data && data.path ? data.path : entry.path;
        // 이름이 바뀐 폴더가 currentPath이거나 그 조상이라면 브라우저는
        // 이제 무의미해진 URL에 있다 — 새 prefix로 다시 쓴다.
        const target = rewritePathAfterFolderRename(entry.path, newPath, currentPath);
        if (target !== currentPath) {
          _browse(target);
        } else {
          _browse(currentPath, false);
        }
        _loadTree();
      } else {
        _browse(currentPath, false);
      }
      return;
    }
    const err = await res.json().catch(() => ({}));
    if (res.status === 409) {
      showRenameError('이미 같은 이름이 있습니다.');
    } else if (res.status === 400 && err.error === 'name unchanged') {
      showRenameError('이름이 같습니다.');
    } else if (res.status === 404) {
      showRenameError('대상을 찾을 수 없습니다.');
    } else {
      showRenameError('유효하지 않은 이름입니다.');
    }
  } finally {
    renameSubmitting = false;
    $.renameConfirmBtn.disabled = false;
  }
}

function showRenameError(msg) {
  $.renameError.textContent = msg;
  $.renameError.classList.remove('hidden');
}

export function wireFileOps(deps) {
  _browse = deps.browse;
  _loadTree = deps.loadTree;

  // 업로드
  $.uploadZone.addEventListener('dragover', e => {
    if (!isExternalFileDrag(e)) return;
    e.preventDefault();
    $.uploadZone.classList.add('drag-over');
  });
  $.uploadZone.addEventListener('dragleave', () => $.uploadZone.classList.remove('drag-over'));
  $.uploadZone.addEventListener('drop', e => {
    if (!isExternalFileDrag(e)) return;
    e.preventDefault();
    $.uploadZone.classList.remove('drag-over');
    uploadFiles(e.dataTransfer.files);
  });
  $.fileInput.addEventListener('change', () => {
    uploadFiles($.fileInput.files);
    $.fileInput.value = '';
  });

  // 폴더 생성
  $.newFolderBtn.addEventListener('click', openFolderModal);
  $.folderCancelBtn.addEventListener('click', closeFolderModal);
  $.folderModal.addEventListener('click', e => {
    if (e.target === $.folderModal) closeFolderModal();
  });
  $.folderConfirmBtn.addEventListener('click', submitCreateFolder);
  $.folderNameInput.addEventListener('keydown', e => {
    if (e.key === 'Enter') submitCreateFolder();
  });

  // 선택 삭제
  $.deleteSelectionBtn.addEventListener('click', deleteSelectedFiles);

  // 이름 변경
  $.renameCancelBtn.addEventListener('click', closeRenameModal);
  $.renameModal.addEventListener('click', e => {
    if (e.target === $.renameModal) closeRenameModal();
  });
  $.renameConfirmBtn.addEventListener('click', submitRename);
  $.renameInput.addEventListener('keydown', e => {
    if (e.key === 'Enter') submitRename();
    if (e.key === 'Escape') closeRenameModal();
  });
}
