// Transitional shim — 본 파일은 import 서브패키지 분리(2회차 B.1, 5e1cf7d)
// 시 부모 패키지의 기존 테스트(import_url_test.go)가 unprefixed 이름으로
// 내부 헬퍼를 그대로 호출할 수 있게 두기 위한 호환층이다. 테스트 자체가
// 서브패키지로 이전되면(FU3-I-2-B) 본 파일과 import/common.go의 export
// wrapper도 함께 제거된다. 자세한 의도는
// tasks/handoff-team-review-3-followup.md FU3-I-2 참조.
package handler

import (
	handlerimport "file_server/internal/handler/import"
	"file_server/internal/importjob"
)

const maxImportURLLength = 2048

type sseStart struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	URL   string `json:"url"`
	Name  string `json:"name"`
	Total int64  `json:"total,omitempty"`
	Type  string `json:"type"`
}

func recoverImportJob(rec any, job *importjob.Job) {
	handlerimport.RecoverImportJob(rec, job)
}

func summarizeURLs(urls []importjob.URLState) importjob.Summary {
	return handlerimport.SummarizeURLs(urls)
}

func summaryEvent(s importjob.Summary) importjob.Event {
	return handlerimport.SummaryEvent(s)
}

func normalizeURLs(in []string) []string {
	return handlerimport.NormalizeURLs(in)
}

func redactURL(raw string) string {
	return handlerimport.RedactURL(raw)
}
