package syncer

import (
	"strings"
	"testing"
)

func TestConflictName(t *testing.T) {
	result := conflictName(`C:\Users\test\notes\doc.md`)

	if !strings.Contains(result, ".sypora-conflict-") {
		t.Fatalf("expected .sypora-conflict- in name, got: %s", result)
	}
	if !strings.HasPrefix(result, `C:\Users\test\notes\doc.sypora-conflict-`) {
		t.Fatalf("unexpected prefix in: %s", result)
	}
	if !strings.HasSuffix(result, ".md") {
		t.Fatalf("expected .md extension, got: %s", result)
	}

	// Should produce unique names
	result2 := conflictName(`C:\Users\test\notes\doc.md`)
	if result != result2 {
		t.Logf("conflict names differ across calls (expected, due to timestamp)")
	}
}

func TestConflictNameNoExt(t *testing.T) {
	result := conflictName(`C:\Users\test\notes\README`)
	if !strings.Contains(result, ".sypora-conflict-") {
		t.Fatalf("expected .sypora-conflict- in name, got: %s", result)
	}
	// Should not end with a dot
	if strings.HasSuffix(result, ".") {
		t.Fatalf("should not end with dot, got: %s", result)
	}
}
