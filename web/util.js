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
