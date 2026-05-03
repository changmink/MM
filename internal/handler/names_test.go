package handler

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// 기존 규칙 — 회귀 보호
		{"빈 문자열", "", true},
		{"점 하나", ".", true},
		{"점 둘", "..", true},
		{"슬래시 포함", "a/b", true},
		{"백슬래시 포함", "a\\b", true},
		{"길이 초과 256", strings.Repeat("a", 256), true},
		{"길이 정확히 255", strings.Repeat("a", 255), false},
		{"평범한 이름", "movie.mp4", false},
		{"한글 이름", "영화.mkv", false},
		{"점 포함", "my.video.mp4", false},

		// Windows 예약 문자
		{"콜론", "a:b", true},
		{"별표", "a*b", true},
		{"파이프", "a|b", true},
		{"물음표", "a?b", true},
		{"꺾쇠 좌", "a<b", true},
		{"꺾쇠 우", "a>b", true},
		{"큰따옴표", `a"b`, true},

		// 제어 문자
		{"NUL", "a\x00b", true},
		{"제어문자 0x1F", "a\x1fb", true},
		{"DEL 0x7F", "a\x7fb", true},
		{"탭", "a\tb", true},
		{"개행", "a\nb", true},
		{"공백 — 정상", "a b", false},

		// Windows 예약 basename (확장자 유무 무관)
		{"CON", "CON", true},
		{"con 소문자", "con", true},
		{"CON.txt", "CON.txt", true},
		{"PRN", "PRN", true},
		{"AUX", "AUX", true},
		{"NUL", "NUL", true},
		{"COM1", "COM1", true},
		{"COM9", "COM9", true},
		{"COM9.log", "COM9.log", true},
		{"LPT1", "LPT1", true},
		{"LPT9", "LPT9", true},

		// 예약 basename이 아닌 경계 케이스
		{"COM 숫자 없음", "COM", false},
		{"COM10 — 두 자리", "COM10", false},
		{"COM0 — 0은 비예약", "COM0", false},
		{"NULL — 4글자라도 비예약", "NULL", false},
		{"CONS — 접미사 다름", "CONS", false},
		{"my-CON.mp4 — basename에 다른 문자", "my-CON.mp4", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateName(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateName(%q) = nil, want error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateName(%q) = %v, want nil", tc.input, err)
			}
			if err != nil && err.Error() != "invalid name" {
				t.Fatalf("validateName(%q) error = %q, want \"invalid name\"", tc.input, err.Error())
			}
		})
	}
}
