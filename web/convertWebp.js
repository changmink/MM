// convertWebp.js — 움짤 → animated WebP 변환 모달.
// 모달 골격은 sseConvertModal 팩토리에 위임하고 여기서는 라벨·DOM·툴바
// 일괄 버튼 매핑만.

import { $ } from './dom.js';
import { createConvertModal } from './sseConvertModal.js';

const ERROR_LABELS = {
  invalid_path: '잘못된 경로',
  not_found: '파일 없음',
  not_a_file: '파일이 아님',
  unsupported_input: '지원하지 않는 입력 (이미지·오디오·기타)',
  not_clip: '움짤 조건 미충족 (50 MiB 또는 30s 초과)',
  duration_unknown: '동영상 길이 확인 실패',
  already_exists: '같은 이름의 WebP 존재',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  ffmpeg_error: '인코딩 실패 (손상되었거나 비호환 코덱)',
  convert_timeout: '타임아웃 (5분)',
  canceled: '취소됨',
  write_error: '저장 실패',
};

const WARN_LABELS = {
  audio_dropped: '오디오 제거됨',
  delete_original_failed: '원본 삭제 실패',
};

const modal = createConvertModal({
  endpoint: '/api/convert-webp',
  errorLabels: ERROR_LABELS,
  warnLabels: WARN_LABELS,
  dom: {
    modal: $.convertWebpModal,
    rows: $.convertWebpRows,
    summary: $.convertWebpSummary,
    result: $.convertWebpResult,
    confirmBtn: $.convertWebpConfirmBtn,
    cancelBtn: $.convertWebpCancelBtn,
    error: $.convertWebpError,
    deleteOrig: $.convertWebpDeleteOrig,
    fileList: $.convertWebpFileList,
    allBtn: $.convertWebpAllBtn,
  },
});

export const openConvertWebPModal = modal.open;
export const wireConvertWebP = modal.wire;
