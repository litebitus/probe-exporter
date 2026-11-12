package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheAssetsAreEveryMinorAndEachMajorAsItsNewestMinor(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "mock-assets")
	if err := writeAssets([]string{folder}); err != nil {
		t.Fatal(err)
	}
	minor, err := os.ReadFile(filepath.Join(folder, "probe-exporter-v1.0.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	major, err := os.ReadFile(filepath.Join(folder, "probe-exporter-v1.schema.json"))
	if err != nil || string(major) != string(minor) {
		t.Errorf("the major's asset is not its newest minor (%v)", err)
	}
}

func TestTheAssetsNeedOneFolderTheyCanBeWrittenTo(t *testing.T) {
	if err := writeAssets(nil); err == nil || err.Error() != "usage: schemaassets <folder>" {
		t.Errorf("err = %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "mock-file")
	os.WriteFile(blocked, nil, 0o600)
	if err := writeAssets([]string{blocked}); err == nil {
		t.Error("the assets were written under a file")
	}
	readOnly := filepath.Join(t.TempDir(), "mock-read-only")
	os.Mkdir(readOnly, 0o500)
	if err := writeAssets([]string{readOnly}); err == nil {
		t.Error("the assets were written to a read-only folder")
	}
}
