package importurl

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"file_server/internal/importjob"
)

// listJobsResponse는 GET /api/import-url/jobs의 wire 형태다. active와
// finished를 미리 나눠두면 클라이언트의 필터 통과를 줄이고, 두 가지 UI
// affordance(라이브 progress 행 vs. dismiss 가능한 history 행)와도 잘
// 맞아떨어진다.
type listJobsResponse struct {
	Active   []importjob.JobSnapshot `json:"active"`
	Finished []importjob.JobSnapshot `json:"finished"`
}

// snapshotEnvelope는 /jobs/{id}/events의 첫 SSE 프레임이다. 스냅샷을
// phase 태그가 달린 envelope로 감싸 라이브 `start/progress/done/error/summary`
// 이벤트와 wire 형태가 대칭으로 유지된다 — 클라이언트는 `phase`로 라우팅하며
// 첫 프레임만 특별 처리할 필요가 없다.
type snapshotEnvelope struct {
	Phase string                `json:"phase"` // always "snapshot"
	Job   importjob.JobSnapshot `json:"job"`
}

// handleJobsRoot는 /api/import-url/jobs(후행 슬래시 없음)을 라우팅한다.
// GET은 active+finished 스냅샷을 반환하고, `?status=finished`인 DELETE는
// 모든 terminal Job을 한 번에 정리한다. 다른 메서드/쿼리는 405/400. 후행
// 슬래시가 있는 변종은 아래 handleJobsByID로 간다 — 두 등록이 합쳐져 /jobs와
// /jobs/{id}/... 공간 전체를 모호함 없이 덮는다.
func (h *Handler) HandleJobsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListJobs(w, r)
	case http.MethodDelete:
		h.handleDeleteFinishedJobs(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

// handleListJobs는 레지스트리가 현재 보유한 모든 Job의 스냅샷을 terminal
// 상태별로 나눠 반환한다. 헤더 경계 이후의 인코드 실패는 로그만 남기고
// 버린다 — 그 시점엔 클라이언트에게 보낼 유용한 것이 없고, writeError를
// 부르면 이미 flush된 본문 위에 잘못된 응답을 덧붙이게 된다.
func (h *Handler) handleListJobs(w http.ResponseWriter, _ *http.Request) {
	active, finished := h.registry.List()
	body := listJobsResponse{
		Active:   snapshotSlice(active),
		Finished: snapshotSlice(finished),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("list jobs encode", "err", err)
	}
}

// snapshotSlice는 Job 슬라이스에 Snapshot()을 펼친다. list와 (이후)
// cancel-broadcast 헬퍼가 변환 로직을 공유하도록 분리해 두었다.
func snapshotSlice(jobs []*importjob.Job) []importjob.JobSnapshot {
	out := make([]importjob.JobSnapshot, len(jobs))
	for i, j := range jobs {
		out[i] = j.Snapshot()
	}
	return out
}

// handleJobsByID는 /api/import-url/jobs/{id}/{action}을 분배한다. 세 형태가
// 연결되어 있다:
//
//	GET  /jobs/{id}/events         — SSE 스냅샷 + 라이브 스트림 (J4)
//	POST /jobs/{id}/cancel         — 배치 또는 URL 단위 취소 (J5)
//	DEL  /jobs/{id}                — terminal Job 제거 (J5)
//
// 그 외(오타 action, 추가 path 세그먼트, 잘못된 메서드)는 404 / 405를 반환해
// 기본 핸들러로 흘러가지 않게 한다.
func (h *Handler) HandleJobsByID(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/import-url/jobs/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "missing job id", nil)
		return
	}
	jobID := parts[0]
	if len(parts) == 1 {
		// /jobs/{id} 단독 — 여기는 DELETE만 유효하다.
		switch r.Method {
		case http.MethodDelete:
			h.handleDeleteJob(w, r, jobID)
		default:
			writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		}
		return
	}
	switch parts[1] {
	case "events":
		if len(parts) != 2 {
			writeError(w, r, http.StatusNotFound, "unknown route", nil)
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
			return
		}
		h.handleSubscribeJob(w, r, jobID)
	case "cancel":
		if len(parts) != 2 {
			writeError(w, r, http.StatusNotFound, "unknown route", nil)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
			return
		}
		h.handleCancelJob(w, r, jobID)
	default:
		writeError(w, r, http.StatusNotFound, "unknown route", nil)
	}
}

