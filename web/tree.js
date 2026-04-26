// tree.js — 사이드바 폴더 트리 + 모바일 토글 + sticky-until-bottom 보정
//
// browse / attachDropHandlers / openRenameModal 의존은 wireTree()에서 주입한다.
// (browse → highlightTreeCurrent 와의 순환 회피)

import { $ } from './dom.js';
import { currentPath, TREE_INIT_DEPTH } from './state.js';

let _browse = null;
let _attachDropHandlers = null;
let _attachDragHandlers = null;
let _openRenameModal = null;
let _deleteFolder = null;

export async function loadTree() {
  $.treeRoot.setAttribute('aria-busy', 'true');
  $.treeRoot.innerHTML = '<div class="tree-empty">로딩 중...</div>';
  try {
    const res = await fetch(`/api/tree?path=/&depth=${TREE_INIT_DEPTH}`);
    if (!res.ok) throw new Error(await res.text());
    const root = await res.json();
    $.treeRoot.innerHTML = '';
    if (!root.has_children) {
      $.treeRoot.innerHTML = '<div class="tree-empty">폴더가 없습니다.</div>';
      return;
    }
    renderTreeChildren(root.children, $.treeRoot, 0);
    highlightTreeCurrent();
    syncSidebarSticky();
  } catch (e) {
    showTreeError(e.message);
  } finally {
    $.treeRoot.setAttribute('aria-busy', 'false');
  }
}

function showTreeError(message) {
  $.treeRoot.innerHTML = '';
  const wrap = document.createElement('div');
  wrap.className = 'tree-error';
  wrap.setAttribute('role', 'alert');

  const text = document.createElement('span');
  text.textContent = `트리 로드 실패: ${message}`;
  wrap.appendChild(text);

  const retry = document.createElement('button');
  retry.type = 'button';
  retry.className = 'tree-retry';
  retry.textContent = '다시 시도';
  retry.addEventListener('click', loadTree);
  wrap.appendChild(retry);

  $.treeRoot.appendChild(wrap);
}

function renderTreeChildren(children, container, depth) {
  if (!children) return;
  children.forEach(node => container.appendChild(buildTreeNode(node, depth)));
}

function buildTreeNode(node, depth) {
  const wrap = document.createElement('div');
  wrap.className = 'tree-node';
  wrap.dataset.path = node.path;

  const row = document.createElement('div');
  row.className = 'tree-node-row';
  row.style.paddingLeft = (depth * 14 + 6) + 'px';

  const chevron = document.createElement('button');
  chevron.className = 'tree-chevron';
  chevron.type = 'button';
  if (node.has_children) {
    const expanded = node.children !== null;
    chevron.textContent = expanded ? '▼' : '▶';
    chevron.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    chevron.addEventListener('click', e => {
      e.stopPropagation();
      toggleNode(wrap, node, depth);
    });
  } else {
    chevron.textContent = '·';
    chevron.disabled = true;
  }

  const label = document.createElement('button');
  label.className = 'tree-label';
  label.type = 'button';
  label.textContent = node.name;
  label.title = node.path;
  label.addEventListener('click', () => {
    _browse(node.path);
    if (window.matchMedia('(max-width: 600px)').matches) {
      setSidebarOpen(false);
    }
  });

  const renameBtn = document.createElement('button');
  renameBtn.className = 'tree-rename';
  renameBtn.type = 'button';
  renameBtn.title = '이름 변경';
  renameBtn.setAttribute('aria-label', `${node.name} 이름 변경`);
  renameBtn.textContent = '✎';
  renameBtn.addEventListener('click', e => {
    e.stopPropagation();
    _openRenameModal({ name: node.name, path: node.path, is_dir: true });
  });

  const deleteBtn = document.createElement('button');
  deleteBtn.className = 'tree-delete';
  deleteBtn.type = 'button';
  deleteBtn.title = '폴더 삭제';
  deleteBtn.setAttribute('aria-label', `${node.name} 삭제`);
  deleteBtn.textContent = '🗑';
  deleteBtn.addEventListener('click', e => {
    e.stopPropagation();
    _deleteFolder(node.path);
  });

  row.appendChild(chevron);
  row.appendChild(label);
  row.appendChild(renameBtn);
  row.appendChild(deleteBtn);
  wrap.appendChild(row);
  _attachDropHandlers(row, node.path);
  _attachDragHandlers(row, { path: node.path, name: node.name, is_dir: true });

  const kids = document.createElement('div');
  kids.className = 'tree-children';
  if (node.children !== null) {
    renderTreeChildren(node.children, kids, depth + 1);
  } else {
    kids.classList.add('collapsed'); // not loaded yet
  }
  wrap.appendChild(kids);
  return wrap;
}

