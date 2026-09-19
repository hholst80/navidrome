package scanner

import (
	"github.com/navidrome/navidrome/utils/scanignore"
	"io/fs"
)

type IgnoreChecker = scanignore.IgnoreChecker

func newIgnoreChecker(fsys fs.FS) *IgnoreChecker { return scanignore.New(fsys) }