// handleCancelJob은 URL 단위 취소(?index=N) 또는 배치 전체 취소를 발사한다.
// URL 단위 취소에는 두 경로가 있다:
//
//   - 진행 중: CancelURL이 등록된 ctx를 트리거하고 워커가 이를 표준
//     error("cancelled") 이벤트와 summary 카운터 증분으로 번역한다.
//   - Pending(아직 시작 안 됨): URLState를 cancelled로 설정하고 우리가 직접
//     error 이벤트를 발행한다. 워커 루프는 매 fetch 직전 URLStatus를 검사해
//     이미 terminal인 항목을 건너뛴다.
//
// 이미 terminal인 URL은 409를 반환한다. Job 자체는 여전히 active(queued
// 또는 running)여야 한다 — finished Job에 대한 cancel은 409.
func (h *Handler) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok := h.registry.Get(jobID)
	if !ok {
		writeError(w, r, http.StatusNotFound, "job not found", nil)
		return
	}
	if !job.IsActive() {
		writeError(w, r, http.StatusConflict, "job already finished", nil)
		return
	}

	indexStr := r.URL.Query().Get("index")
	if indexStr == "" {
		// 배치 전체 취소 — 워커가 job.Ctx().Done()을 관측해 cancelled 이벤트와
		// summary를 스스로 발행한다.
		job.Cancel()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	idx, err := strconv.Atoi(indexStr)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid index", err)
		return
	}
	if idx < 0 || idx >= job.URLCount() {
		writeError(w, r, http.StatusBadRequest, "index out of range", nil)
		return
	}

	// 원자적 판단: 등록된 cancel이 우선한다(워커가 error 프레임을 발행).
	// pending이면 mark + 핸들러가 error 프레임을 발행. 그 외 terminal이면 409.
	// 두 검사를 한 번의 잠금에서 함께 수행해, 핸들러 측 두 검사 사이에 워커가
	// RegisterURLCancel을 끼워넣을 수 있던 race 창을 닫는다.
	url, kind := job.CancelOne(idx)
	switch kind {
	case importjob.CancelKindRunning:
		// 워커가 ctx.Done()을 관측해 fetchOneJob의 기존 cancellation 경로로
		// error("cancelled")를 발행한다 — 핸들러는 침묵한다.
		w.WriteHeader(http.StatusNoContent)
	case importjob.CancelKindPending:
		// 워커는 다음 URLStatus 검사에서 이 인덱스를 우회한다. 이 행의
		// 라이프사이클 이벤트는 우리가 소유한다.
		job.Publish(mustEvent("error", sseError{
			Phase: "error", Index: idx, URL: url, Error: "cancelled",
		}))
		w.WriteHeader(http.StatusNoContent)
	default:
		// terminal이거나 알 수 없음 — 취소할 대상이 없다.
		writeError(w, r, http.StatusConflict, "url already finished", nil)
	}
}

// handleDeleteJob은 terminal Job을 레지스트리에서 제거한다. Active Job
// (queued/running)은 먼저 명시적 cancel을 요구한다 — 409. 알 수 없는 id는
// 404. 구독자가 있었다면 SetStatus(terminal) 시점에 이미 분리되었으므로
// 여기서 broadcast가 필요 없다.
func (h *Handler) handleDeleteJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if err := h.registry.Remove(jobID); err != nil {
		switch {
		case errors.Is(err, importjob.ErrJobNotFound):
			writeError(w, r, http.StatusNotFound, "job not found", nil)
		case errors.Is(err, importjob.ErrJobActive):
			writeError(w, r, http.StatusConflict, "job_active", nil)
		default:
			writeError(w, r, http.StatusInternalServerError, "remove failed", err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteFinishedJobs는 모든 terminal Job을 한 번에 제거한다. 필터
// 없는 우연한 DELETE가 조용히 "전부 지우기"로 해석되는 일이 없도록
// `status=finished` 쿼리를 검증한다.
func (h *Handler) handleDeleteFinishedJobs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("status") != "finished" {
		writeError(w, r, http.StatusBadRequest, "missing status=finished filter", nil)
		return
	}
	n := h.registry.RemoveFinished()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]int{"removed": n}); err != nil {
		slog.Error("remove finished encode", "err", err)
	}
}

// handleSubscribeJob은 지정된 Job의 라이프사이클을 SSE로 스트리밍한다 —
// 스냅샷 프레임 한 개, 그 후 Job이 종료될 때까지의 모든 라이브 이벤트.
// Job이 이미 끝난 뒤에 도착한 구독자는 스냅샷만 받는다 — 그 경우
// SubscribeWithSnapshot이 채널을 미리 close하므로 루프가 첫 read에서 즉시
// 빠져나간다.
func (h *Handler) handleSubscribeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok := h.registry.Get(jobID)
	if !ok {
		writeError(w, r, http.StatusNotFound, "job not found", nil)
		return
	}

	flusher := assertFlusher(w, r)
	if flusher == nil {
		return
	}

	// 단일 j.mu 잠금 안에서 원자적 snapshot+subscribe — Snapshot()과
	// Subscribe() 사이에 발행된 이벤트가 스냅샷과 채널 모두에 들어가서 클라이언트
	// 측에서 중복 카운트되는 race 창을 닫는다.
	snapshot, events, unsubscribe := job.SubscribeWithSnapshot()
	defer unsubscribe()

	writeSSEHeaders(w)

	writeSSEEvent(w, flusher, snapshotEnvelope{Phase: "snapshot", Job: snapshot})

	for {
		select {
		case ev, open := <-events:
			if !open {
				// Job이 terminal 상태에 도달했다 — SetStatus가 채널을 닫았다.
				// 스냅샷이 이미 terminal 상태를 반영한다(핸들러는 그 스냅샷을
				// 전달한 뒤 항상 반환한다).
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", ev.Data)
			flusher.Flush()
			if ev.Phase == "summary" {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
