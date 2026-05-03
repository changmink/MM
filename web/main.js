// main.js — entry point. 모듈 import + init wiring 만 담당. 도메인 로직은 별도 파일.

import { browse, renderView, wireBrowse } from './browse.js';
import {
  readViewFromURL, syncToolbarUI,
  wireRouter, wireToolbar,
} from './router.js';
import { loadTree, wireTree } from './tree.js';
import {
  attachDragHandlers, attachDropHandlers,
  openRenameModal, deleteFolder, wireFileOps,
} from './fileOps.js';
import { wireSettings } from './settings.js';
import { wireConvert } from './convert.js';
import { wireConvertImage } from './convertImage.js';
import { wireConvertWebP } from './convertWebp.js';
import { wireDragSelect } from './dragSelect.js';
import { wireDownload } from './download.js';
import { setURLImportDeps, wireURLImport } from './urlImport.js';
import {
  bootstrapURLJobs,
  cancelURLAt, cancelBatchAll, dismissBatch, dismissAllFinishedBatches,
} from './urlImportJobs.js';

// ── Init ──────────────────────────────────────────────────────────────────────
// urlImport 의 row/header 가 jobs 의 cancel/dismiss 콜백을 필요로 한다.
// 모듈 평가 후 반드시 wireURLImport / bootstrapURLJobs 보다 먼저 호출.
setURLImportDeps({
  cancelURLAt, cancelBatchAll, dismissBatch, dismissAllFinishedBatches,
  browse,
});

wireFileOps({ browse, loadTree });
wireRouter(browse);
wireToolbar(renderView);
wireTree({ browse, attachDropHandlers, attachDragHandlers, openRenameModal, deleteFolder });
wireSettings();
wireConvert({ browse });
wireConvertImage({ browse });
wireConvertWebP({ browse });
wireURLImport();
wireBrowse();
wireDragSelect();
wireDownload();

readViewFromURL();
syncToolbarUI();
const initPath = new URLSearchParams(location.search).get('path') || '/';
browse(initPath, false);
loadTree();
// 서버에서 진행 중인 URL import를 복원한다(Phase 20 J4). browse/tree와
// 독립적이라 fire-and-forget으로 안전하다 — 응답이 도착하면 badge가
// 비동기로 나타난다.
bootstrapURLJobs();
