// Package media는 미디어 분류와 파일시스템 안전 연산을 모아둔 공유 leaf
// 패키지다.
//
// 책임은 의도적으로 두 그룹으로 나뉘어 있다:
//
//  1. types.go의 타입·MIME 분류. 의존성이 없어 어디서든 import 가능하다.
//  2. SafePath, MoveFile, MoveDir 같은 파일시스템 연산. 핸들러와 폴더
//     조작에서 사용하며, 패키지가 더 커지면 internal/media/fsop 서브패키지로
//     옮기는 게 자연스럽다.
//
// 파일시스템 쪽이 더 늘어나면 types.go를 internal/media/types로,
// path.go/move.go를 internal/media/fsop으로 분리해 썸네일·URL import
// 코드가 분류 로직에만 의존하게 만드는 걸 우선 고려한다.
package media
