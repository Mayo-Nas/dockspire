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

func TestBundleContainsOnlyCurrentBrandAssets(t *testing.T) {
	entries, err := fs.ReadDir(Assets, "brand")
	expected := []string{"dockspire-horizontal-dark.png", "dockspire-icon-256.png"}
	if err != nil || len(entries) != len(expected) {
		t.Fatal("unexpected branding asset inventory", err)
	}
	for index, entry := range entries {
		if entry.IsDir() || entry.Name() != expected[index] {
			t.Fatal("unexpected branding asset")
		}
	}
	icons, err := fs.Glob(Assets, "*.ico")
	if err != nil || len(icons) != 0 {
		t.Fatal("unexpected ICO asset in browser bundle", err)
	}
}
