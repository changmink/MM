// convert.js — TS → MP4 ffmpeg remux 모달.
// 모달 골격은 sseConvertModal 팩토리에 위임하고 여기서는 라벨·DOM 매핑만.

import { $ } from './dom.js';
import { createConvertModal } from './sseConvertModal.js';

const ERROR_LABELS = {
  invalid_path: '잘못된 경로',
  not_found: '파일 없음',
  not_a_file: '파일이 아님',
  not_ts: 'TS 파일이 아님',
  already_exists: '같은 이름의 MP4 존재',
  ffmpeg_missing: 'ffmpeg 미설치 (서버 설정 필요)',
  ffmpeg_error: '변환 실패 (손상되었거나 비호환 코덱)',
  convert_timeout: '타임아웃 (10분)',
  canceled: '취소됨',
  write_error: '저장 실패',
};

const WARN_LABELS = {
  delete_original_failed: '원본 삭제 실패',
};

const modal = createConvertModal({
  endpoint: '/api/convert',
  errorLabels: ERROR_LABELS,
  warnLabels: WARN_LABELS,
  dom: {
    modal: $.convertModal,
    rows: $.convertRows,
    summary: $.convertSummary,
    result: $.convertResult,
    confirmBtn: $.convertConfirmBtn,
    cancelBtn: $.convertCancelBtn,
    error: $.convertError,
    deleteOrig: $.convertDeleteOrig,
    fileList: $.convertFileList,
  },
});

export const openConvertModal = modal.open;
export const wireConvert = modal.wire;
