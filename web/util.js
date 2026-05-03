// util.js — 작은 순수 헬퍼 (DOM·상태 비의존)

export function iconFor(type, isDir) {
  if (isDir) return '📁';
  if (type === 'image') return '🖼';
  if (type === 'video') return '🎬';
  if (type === 'audio') return '🎵';
  return '📄';
}

export function formatSize(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

// YouTube-style duration: <1h → "M:SS", ≥1h → "H:MM:SS".
// Returns null when seconds is unknown or non-positive so callers can skip rendering.
export function formatDuration(sec) {
  if (sec == null || !Number.isFinite(sec) || sec <= 0) return null;
  const total = Math.floor(sec);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, '0');
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${ss}`;
  return `${m}:${ss}`;
}

export function esc(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function splitExtension(name) {
  // Mirror the server: filepath.Ext returns the final ".ext" or "".
  // Folder names or extension-less files have ext = ''.
  const dot = name.lastIndexOf('.');
  if (dot <= 0 || dot === name.length - 1) return { base: name, ext: '' };
  return { base: name.slice(0, dot), ext: name.slice(dot) };
}

// Mirrors server validateName (internal/handler/names.go). Returns null if
// the name is acceptable, or a human-readable Korean message describing the
// first violation. Server has the final say, but this catches obvious cases
// before a roundtrip and surfaces a useful UX message instead of generic
// "유효하지 않은 이름입니다.".
const RESERVED_BASENAMES = new Set(['CON', 'PRN', 'AUX', 'NUL']);
export function validateRenameInput(name) {
  if (name === '' || name === '.' || name === '..') return '이름을 입력하세요.';
  if (name.length > 255) return '이름이 너무 깁니다 (최대 255자).';
  for (let i = 0; i < name.length; i++) {
    const c = name.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return '이름에 사용할 수 없는 제어 문자가 있습니다.';
    const ch = name[i];
    if (ch === '/' || ch === '\\' || ch === '<' || ch === '>' ||
        ch === ':' || ch === '"' || ch === '|' || ch === '?' || ch === '*') {
      return `이름에 사용할 수 없는 문자가 있습니다: ${ch}`;
    }
  }
  // Reserved basename check (case-insensitive, with or without extension).
  // Mirrors stripTrailingExt + ToUpper(base) on the server.
  const dot = name.lastIndexOf('.');
  const base = (dot > 0 ? name.slice(0, dot) : name).toUpperCase();
  if (RESERVED_BASENAMES.has(base)) return `'${base}'는 시스템 예약 이름입니다.`;
  if (base.length === 4 && (base.startsWith('COM') || base.startsWith('LPT'))) {
    const last = base.charCodeAt(3);
    if (last >= 0x31 && last <= 0x39) return `'${base}'는 시스템 예약 이름입니다.`;
  }
  return null;
}

export function parentDir(p) {
  if (!p || p === '/') return '/';
  const i = p.lastIndexOf('/');
  return i <= 0 ? '/' : p.substring(0, i);
}

export function rewritePathAfterFolderRename(oldPath, newPath, current) {
  if (current === oldPath) return newPath;
  if (current.startsWith(oldPath + '/')) {
    return newPath + current.substring(oldPath.length);
  }
  return current;
}

// Parses an SSE response body. onEvent is called with the decoded JSON of each
// `data: ...` frame. Stops when the stream closes. Domain-agnostic — both URL
// import and TS→MP4 convert use this.
export async function consumeSSE(res, onEvent) {
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let idx;
    while ((idx = buf.indexOf('\n\n')) !== -1) {
      const frame = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      const line = frame.trim();
      if (!line.startsWith('data:')) continue;
      const payload = line.slice(5).trim();
      try {
        onEvent(JSON.parse(payload));
      } catch (e) {
        console.warn('bad sse frame', payload, e);
      }
    }
  }
}
