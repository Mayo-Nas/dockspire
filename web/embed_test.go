package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestBundleDoesNotExposeGoSource(t *testing.T) {
	err := fs.WalkDir(Assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".go") {
			t.Errorf("Go source was embedded in the public asset bundle: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
