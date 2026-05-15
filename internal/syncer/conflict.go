package syncer

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// conflictName generates a conflict file name:
// "notes/doc.md" -> "notes/doc.sypora-conflict-20260115-143022.md"
func conflictName(remoteKey string) string {
	dir := filepath.Dir(remoteKey)
	base := filepath.Base(remoteKey)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	ts := time.Now().Format("20060102-150405")
	conflictBase := fmt.Sprintf("%s.sypora-conflict-%s%s", name, ts, ext)
	return filepath.Join(dir, conflictBase)
}
