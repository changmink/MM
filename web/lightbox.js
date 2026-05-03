// lightbox.js — 라이트박스(이미지/동영상) + 오디오 플레이리스트.
//
// 두 도메인을 한 모듈에 두는 이유: 카드 클릭 분기(browse.handleClick)가
// 양쪽 모두에 진입하고, "현재 항목 + prev/next" 패턴이 응집도 높다.
//
// 사이클 회피: 라이트박스 삭제 후 폴더 새로고침이 필요한데 browse() 를
// 직접 import 하면 lightbox → browse → lightbox 순환. wireLightbox
// 시점에 onAfterDelete 콜백을 주입받는다 (plan §6 #4).

import { $ } from './dom.js';
import { esc } from './util.js';
import {
  imageEntries,
  lbIndex, setLbIndex,
  lbCurrentVideoPath, setLbCurrentVideoPath,
  playlist,
  playlistIndex, setPlaylistIndex,
} from './state.js';
import { deleteFile } from './fileOps.js';

let _onAfterDelete = null;

export function wireLightbox({ onAfterDelete }) {
  _onAfterDelete = onAfterDelete;

  $.lbClose.addEventListener('click', closeLightbox);
  $.lbDelete.addEventListener('click', deleteCurrentLightboxItem);
  $.lbPrev.addEventListener('click', () => {
    if (!imageEntries.length) return;
    setLbIndex((lbIndex - 1 + imageEntries.length) % imageEntries.length);
    openLightboxImage(lbIndex);
  });
  $.lbNext.addEventListener('click', () => {
    if (!imageEntries.length) return;
    setLbIndex((lbIndex + 1) % imageEntries.length);
    openLightboxImage(lbIndex);
  });
  $.lightbox.addEventListener('click', e => {
    if (e.target === $.lightbox) closeLightbox();
  });
  document.addEventListener('keydown', e => {
    if ($.lightbox.classList.contains('hidden')) return;
    if (e.key === 'Escape') closeLightbox();
    if (e.key === 'ArrowLeft') $.lbPrev.click();
    if (e.key === 'ArrowRight') $.lbNext.click();
    if (e.key === 'Delete') deleteCurrentLightboxItem();
  });

  // 오디오 자동 다음 곡 — 모듈 모드 import는 읽기 전용 바인딩이라 이
  // 리스너의 원래 `playlistIndex++`가 FM-1 이후 조용히 TypeError를 일으켰다.
  // setter를 거쳐 수정한다.
  $.audioEl.addEventListener('ended', () => {
    if (playlistIndex < playlist.length - 1) {
      setPlaylistIndex(playlistIndex + 1);
      loadPlaylistTrack(playlistIndex);
    }
  });
}

// ── Lightbox ──────────────────────────────────────────────────────────────────
export function openLightboxImage(index) {
  setLbIndex(index);
  setLbCurrentVideoPath(null);
  const entry = imageEntries[lbIndex];
  $.lbContent.innerHTML = `<img src="/api/stream?path=${encodeURIComponent(entry.path)}" alt="${esc(entry.name)}">`;
  $.lightbox.classList.remove('hidden');
}

export function openLightboxVideo(entry) {
  setLbCurrentVideoPath(entry.path);
  const mime = entry.path.toLowerCase().endsWith('.ts') ? 'video/mp4' : (entry.mime || 'video/mp4');
  $.lbContent.innerHTML = `
    <video controls autoplay>
      <source src="/api/stream?path=${encodeURIComponent(entry.path)}" type="${esc(mime)}">
    </video>`;
  $.lightbox.classList.remove('hidden');
}

// 닫기 트리거(✕ / 배경 클릭 / Esc)는 모두 이 함수를 거치게 해서
// lbCurrentVideoPath 리셋이 한 곳에 모이게 한다 — 누락 시 다음 이미지
// 라이트박스에서 stale path가 살아남아 삭제 분기가 동영상으로 새는 버그 발생.
function closeLightbox() {
  $.lightbox.classList.add('hidden');
  $.lbContent.innerHTML = '';
  setLbCurrentVideoPath(null);
}

async function deleteCurrentLightboxItem() {
  if (lbCurrentVideoPath) {
    const ok = await deleteFile(lbCurrentVideoPath, { skipBrowse: true });
    if (!ok) return;
    closeLightbox();
    if (_onAfterDelete) _onAfterDelete();
  } else if (imageEntries.length) {
    const entry = imageEntries[lbIndex];
    const ok = await deleteFile(entry.path, { skipBrowse: true });
    if (!ok) return;
    imageEntries.splice(lbIndex, 1);
    if (imageEntries.length === 0) {
      closeLightbox();
    } else {
      setLbIndex(lbIndex % imageEntries.length);
      openLightboxImage(lbIndex);
    }
    if (_onAfterDelete) _onAfterDelete();
  }
}

// ── Audio Player ──────────────────────────────────────────────────────────────
export function playAudio(entry) {
  setPlaylistIndex(playlist.findIndex(e => e.path === entry.path));
  if (playlistIndex < 0) setPlaylistIndex(0);
  loadPlaylistTrack(playlistIndex);
  $.audioPlayer.classList.remove('hidden');
  renderPlaylist();
}

function loadPlaylistTrack(index) {
  const entry = playlist[index];
  $.audioEl.src = '/api/stream?path=' + encodeURIComponent(entry.path);
  $.audioTitle.textContent = entry.name;
  $.audioEl.play();
  renderPlaylist();
}

function renderPlaylist() {
  $.playlistEl.innerHTML = '';
  playlist.forEach((entry, i) => {
    const item = document.createElement('div');
    item.className = 'playlist-item' + (i === playlistIndex ? ' active' : '');
    item.textContent = entry.name;
    item.addEventListener('click', () => {
      setPlaylistIndex(i);
      loadPlaylistTrack(i);
    });
    $.playlistEl.appendChild(item);
  });
}
