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
} from './util.js';

// Tracks whether the active drag is a single folder. Folders take a separate
// PATCH endpoint and a separate self/descendant-rejection rule, so the drop
// handler needs this even before reading the dataTransfer payload.
let dragSrcIsDir = false;

let _browse = null;
let _loadTree = null;

// ── Upload ────────────────────────────────────────────────────────────────────
// Internal card drags carry our custom MIME but no Files; external OS drags
// carry Files. Gate on Files so dragging an internal file over the upload
// zone doesn't light it up.
function isExternalFileDrag(e) {
  return Array.from(e.dataTransfer.types).includes('Files');
}

function uploadFiles(files) {
  Array.from(files).forEach(file => uploadOne(file));
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
      setTimeout(() => container.remove(), 1500);
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

// ── Folder Create ─────────────────────────────────────────────────────────────
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

// ── Delete ────────────────────────────────────────────────────────────────────
export async function deleteFile(path) {
  if (!confirm(`삭제하시겠습니까?\n${path}`)) return;
  const res = await fetch('/api/file?path=' + encodeURIComponent(path), { method: 'DELETE' });
  if (res.ok) {
    _browse(currentPath, false);
  } else {
    alert('삭제 실패');
  }
}

export async function deleteFolder(path) {
  if (!confirm(`폴더 안의 모든 파일이 삭제됩니다.\n${path}\n\n계속하시겠습니까?`)) return;
  const res = await fetch('/api/folder?path=' + encodeURIComponent(path), { method: 'DELETE' });
  if (res.ok) {
    // If currentPath sat inside (or was) the deleted folder, browse() would
    // 404 on a path that no longer exists. Fall back to the parent so the
    // user lands somewhere valid.
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

// ── Drag and Drop (file move) ────────────────────────────────────────────────
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
    // Folders are always single-target. Files can carry the active selection.
    const isDir = !!entry.is_dir;
    const paths = isDir ? [entry.path] : selectedMovePathsFor(entry);
    setDragSrcPath(entry.path);
    setDragSrcPaths(paths);
    dragSrcIsDir = isDir;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData(DND_MIME, JSON.stringify({ src: entry.path, paths, isDir }));
    // Firefox won't initiate a drag without text/plain or text/uri-list set.
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
  // Folder drag: forbid dropping onto self or any descendant. Separator boundary
  // ensures /a/b is not flagged as inside /a/bc. Mirrors media.MoveDir.
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
  // Defensive guards — same rules the server enforces, so we surface friendly
  // messages without a round trip when the UI race lets a drop slip through.
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
    // If the moved folder was currentPath or an ancestor, the URL is now stale.
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
  if (paths.length === 0) return; // defensive — also blocked by backend
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
    // Folder structure unchanged on file move; only the listing needs a refresh.
    _browse(currentPath, false);
    if (failed.length) {
      alert(`이동 실패 ${failed.length}개\n` + failed.join('\n'));
    }
  } catch (e) {
    alert('이동 실패: ' + e.message);
  }
}

// ── Rename ────────────────────────────────────────────────────────────────────
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
        // If the renamed folder is currentPath or an ancestor of it, the
        // browser is sitting on a now-defunct URL — rewrite to the new prefix.
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

  // Upload
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

  // Folder create
  $.newFolderBtn.addEventListener('click', openFolderModal);
  $.folderCancelBtn.addEventListener('click', closeFolderModal);
  $.folderModal.addEventListener('click', e => {
    if (e.target === $.folderModal) closeFolderModal();
  });
  $.folderConfirmBtn.addEventListener('click', submitCreateFolder);
  $.folderNameInput.addEventListener('keydown', e => {
    if (e.key === 'Enter') submitCreateFolder();
  });

  // Rename
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
