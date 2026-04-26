// router.js — URL ↔ view 동기화 + popstate + toolbar 위젯 wiring
//
// browse / renderView는 caller가 wireRouter / wireToolbar에 주입한다.
// router → browse 의존이 발생하면 browse → router(syncURL) 와 함께 순환이 되므로
// DI로 단방향을 유지한다.

import { $, typeButtons } from './dom.js';
import { view, currentPath, SORT_VALUES, TYPE_VALUES } from './state.js';

export function readViewFromURL() {
  const p = new URLSearchParams(location.search);
  const s = p.get('sort'); view.sort = SORT_VALUES.has(s) ? s : 'name:asc';
  view.q = (p.get('q') || '').trim();
  const t = p.get('type'); view.type = TYPE_VALUES.has(t) ? t : 'all';
}

export function syncURL(push) {
  const p = new URLSearchParams();
  p.set('path', currentPath);
  if (view.sort !== 'name:asc') p.set('sort', view.sort);
  if (view.q) p.set('q', view.q);
  if (view.type !== 'all') p.set('type', view.type);
  const qs = '?' + p.toString();
  if (push) history.pushState({}, '', qs);
  else history.replaceState({}, '', qs);
}

export function syncToolbarUI() {
  typeButtons.forEach(btn => btn.classList.toggle('active', btn.dataset.type === view.type));
  $.toolbarSearch.value = view.q;
  $.toolbarSort.value = view.sort;
}

// popstate treats the URL as the source of truth — read view + path out of it,
// sync the toolbar widgets, then fetch. browse(..., false) won't rewrite the
// URL, so we don't loop.
export function wireRouter(browse) {
  window.addEventListener('popstate', () => {
    const p = new URLSearchParams(location.search).get('path') || '/';
    readViewFromURL();
    syncToolbarUI();
    browse(p, false);
  });
}

export function wireToolbar(renderView) {
  typeButtons.forEach(btn => {
    btn.addEventListener('click', () => {
      if (view.type === btn.dataset.type) return;
      view.type = btn.dataset.type;
      syncURL(false);
      syncToolbarUI();
      renderView();
    });
  });
  $.toolbarSearch.addEventListener('input', (e) => {
    view.q = e.target.value.trim();
    syncURL(false);
    renderView();
  });
  $.toolbarSort.addEventListener('change', (e) => {
    view.sort = e.target.value;
    syncURL(false);
    renderView();
  });
}
