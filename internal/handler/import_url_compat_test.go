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
