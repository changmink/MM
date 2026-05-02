package handler

import (
	"errors"
	"net/http"
)

// 요청 본문 크기 상한. mutating 핸들러가 무한 streaming/메모리 적재로 디스크나
// RAM을 채우지 않도록 진입부에서 http.MaxBytesReader로 감싼다.

// maxUploadBytes는 multipart 업로드 한 건의 절대 상한. 단일 사용자 LAN
// 환경에서 정상 미디어 한 개의 한계 — 이보다 큰 파일은 거의 항상 stuck
// 또는 악의 클라이언트. settings로 노출하는 것은 추후 phase에서 검토
// (현재 SPEC §2.7은 URL import 캡만 사용자에게 노출).
//
// var인 이유: 테스트가 100 GiB짜리 multipart를 만들 수 없으므로 한도를
// 임시로 낮춰서 trip 동작을 검증할 수 있도록.
var maxUploadBytes int64 = 100 << 30 // 100 GiB

// maxJSONBodyBytes는 PATCH /api/file·/api/folder의 JSON body 상한. 정상
// 페이로드는 수백 바이트 — 64 KiB는 path 1024자가 와도 여유.
var maxJSONBodyBytes int64 = 64 << 10 // 64 KiB

// isMaxBytesErr는 http.MaxBytesReader가 cap을 초과해 던진 에러인지 식별한다.
// io.Copy/multipart.Reader가 wrap한 형태로도 흘러오므로 errors.As로 풀어 본다.
func isMaxBytesErr(err error) bool {
	var me *http.MaxBytesError
	return errors.As(err, &me)
}
