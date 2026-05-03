package importurl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"file_server/internal/importjob"
	"file_server/internal/media"
	"file_server/internal/settings"
	"file_server/internal/urlfetch"
)

const (
	maxImportURLs = 500
	// maxImportURLLength는 개별 URL 문자열의 상한이다. 임의로 큰 blob을
	// 레지스트리에 강제로 밀어넣어 GET /api/import-url/jobs로 그대로 노출
	// 되는 것을 막는다. 2 KB는 실제 정상 signed URL 길이(S3, Cloudfront, Mux의
	// 최악 ~1.5 KB)보다 충분히 크다.
	maxImportURLLength = 2048
	// progressChanBuffer는 broadcast가 뒤처졌을 때 Fetch 고루틴이 블록하지
	// 않고 샘플을 떨어뜨릴 수 있게 해준다. 느린 구독자가 다운로드를 멈춰
	// 세워서는 안 된다.
	progressChanBuffer = 16
	// importURLWorkers는 한 URL import 배치 안에서 동시에 받을 URL의 상한.
	// HLS와 달리 origin이 동일하다는 보장이 없어 보수적으로 2. process-wide
	// importSem(=1)이 배치 간 직렬화를 처리하므로 여기는 batch 내부만 책임진다.
	// 변경 시 TestImportURL_BatchFetchesInParallel의 상한 검증도 함께 갱신할 것.
	importURLWorkers = 2
)

type importRequest struct {
	URLs []string `json:"urls"`
}

// sseRegister는 POST 응답의 첫 프레임이다. 클라이언트에게 jobId를 넘겨,
// 새로 고침 시 GET /jobs/{id}/events로 재구독할 수 있게 한다. Job.Publish가
// 아니라 요청 writer로 직접 쓴다 — register는 요청별 메타데이터지 Job 상태가
// 아니므로 다른 구독자에게 보일 필요가 없다.
type sseRegister struct {
	Phase string `json:"phase"` // always "register"
	JobID string `json:"jobId"`
}

type sseStart struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	URL   string `json:"url"`
	Name  string `json:"name"`
	// Total은 origin이 광고한 Content-Length다. HLS는 총 바이트 개수가 없으므로
	// (스트리밍 세그먼트의 가변 비트레이트 remux) 0으로 도착하고 omitempty로
	// wire에서 빠진다 — 클라이언트는 이 부재를 보고 진행률을 indeterminate
	// progress bar로 렌더링한다.
	Total int64  `json:"total,omitempty"`
	Type  string `json:"type"`
}

type sseProgress struct {
	Phase    string `json:"phase"`
	Index    int    `json:"index"`
	Received int64  `json:"received"`
}

type sseDone struct {
	Phase    string   `json:"phase"`
	Index    int      `json:"index"`
	URL      string   `json:"url"`
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Type     string   `json:"type"`
	Warnings []string `json:"warnings"`
}

