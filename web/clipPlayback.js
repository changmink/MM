// clipPlayback.js — SPEC §2.5.6 GIF/WebP 카드 hover/IO 기반 자동재생 throttle.
// hover-capable 디바이스는 hover 시만 stream src 부착, 그 외(모바일 등)는
// IntersectionObserver 로 viewport 안에서만 활성. 모듈 lifetime 동안 IO
// 인스턴스 1개를 공유한다.

import { CLIP_MAX_BYTES, CLIP_MAX_DURATION_SEC } from './state.js';

const HOVER_CAPABLE = typeof window !== 'undefined'
  && window.matchMedia
  && window.matchMedia('(hover: hover)').matches;

let _clipIO = null;
function clipIOInstance() {
  if (_clipIO) return _clipIO;
  _clipIO = new IntersectionObserver(entries => {
    for (const ent of entries) {
      const card = ent.target;
      const img = card.querySelector('img');
      if (!img) continue;
      const desired = ent.isIntersecting ? 'stream' : 'thumb';
      if (card.dataset.clipState === desired) continue;
      card.dataset.clipState = desired;
      img.src = desired === 'stream' ? img.dataset.streamSrc : img.dataset.thumbSrc;
    }
  }, { rootMargin: '0px', threshold: 0.1 });
  return _clipIO;
}

export function attachClipHoverPlayback(card) {
  const img = card.querySelector('img');
  if (!img || !img.dataset.streamSrc) return;
  if (HOVER_CAPABLE) {
    let current = 'thumb';
    card.addEventListener('mouseenter', () => {
      if (current === 'stream') return;
      current = 'stream';
      img.src = img.dataset.streamSrc;
    });
    card.addEventListener('mouseleave', () => {
      if (current === 'thumb') return;
      current = 'thumb';
      img.src = img.dataset.thumbSrc;
    });
  } else {
    card.dataset.clipState = 'thumb';
    clipIOInstance().observe(card);
  }
}

// 움짤 분류 — GIF·WebP 는 무조건 움짤(SPEC §2.5.3); video 는 짧고 작을
// 때만 (null duration 제외 — 길이를 모르면 보수적으로 비-움짤).
// 분류와 변환 입력 자격은 다르다: webp 는 §2.9 변환 결과물이므로 입력
// 자격에서 빠진다 (isClipConvertable).
export function isClip(e) {
  if (e.mime === 'image/gif' || e.mime === 'image/webp') return true;
  if (e.type === 'video') {
    return e.size <= CLIP_MAX_BYTES
      && e.duration_sec != null
      && e.duration_sec <= CLIP_MAX_DURATION_SEC;
  }
  return false;
}

// isClipConvertable — §2.9 변환 입력 자격. isClip 의 부분집합으로 webp 를
// 제외한다 (변환 결과물이라 재변환 의도 없음). 서버도 동일 결정 — webp
// 입력은 unsupported_input 으로 거부.
export function isClipConvertable(e) {
  return isClip(e) && e.mime !== 'image/webp';
}
