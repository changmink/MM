// Transitional shim — convert 서브패키지 분리(2회차 B.1, 5e1cf7d) 시
// 부모 패키지의 기존 테스트가 sub-package 상수를 unprefixed 이름으로 참조할
// 수 있게 두기 위한 호환층이다. 테스트가 서브패키지로 이전되면 본 파일은
// 제거된다. tasks/handoff-team-review-3-followup.md FU3-I-2 참조.
package handler

const maxConvertWebPPaths = 500