type sseError struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type sseSummary struct {
	Phase     string `json:"phase"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Cancelled int    `json:"cancelled,omitempty"`
}

// sseQueued는 Job의 이벤트 스트림에 가장 먼저 발행되는 이벤트다 — 핸들러가
// 프로세스 단위 import 세마포어를 획득하기 전에 발사된다. 다른 배치가 진행
// 중이 아니면 뒤이은 세마포어 획득이 즉시 반환되고 `start`가 따라붙어 UI는
// queued 상태를 렌더링할 일이 없다. 다른 배치가 세마포어를 잡고 있으면
// 클라이언트는 멈춘 progress bar 대신 "대기 중"을 표시할 명확한 신호를 갖는다.
type sseQueued struct {
	Phase string `json:"phase"` // always "queued"
}

func (h *Handler) HandleImportURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	rel := r.URL.Query().Get("path")
	destAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	fi, err := os.Stat(destAbs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "path not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}
	if !fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		return
	}

	var body importRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	urls := normalizeURLs(body.URLs)
	if len(urls) == 0 {
		writeError(w, r, http.StatusBadRequest, "no urls", nil)
		return
	}
	if len(urls) > maxImportURLs {
		writeError(w, r, http.StatusBadRequest, "too many urls", nil)
		return
	}

	flusher := assertFlusher(w, r)
	if flusher == nil {
		return
	}

	job, err := h.registry.Create(rel, urls)
	if err != nil {
		if errors.Is(err, importjob.ErrTooManyJobs) {
			writeError(w, r, http.StatusTooManyRequests, "too_many_jobs", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "registry create failed", err)
		return
	}

	// 워커를 띄우기 전에 Subscribe해 워커가 즉시 발행할 초기 queued 이벤트를
	// 핸들러가 놓치지 않게 한다.
	events, unsubscribe := job.Subscribe()
	defer unsubscribe()

	// 요청 도착 시점(queue 진입 전)에 settings를 스냅샷한다. 이 배치가
	// 세마포어를 기다리는 동안 PATCH가 들어와도 결국 실행될 때의 cap/timeout이
	// 바뀌지 않게 한다.
	snap := h.settingsSnapshot()

	// 워커는 백그라운드에서 실제 import를 구동한다. 요청 컨텍스트가 아닌
	// job.Ctx()(서버 수명, shutdown이나 사용자 Cancel로 취소 가능)를 쓴다 —
	// 그래야 클라이언트가 탭을 닫거나 새로 고침해도 다운로드가 계속된다.
	go h.runImportJob(job, snap, destAbs)

	writeSSEHeaders(w)

	// jobId를 즉시 클라이언트에 넘긴다 — 새로 고침 시
	// GET /api/import-url/jobs/{id}/events(J4에 추가됨)로 이 Job에 다시 바인딩할 수 있다.
	writeSSEEvent(w, flusher, sseRegister{Phase: "register", JobID: job.ID})

	// Job의 이벤트를 SSE 스트림으로 펌프한다. summary 프레임이 Job의 마지막
	// 라이브 이벤트이므로, flush가 끝나는 즉시 워커와의 조율 없이 핸들러가
	// 반환할 수 있다. 클라이언트가 끊으면 r.Context()로 short-circuit 되지만,
	// Job 자체는 절대 취소하지 않는다.
	for {
		select {
		case ev, ok := <-events:
			if !ok {
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

// runImportJob은 import 배치 하나를 구동하는 워커 고루틴이다. URLState
// 변형과 SSE 이벤트 발행을 소유한다. HTTP 핸들러는 (잠재적으로) 다수
// 구독자 중 하나일 뿐이며 Job 상태를 직접 건드리지 않는다.
//
// defer recover는 urlfetch / ffmpeg 헬퍼에서 panic이 나도 Job이 terminal
// 상태에 도달함을 보장한다 — 이게 없으면 고루틴이 조용히 죽어 Done()이
// 닫히지 않고, MaxQueuedJobs 슬롯이 영구 점유되며, shutdown 시
// Handler.Close가 WaitAll 데드라인을 가득 채워 멈춘다.
func (h *Handler) runImportJob(job *importjob.Job, snap settings.Settings, destAbs string) {
	defer func() { recoverImportJob(recover(), job) }()

	maxBytes := snap.URLImportMaxBytes
	perURLTimeout := time.Duration(snap.URLImportTimeoutSeconds) * time.Second

	// 이 배치가 queued 상태임을 알린다. 세마포어가 비어 있어도 이벤트는
	// 그대로 발사되며, `start`까지의 간격이 더 짧을 뿐이다.
	job.Publish(mustEvent("queued", sseQueued{Phase: "queued"}))

	// 프로세스 단위 배치 세마포어를 획득한다. importSem은 job.Ctx()와 짝을
	// 이뤄 graceful-shutdown 취소가 대기를 풀어준다 — 요청 컨텍스트는 의도적으로
	// 쓰지 않아, 브라우저 탭을 닫아도 큐 위치를 잃지 않는다.
	select {
	case h.importSem <- struct{}{}:
		defer func() { <-h.importSem }()
	case <-job.Ctx().Done():
		// 세마포어 획득 전에 취소됨 — 모든 URL을 cancelled로 보고하고
		// Start/Done은 하나도 발행되지 않은 상태다.
		urls := job.Snapshot().URLs
		cancelled := cancelRemainingURLs(job, urls, 0)
		finalizeBatch(job, 0, 0, cancelled)
		return
	}

	job.SetStatus(importjob.StatusRunning)

	rel := job.DestPath
	urls := job.Snapshot().URLs
	h.runImportURLWorkers(job, urls, destAbs, rel, maxBytes, perURLTimeout)

	sum := summarizeURLs(job.Snapshot().URLs)
	finalizeBatch(job, sum.Succeeded, sum.Failed, sum.Cancelled)
}

type importURLTask struct {
	index int
	url   string
}

func (h *Handler) runImportURLWorkers(job *importjob.Job, urls []importjob.URLState, destAbs, rel string,
	maxBytes int64, perURLTimeout time.Duration) {

	workerCount := importURLWorkers
	if len(urls) < workerCount {
		workerCount = len(urls)
	}
	if workerCount == 0 {
		return
	}

	tasks := make(chan importURLTask)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				// 두 종류의 fetch 직전 가드: 배치 취소(job.Ctx)와 URL 단위
				// 취소(URLStatus). fetchOneJob의 RegisterURLCancel이 j.mu
				// 아래에서 status를 다시 검사하므로 여기는 lock-protected
				// 재검사 전의 가벼운 fast-path다 — race가 있어도 lock 보호된
				// 검사가 권위 있는 결정이기 때문에 double-emit이 발생하지 않는다.
				if job.Ctx().Err() != nil {
					continue
				}
				// 워커가 task를 받기 전에 URL 단위 취소가 이 인덱스를
				// "cancelled"로 뒤집었을 수 있다. fetch를 건너뛴다 — 그 pending
				// URL의 SSE error 이벤트는 cancel 핸들러가 소유한다.
				if job.URLStatus(task.index) == "cancelled" {
					continue
				}
				_ = h.fetchOneJob(job, task.index, task.url, destAbs, rel, maxBytes, perURLTimeout)
			}
		}()
	}

dispatch:
	for i, urlState := range urls {
		if job.URLStatus(i) == "cancelled" {
			continue
		}
		select {
		case <-job.Ctx().Done():
			break dispatch
		case tasks <- importURLTask{index: i, url: urlState.URL}:
		}
	}
	close(tasks)
	wg.Wait()

	if job.Ctx().Err() != nil {
		cancelPendingURLs(job, urls)
	}
}

// cancelRemainingURLs는 [fromIdx, len(urls)) 범위의 URL을 cancelled로 표시
// 하고 해당 error 이벤트를 발행한다. 이 호출에서 취소된 개수를 반환한다.
// 이전의 URL 단위 CancelOne 핸들러가 이미 "cancelled"로 뒤집은 URL은
// 카운트는 하되 이벤트를 재발행하지 않는다 — 그 발행은 CancelKindPending에서
// 핸들러가 소유하고 있으며, 중복은 같은 인덱스에 대해 동일한 error
// ("cancelled")를 두 번 전달한다.
func cancelRemainingURLs(job *importjob.Job, urls []importjob.URLState, fromIdx int) int {
	cancelled := 0
	for j := fromIdx; j < len(urls); j++ {
		if job.URLStatus(j) == "cancelled" {
			cancelled++
			continue
		}
		u := urls[j].URL
		job.UpdateURL(j, func(s *importjob.URLState) {
			s.Status = "cancelled"
			s.Error = "cancelled"
		})
		job.Publish(mustEvent("error", sseError{
			Phase: "error", Index: j, URL: u, Error: "cancelled",
		}))
		cancelled++
	}
	return cancelled
}

func cancelPendingURLs(job *importjob.Job, urls []importjob.URLState) {
	for j, u := range urls {
		if job.URLStatus(j) != "pending" {
			continue
		}
		job.UpdateURL(j, func(s *importjob.URLState) {
			s.Status = "cancelled"
			s.Error = "cancelled"
		})
		job.Publish(mustEvent("error", sseError{
			Phase: "error", Index: j, URL: u.URL, Error: "cancelled",
		}))
	}
}

// finalizeBatch는 summary 프레임을 발행하고 Job에 기록한 뒤 terminal 상태를
// 설정한다. 우선순위: 성공이 하나라도 있으면 Completed, 그렇지 않고 취소가
// 하나라도 있으면 Cancelled, 그 외엔 Failed. terminal 전이의 단일 출처라
// 순서(SetSummary → Publish → SetStatus)가 어긋나지 않는다 — SetStatus가
// 구독자 채널을 close하므로 summary는 반드시 먼저 Publish 해야 한다.
// 그렇지 않으면 라이브 클라이언트가 마지막 프레임을 race로 놓친다.
func finalizeBatch(job *importjob.Job, succeeded, failed, cancelled int) {
	summary := importjob.Summary{
		Succeeded: succeeded, Failed: failed, Cancelled: cancelled,
	}
	job.SetSummary(summary)
	job.Publish(summaryEvent(summary))
	switch {
	case succeeded > 0:
		job.SetStatus(importjob.StatusCompleted)
	case cancelled > 0:
		job.SetStatus(importjob.StatusCancelled)
	default:
		job.SetStatus(importjob.StatusFailed)
	}
}

type fetchResult int

const (
	fetchFailed fetchResult = iota
	fetchSucceeded
	fetchCancelled
)

// fetchOneJob은 urlfetch.Fetch를 통해 URL 하나를 다운로드하면서 모든 SSE
// 이벤트를 발행한다 — start 최대 1번, progress 0회 이상, terminal
// (done 또는 error) 정확히 1번. 동시에 Job의 해당 URLState도 갱신해 스냅샷
// 재생과 라이브 이벤트 스트림이 동기화되도록 한다.
func (h *Handler) fetchOneJob(job *importjob.Job, index int, u, destAbs, relDir string,
	maxBytes int64, perURLTimeout time.Duration) fetchResult {

	// URL 단위 컨텍스트 — Job에 등록해, J5에 추가된 cancel API가 배치 전체를
	// 중단하지 않고 특정 URL만 노릴 수 있게 한다.
	urlCtx, cancelURL := context.WithCancel(job.Ctx())
	defer cancelURL()
	job.RegisterURLCancel(index, cancelURL)
	defer job.UnregisterURLCancel(index)

	// race-close: runImportJob 루프의 URLStatus 검사와 위의 RegisterURLCancel
	// 사이에 CancelOne 호출이 이 URL을 "cancelled"(CancelKindPending)로
	// 뒤집었을 수 있다. j.mu 아래의 CancelOne은 이 시점 이후에야 우리의
	// 등록 항목을 볼 수 있으므로, 여기서 URLStatus(역시 j.mu 아래)로 status를
	// 다시 검사하면 보장된다 — 우리가 지금 취소를 보고 fetch를 건너뛰거나,
	// CancelOne이 우리 항목을 보고 CancelKindRunning 경로로 가서 urlCtx를
	// 트리거하거나. 어느 쪽이든 URL은 정확히 한 번의 terminal 회계만 받는다.
	if job.URLStatus(index) == "cancelled" {
		return fetchCancelled
	}

	progressCh := make(chan int64, progressChanBuffer)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for received := range progressCh {
			job.UpdateURL(index, func(s *importjob.URLState) { s.Received = received })
			job.Publish(mustEvent("progress", sseProgress{
				Phase: "progress", Index: index, Received: received,
			}))
		}
	}()

	cb := &urlfetch.Callbacks{
		Start: func(name string, total int64, fileType string) {
			job.UpdateURL(index, func(s *importjob.URLState) {
				s.Name = name
				s.Type = fileType
				s.Total = total
				s.Status = "running"
			})
			job.Publish(mustEvent("start", sseStart{
				Phase: "start", Index: index, URL: u,
				Name: name, Total: total, Type: fileType,
			}))
		},
		Progress: func(received int64) {
			select {
			case progressCh <- received:
			default:
				// drop — 느린 구독자가 io.Copy를 멈춰 세워서는 안 된다.
			}
		},
	}

	fctx, cancelTimeout := context.WithTimeout(urlCtx, perURLTimeout)
	res, ferr := urlfetch.Fetch(fctx, h.urlClient, u, destAbs, relDir, maxBytes, cb)
	cancelTimeout()

	close(progressCh)
	<-writerDone

	if ferr != nil {
		// URL 단위/배치 취소를 진짜 fetch 실패와 구분해, 워커의
		// success/fail/cancelled 카운터와 terminal 상태가 올바르게 유지되게 한다.
		if isCancelled(urlCtx, job.Ctx(), ferr) {
			job.UpdateURL(index, func(s *importjob.URLState) {
				s.Status = "cancelled"
				s.Error = "cancelled"
			})
			job.Publish(mustEvent("error", sseError{
				Phase: "error", Index: index, URL: u, Error: "cancelled",
			}))
			return fetchCancelled
		}
		logFetchError(u, ferr)
		job.UpdateURL(index, func(s *importjob.URLState) {
			s.Status = "error"
			s.Error = ferr.Code
		})
		job.Publish(mustEvent("error", sseError{
			Phase: "error", Index: index, URL: u, Error: ferr.Code,
		}))
		return fetchFailed
	}

	job.UpdateURL(index, func(s *importjob.URLState) {
		s.Status = "done"
		s.Name = res.Name
		s.Type = res.Type
		s.Received = res.Size
		s.Warnings = append([]string(nil), res.Warnings...)
	})
	job.Publish(mustEvent("done", sseDone{
		Phase: "done", Index: index, URL: u,
		Path: res.Path, Name: res.Name, Size: res.Size,
		Type: res.Type, Warnings: res.Warnings,
	}))

	if res.Type != string(media.TypeAudio) {
		thumbDir := filepath.Join(destAbs, ".thumb")
		thumbPath := filepath.Join(thumbDir, res.Name+".jpg")
		finalSrc := filepath.Join(destAbs, res.Name)
		if !h.thumbPool.Submit(finalSrc, thumbPath) {
			slog.Warn("thumb pool full, deferring to lazy generation", "src", finalSrc)
		}
	}
	return fetchSucceeded
}

// isCancelled는 urlfetch 에러가 진짜 origin/IO 실패가 아닌 컨텍스트 취소
// (URL 단위 또는 배치 전체)에서 비롯된 것인지 보고한다. URL을 failed로 셀지
// cancelled로 셀지 결정할 때 쓴다.
func isCancelled(urlCtx, jobCtx context.Context, ferr *urlfetch.FetchError) bool {
	if jobCtx.Err() != nil || urlCtx.Err() != nil {
		return true
	}
	if ferr == nil {
		return false
	}
	if u := ferr.Unwrap(); u != nil {
		return errors.Is(u, context.Canceled) || errors.Is(u, context.DeadlineExceeded)
	}
	return false
}

// logFetchError는 실패한 URL import에 대해 구조화된 서버 측 로그를 남긴다.
// 클라이언트는 불투명 에러 코드만 받기 때문에, 운영자는 여기서 실제로
// 무엇이 깨졌는지 본다 — ffmpeg_missing(운영자가 ffmpeg를 설치해야 함)과
// ffmpeg_error(스트림별 stderr를 들여다봐 DRM/포맷 문제를 구분)에 특히
// 유용하다. URL은 로깅 전에 redact한다 — 사용자가 입력한 origin에는 흔히
// 서명된 query 파라미터·자격(`?token=`, `user:pass@host`)이 포함되며, 이는
// journald나 붙여넣어진 로그 조각에 남아서는 안 된다.
func logFetchError(u string, ferr *urlfetch.FetchError) {
	attrs := []any{"code", ferr.Code, "url", redactURL(u)}
	if unwrapped := ferr.Unwrap(); unwrapped != nil {
		attrs = append(attrs, "err", redactErr(unwrapped))
	}
	slog.Warn("url import failed", attrs...)
}

// sensitiveQueryKeys는 URL redactor가 로깅 전에 값을 제거할 query 파라미터
// 목록이다. 매칭은 대소문자 무시다(lookup이 키를 소문자로 만들므로 여기
// 항목은 반드시 소문자여야 한다). 의도적으로 좁게 둔다 — 폭넓게 redact
// 하면 정상 origin의 유용한 진단 정보를 가린다. URL query 키는 정규화되지
// 않으므로 하이픈과 snake_case 변형을 별도로 나열한다.
var sensitiveQueryKeys = map[string]struct{}{
	// 일반 auth 토큰.
	"token":        {},
	"access_token": {},
	"auth":         {},
	// signed-URL 서명.
	"signature":       {},
	"sig":             {},
	"x-amz-signature": {},
	"signed_url":      {},
	"presigned_url":   {},
	// API 키.
	"key":     {},
	"apikey":  {},
	"api_key": {},
	// 일반 자격 정보.
	"password": {},
	"secret":   {},
}

// redactURL은 userinfo를 제거하고, 자격·서명처럼 보이는 query 파라미터의
// 값을 마스킹한다. url.Parse가 실패하는 입력에는 "<unparseable>"을 반환해,
// 잘못된 문자열이 로그 라인의 나머지를 short-circuit 시키지 않게 한다.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	if u.User != nil {
		u.User = nil
	}
	q := u.Query()
	changed := false
	for k := range q {
		if _, ok := sensitiveQueryKeys[strings.ToLower(k)]; ok {
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// redactErr는 *url.Error 래퍼에 redactURL을 적용해, net/http가 에러 문자열에
// 박아둔 URL도 함께 마스킹한다. 그 외의 에러는 변경 없이 err.Error()로
// 흘려 보낸다.
func redactErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		copy := *ue
		copy.URL = redactURL(ue.URL)
		return copy.Error()
	}
	return err.Error()
}

// recoverImportJob은 워커의 defer 본체다 — panic 복구 불변식을 단위 테스트
// 할 수 있도록 분리했다. SetStatus가 이미 Job을 terminal 상태로 몰아갔으면
// 발행을 건너뛴다(늦은 defer panic이 Completed/Cancelled를 Failed로 뒤집어선
// 안 된다). SetSummary가 이미 summary를 기록했으면 재발행을 건너뛴다 —
// 이전 Publish(summary)가 성공했고, 중복은 같은 클라이언트에게 summary
// 프레임 두 개를 전달한다.
func recoverImportJob(rec any, job *importjob.Job) {
	if rec == nil {
		return
	}
	slog.Error("import worker panic",
		"jobId", job.ID, "panic", rec, "stack", string(debug.Stack()))
	if job.Status().IsTerminal() {
		return
	}
	snap := job.Snapshot()
	if snap.Summary == nil {
		sum := summarizeURLs(snap.URLs)
		job.SetSummary(sum)
		job.Publish(summaryEvent(sum))
	}
	// SetStatus(terminal)이 구독자 채널을 close한다 — summary를 놓친
	// 클라이언트도 ok=false를 관측하고 깔끔하게 종료된다.
	job.SetStatus(importjob.StatusFailed)
}

// summarizeURLs는 URLState 엔트리를 terminal status에 따라 Summary로
// 접는다. pending/running 상태인 URL은 Failed로 들어간다 — 워커가 어느
// 쪽으로도 결정짓기 전에 중단된 panic-recovery 경로에서 사용된다.
func summarizeURLs(urls []importjob.URLState) importjob.Summary {
	var sum importjob.Summary
	for _, u := range urls {
		switch u.Status {
		case "done":
			sum.Succeeded++
		case "cancelled":
			sum.Cancelled++
		default:
			sum.Failed++
		}
	}
	return sum
}

// summaryEvent는 SSE summary 프레임을 만든다. "summary" phase 문자열을
// 한 곳에 모아 wire-format 상수의 단일 출처로 둔다.
func summaryEvent(s importjob.Summary) importjob.Event {
	return mustEvent("summary", sseSummary{
		Phase:     "summary",
		Succeeded: s.Succeeded,
		Failed:    s.Failed,
		Cancelled: s.Cancelled,
	})
}

// mustEvent는 payload를 주어진 phase의 importjob.Event로 마샬한다. 이런
// 안정 구조체의 JSON 마샬링은 실제로 실패하지 않는다. 만에 하나 실패하면
// broadcast를 건너뛰고(한 번 로깅) 워커는 진행을 계속한다.
func mustEvent(phase string, payload any) importjob.Event {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("import job event marshal", "phase", phase, "err", err)
		return importjob.Event{Phase: phase, Data: []byte("{}")}
	}
	return importjob.Event{Phase: phase, Data: data}
}

// normalizeURLs는 공백을 제거하고, 빈 엔트리를 버리고, maxImportURLLength를
// 넘는 URL을 폐기한다. 순서와 의도된 중복은 보존한다(충돌은 downstream에서
// _N 접미사로 처리된다). 과도하게 긴 엔트리는 조용히 버린다. 요청은 남은
// URL로 진행되며, downstream 카운트 검사(maxImportURLs)가 모든 URL이 거부된
// 배치를 잡아낸다.
func normalizeURLs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		u = strings.TrimSpace(u)
		if u == "" || len(u) > maxImportURLLength {
			continue
		}
		out = append(out, u)
	}
	return out
}
