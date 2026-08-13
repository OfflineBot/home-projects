package capability_test

// The deletion test from section 11 of the brief, as far as a test can carry
// it: the core knows no capability names.
//
// If this fails, someone put a special case for one capability into the core,
// and deleting that capability's folder would stop being possible.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreDirs are the packages that must stay free of capability names.
var coreDirs = []string{
	"../api", "../auth", "../access", "../files", "../store", "../model",
	"../gitsrv", "../workspace", "../scheduler", "../variables", "../accounts",
	"../capability", "../httpx", "../db", "../events", "../config", "../slug",
	"../projectyaml", "../secret",
}

// names are the capabilities that exist today. The core may not mention them.
var names = []string{
	`"calendar"`, `"markdown"`, `"grades"`, `"site"`, `"mail"`, `"feed"`, `"automation"`,
	`"moodle"`,
}

func TestCoreDoesNotNameCapabilities(t *testing.T) {
	for _, dir := range coreDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // the folder may simply not exist any more
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for lineNo, line := range strings.Split(string(body), "\n") {
				code, _, _ := strings.Cut(line, "//") // comments may name them
				for _, name := range names {
					if strings.Contains(code, name) {
						t.Errorf("%s:%d names a capability (%s) — the core has to ask "+
							"whether a project *has* a capability, never which one it is:\n\t%s",
							path, lineNo+1, name, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}

// A capability's files must not reach into another capability's package.
func TestCapabilitiesDoNotImportEachOther(t *testing.T) {
	root := "../capabilities"
	folders, err := os.ReadDir(root)
	if err != nil {
		t.Skip("no capabilities folder")
	}
	for _, folder := range folders {
		if !folder.IsDir() || folder.Name() == "all" {
			continue
		}
		err := filepath.Walk(filepath.Join(root, folder.Name()), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, other := range folders {
				if !other.IsDir() || other.Name() == folder.Name() || other.Name() == "all" {
					continue
				}
				needle := "internal/capabilities/" + other.Name() + `"`
				if strings.Contains(string(body), needle) {
					t.Errorf("%s imports the %s capability — capabilities talk through files and events, not directly",
						path, other.Name())
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
