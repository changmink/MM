// settings.js — 다운로드 설정 모달 (⚙)

import { $ } from './dom.js';

// Client-side bounds mirror the server's — they keep a typo from ever hitting
// the backend, but the server revalidates regardless (defense in depth).
const SETTINGS_MAX_MIB_MIN = 1;
const SETTINGS_MAX_MIB_MAX = 1024 * 1024; // 1 TiB
const SETTINGS_TIMEOUT_MIN = 1;
const SETTINGS_TIMEOUT_MAX = 240;
const SETTINGS_FIELD_LABELS = {
  url_import_max_bytes: '최대 다운로드 크기',
  url_import_timeout_seconds: '다운로드 타임아웃',
};

async function openSettingsModal() {
  $.settingsError.textContent = '';
  $.settingsError.classList.add('hidden');
  $.settingsMaxInput.value = '';
  $.settingsTimeInput.value = '';
  $.settingsAutoPNG.checked = true; // optimistic — overwritten by GET
  $.settingsMaxHint.textContent = '';
  $.settingsModal.classList.remove('hidden');
  $.settingsMaxInput.focus();

  try {
    const res = await fetch('/api/settings');
    if (!res.ok) throw new Error('status ' + res.status);
    const cur = await res.json();
    $.settingsMaxInput.value = Math.round(cur.url_import_max_bytes / (1024 * 1024));
    $.settingsTimeInput.value = Math.round(cur.url_import_timeout_seconds / 60);
    $.settingsAutoPNG.checked = !!cur.auto_convert_png_to_jpg;
    updateSettingsMaxHint();
  } catch (e) {
    showSettingsError('설정을 불러오지 못했습니다: ' + e.message);
  }
}

function closeSettingsModal() {
  $.settingsModal.classList.add('hidden');
}

function updateSettingsMaxHint() {
  const mib = parseInt($.settingsMaxInput.value, 10);
  if (!Number.isFinite(mib) || mib <= 0) {
    $.settingsMaxHint.textContent = '';
    return;
  }
  const gib = mib / 1024;
  // Show MiB as-is for sub-GiB values, GiB with one decimal otherwise.
  $.settingsMaxHint.textContent = gib < 1
    ? `≈ ${mib} MiB`
    : `≈ ${gib.toFixed(gib >= 10 ? 0 : 1)} GiB`;
}

async function submitSettings() {
  const mib = parseInt($.settingsMaxInput.value, 10);
  const minutes = parseInt($.settingsTimeInput.value, 10);
  if (!Number.isInteger(mib) || mib < SETTINGS_MAX_MIB_MIN || mib > SETTINGS_MAX_MIB_MAX) {
    showSettingsError(`최대 다운로드 크기는 ${SETTINGS_MAX_MIB_MIN}~${SETTINGS_MAX_MIB_MAX} MiB 범위여야 합니다.`);
    $.settingsMaxInput.focus();
    return;
  }
  if (!Number.isInteger(minutes) || minutes < SETTINGS_TIMEOUT_MIN || minutes > SETTINGS_TIMEOUT_MAX) {
    showSettingsError(`타임아웃은 ${SETTINGS_TIMEOUT_MIN}~${SETTINGS_TIMEOUT_MAX} 분 범위여야 합니다.`);
    $.settingsTimeInput.focus();
    return;
  }

  $.settingsError.classList.add('hidden');
  $.settingsConfirmBtn.disabled = true;
  $.settingsConfirmBtn.textContent = '저장 중...';
  try {
    const payload = {
      url_import_max_bytes: mib * 1024 * 1024,
      url_import_timeout_seconds: minutes * 60,
      auto_convert_png_to_jpg: $.settingsAutoPNG.checked,
    };
    const res = await fetch('/api/settings', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!res.ok) {
      let msg = `저장 실패 (${res.status})`;
      try {
        const body = await res.json();
        if (body.error === 'out_of_range' && body.field) {
          const label = SETTINGS_FIELD_LABELS[body.field] || body.field;
          msg = `${label} 값이 허용 범위를 벗어났습니다.`;
        } else if (body.error) {
          msg = body.error;
        }
      } catch { /* not JSON */ }
      showSettingsError(msg);
      return;
    }
    closeSettingsModal();
  } catch (e) {
    showSettingsError('저장 실패: ' + e.message);
  } finally {
    $.settingsConfirmBtn.disabled = false;
    $.settingsConfirmBtn.textContent = '저장';
  }
}

function showSettingsError(msg) {
  $.settingsError.textContent = msg;
  $.settingsError.classList.remove('hidden');
}

export function wireSettings() {
  $.settingsBtn.addEventListener('click', openSettingsModal);
  $.settingsCancelBtn.addEventListener('click', closeSettingsModal);
  $.settingsConfirmBtn.addEventListener('click', submitSettings);
  $.settingsModal.addEventListener('click', e => {
    if (e.target === $.settingsModal) closeSettingsModal();
  });
  $.settingsMaxInput.addEventListener('input', updateSettingsMaxHint);
  document.addEventListener('keydown', e => {
    if ($.settingsModal.classList.contains('hidden')) return;
    if (e.key === 'Escape') closeSettingsModal();
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submitSettings(); }
  });
}
