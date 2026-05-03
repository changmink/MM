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

// YouTube 스타일 duration: <1h → "M:SS", ≥1h → "H:MM:SS".
// 초가 미상이거나 0 이하면 null을 반환해 호출자가 렌더링을 건너뛸 수 있게 한다.
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
  // 서버 미러: filepath.Ext는 마지막 ".ext" 또는 ""를 반환한다.
  // 폴더 이름이나 확장자 없는 파일은 ext = ''.
  const dot = name.lastIndexOf('.');
  if (dot <= 0 || dot === name.length - 1) return { base: name, ext: '' };
  return { base: name.slice(0, dot), ext: name.slice(dot) };
}

// 서버 validateName(internal/handler/names.go) 미러. 이름이 허용 가능하면
// null을, 아니면 첫 위반을 설명하는 사람이 읽을 수 있는 한글 메시지를
// 반환한다. 최종 결정은 서버가 하지만, 명백한 케이스를 round-trip 전에 잡아
// 일반적인 "유효하지 않은 이름입니다." 대신 유용한 UX 메시지를 표면화한다.
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
  // 예약 basename 검사(대소문자 무시, 확장자 유무 무관).
  // 서버의 stripTrailingExt + ToUpper(base)를 미러링.
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

// SSE 응답 본문을 파싱한다. 각 `data: ...` 프레임의 디코드된 JSON으로
// onEvent를 호출한다. 스트림이 닫히면 멈춘다. 도메인 비종속 — URL import와
// TS→MP4 convert가 모두 사용한다.
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
