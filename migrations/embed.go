// Package migrations exposes the migrations required by this application build.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

func Versions() ([]int64, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, len(names))
	seen := make(map[int64]bool)
	for _, name := range names {
		prefix, _, ok := strings.Cut(name, "_")
		version, err := strconv.ParseInt(prefix, 10, 64)
		if !ok || err != nil || version <= 0 || seen[version] {
			return nil, fmt.Errorf("invalid or duplicate migration version in %s", name)
		}
		seen[version] = true
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no migrations are bundled")
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}
