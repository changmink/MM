// main.js — entry point. 모듈 import + init wiring 만 담당. 도메인 로직은 별도 파일.

import { browse, renderView, wireBrowse } from './browse.js';
import {
  readViewFromURL, syncToolbarUI,
  wireRouter, wireToolbar,
} from './router.js';
import { loadTree, wireTree } from './tree.js';
import {
  attachDragHandlers, attachDropHandlers,
  openRenameModal, wireFileOps,
} from './fileOps.js';
import { wireSettings } from './settings.js';
import { wireConvert } from './convert.js';
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
wireTree({ browse, attachDropHandlers, openRenameModal });
wireSettings();
wireConvert({ browse });
wireURLImport();
wireBrowse();

readViewFromURL();
syncToolbarUI();
const initPath = new URLSearchParams(location.search).get('path') || '/';
browse(initPath, false);
loadTree();
// Restore in-progress URL imports from the server (Phase 20 J4). Independent
// of browse/tree — safe to fire-and-forget; the badge appears asynchronously
// when the response arrives.
bootstrapURLJobs();
