// Package media is the shared leaf package for media classification and
// filesystem-safe media operations.
//
// Its current responsibilities are intentionally split into two groups:
//
//  1. Type and MIME classification in types.go. These helpers are dependency
//     free and may be imported broadly.
//  2. SafePath, MoveFile, and MoveDir filesystem operations. These are used by
//     handlers and folder operations, and may move to an internal/media/fsop
//     subpackage if the package grows.
//
// If the filesystem side expands, prefer splitting types.go into
// internal/media/types and path.go/move.go into internal/media/fsop so thumbnail
// and URL import code only depend on classification.
package media
