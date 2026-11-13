package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFilesFixedDefaults(t *testing.T) {
	userDir, projectDir := t.TempDir(), t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadFiles(userDir, projectDir); err == nil {
		t.Fatal("missing defaults succeeded")
	}
	userPath := filepath.Join(userDir, "taxion", "config.yaml")
	projectPath := filepath.Join(projectDir, ".taxion", "config.yaml")
	write(userPath, validConfig)
	write(projectPath, "version: 1\nruntime:\n  account: project-account\n")
	layers, err := ReadFiles(userDir, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Resolve(layers)
	if err != nil || result.Config.Runtime.Account != "project-account" ||
		result.Sources["runtime.account"] != "project" {
		t.Fatal("default order incorrect", err)
	}
	if len(layers) != 2 || layers[0].Source != "user" || layers[1].Source != "project" {
		t.Fatal("fixed layers incorrect")
	}
	// Only the supplied directory is searched, even when its parent has config.
	child := filepath.Join(projectDir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	layers, err = ReadFiles(userDir, child)
	if err != nil || len(layers) != 1 || layers[0].Source != "user" {
		t.Fatal("ancestor project configuration was loaded", err)
	}
	if err := os.Remove(projectPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(projectPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(userDir, projectDir); err == nil {
		t.Fatal("default read error silently skipped")
	}
}
