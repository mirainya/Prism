package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	migrationfiles "github.com/mirainya/Prism/database/migrations"
)

var migrationFilenamePattern = regexp.MustCompile(`^(\d{8}_\d{6})_([a-z0-9][a-z0-9_]*)\.sql$`)

type Migration struct {
	Version  string
	Name     string
	Filename string
	Checksum string
	SQL      string
}

func Load() ([]Migration, error) {
	return loadFromFS(migrationfiles.Files)
}

func loadFromFS(filesystem fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := migrationFilenamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			if entry.Name() > migrationfiles.BaselineVersion {
				return nil, fmt.Errorf("invalid managed migration filename %q", entry.Name())
			}
			continue
		}
		if match[1] < migrationfiles.BaselineVersion {
			continue
		}
		content, err := fs.ReadFile(filesystem, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("migration %s is empty", entry.Name())
		}
		normalized := bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		digest := sha256.Sum256(normalized)
		migrations = append(migrations, Migration{
			Version:  match[1],
			Name:     match[2],
			Filename: filepath.ToSlash(entry.Name()),
			Checksum: hex.EncodeToString(digest[:]),
			SQL:      string(content),
		})
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	if len(migrations) == 0 || migrations[0].Version != migrationfiles.BaselineVersion {
		return nil, fmt.Errorf("managed baseline migration %s is missing", migrationfiles.BaselineVersion)
	}
	for index := 1; index < len(migrations); index++ {
		if migrations[index-1].Version == migrations[index].Version {
			return nil, fmt.Errorf("duplicate migration version %s", migrations[index].Version)
		}
	}
	return migrations, nil
}