async function toggleNode(wrapEl, node, depth) {
  const kids = wrapEl.querySelector(':scope > .tree-children');
  const chevron = wrapEl.querySelector(':scope > .tree-node-row > .tree-chevron');
  const collapsed = kids.classList.contains('collapsed');

  // First-time expand of a not-yet-loaded subtree: fetch one level.
  if (collapsed && kids.childElementCount === 0) {
    chevron.textContent = '…';
    try {
      const res = await fetch(`/api/tree?path=${encodeURIComponent(node.path)}&depth=1`);
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      renderTreeChildren(data.children, kids, depth + 1);
      highlightTreeCurrent();
    } catch (e) {
      chevron.textContent = '▶';
      alert('하위 폴더 로드 실패: ' + e.message);
      return;
    }
  }

  if (collapsed) {
    kids.classList.remove('collapsed');
    chevron.textContent = '▼';
    chevron.setAttribute('aria-expanded', 'true');
  } else {
    kids.classList.add('collapsed');
    chevron.textContent = '▶';
    chevron.setAttribute('aria-expanded', 'false');
  }
  syncSidebarSticky();
}

export function highlightTreeCurrent() {
  $.treeRoot.querySelectorAll('.tree-node-row.active')
    .forEach(el => el.classList.remove('active'));
  if (currentPath === '/' || !currentPath) return;
  // CSS.escape handles slashes/quotes safely; required for arbitrary paths.
  const sel = `.tree-node[data-path="${CSS.escape(currentPath)}"] > .tree-node-row`;
  const target = $.treeRoot.querySelector(sel);
  if (target) target.classList.add('active');
}

// Sticky-until-bottom for the desktop sidebar. When the tree is taller than
// the viewport's available area, set a negative top so the sidebar pins with
// its bottom flush against the viewport bottom — page scroll then reveals the
// rest of the tree. Mobile (<600px) is a fixed drawer; leave its top alone.
export function syncSidebarSticky() {
  if (!$.sidebar) return;
  if (window.matchMedia('(max-width: 600px)').matches) {
    $.sidebar.style.top = '';
    return;
  }
  const headerH = parseInt(
    getComputedStyle(document.documentElement).getPropertyValue('--header-h'),
    10,
  ) || 57;
  const sidebarH = $.sidebar.scrollHeight;
  const viewportH = window.innerHeight;
  const overflow = Math.max(0, sidebarH - (viewportH - headerH));
  $.sidebar.style.top = (headerH - overflow) + 'px';
}

export function setSidebarOpen(open) {
  document.body.classList.toggle('sidebar-open', open);
  $.sidebarToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  $.sidebarToggle.setAttribute('aria-label', open ? '폴더 메뉴 닫기' : '폴더 메뉴 열기');
  $.sidebarBackdrop.classList.toggle('hidden', !open);
}

export function wireTree(deps) {
  _browse = deps.browse;
  _attachDropHandlers = deps.attachDropHandlers;
  _attachDragHandlers = deps.attachDragHandlers;
  _openRenameModal = deps.openRenameModal;
  _deleteFolder = deps.deleteFolder;

  $.sidebarToggle.addEventListener('click', () => {
    setSidebarOpen(!document.body.classList.contains('sidebar-open'));
  });
  $.sidebarBackdrop.addEventListener('click', () => setSidebarOpen(false));

  window.addEventListener('resize', syncSidebarSticky);
  if ($.sidebar && typeof ResizeObserver !== 'undefined') {
    // Catches mutations not covered by explicit syncSidebarSticky() calls
    // (e.g. third-party DOM changes, font load reflows).
    new ResizeObserver(syncSidebarSticky).observe($.sidebar);
  }
}
