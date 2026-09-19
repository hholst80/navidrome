package cue

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ResolveSourceName prefers the exact spelling, then a unique case-insensitive match.
func ResolveSourceName(name string, files map[string]fs.DirEntry) (string, error) {
	if _, ok := files[name]; ok {
		return name, nil
	}
	var matched string
	for candidate := range files {
		if strings.EqualFold(candidate, name) {
			if matched != "" {
				return "", fmt.Errorf("CUE FILE %q matches multiple audio files ignoring case", name)
			}
			matched = candidate
		}
	}
	return matched, nil
}

// Sidecar identifies the selected external sheet and its original FILE spelling.
type Sidecar struct {
	Name       string
	SourceName string
}

// OriginalSidecar uses the scanner's source matching and basename preference.
// Embedded-only images have no external original. Candidate failures are skipped
// just as during scanning; directory-level failures are returned to the caller.
func OriginalSidecar(source string, followSymlinks bool) (*Sidecar, error) {
	dir, base := filepath.Dir(source), filepath.Base(source)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := map[string]fs.DirEntry{}
	sheets := []string{}
	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(dir, name)
		resolvedName := name
		if entry.Type()&fs.ModeSymlink != 0 {
			if !followSymlinks {
				continue
			}
			resolved, err := filepath.EvalSymlinks(full)
			if err != nil {
				continue
			}
			resolvedName = filepath.Base(resolved)
		}
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files[name] = entry
		if strings.EqualFold(filepath.Ext(resolvedName), ".cue") {
			sheets = append(sheets, name)
		}
	}
	preferred := strings.TrimSuffix(base, filepath.Ext(base)) + ".cue"
	var chosen *Sidecar
	for _, name := range sheets {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sheet, err := ReadCue(f)
		_ = f.Close()
		if err != nil || len(sheet.File) != 1 || strings.ContainsAny(sheet.File[0].FileName, "/\\") {
			continue
		}
		ref := sheet.File[0].FileName
		resolved, err := ResolveSourceName(ref, files)
		if err != nil || resolved != base {
			continue
		}
		if chosen == nil || name == preferred {
			chosen = &Sidecar{Name: name, SourceName: ref}
		}
	}
	return chosen, nil
}
