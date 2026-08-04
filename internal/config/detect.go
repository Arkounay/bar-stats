package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Candidate is a possible replay folder found on this machine.
type Candidate struct {
	Path string `json:"path"`
	// Label names the install the folder belongs to, for the setup screen.
	Label string `json:"label"`
	// DemoCount is how many replay files were found there. The setup screen
	// sorts by it so the folder the user actually plays from comes first.
	DemoCount int `json:"demoCount"`
}

// demoExt is the extension of a compressed replay.
const demoExt = ".sdfz"

// Detect searches the well-known Beyond All Reason install locations and
// returns every folder that actually contains replays, most populated first.
//
// The list is a suggestion for the setup screen, never applied automatically:
// several installs can coexist (standalone, Steam, an old portable copy) and
// only the user knows which one they play.
func Detect() []Candidate {
	seen := map[string]bool{}
	var out []Candidate

	add := func(dir, label string) {
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		key := strings.ToLower(abs)
		if seen[key] {
			return
		}
		seen[key] = true
		n := countDemos(abs)
		if n == 0 {
			return
		}
		out = append(out, Candidate{Path: abs, Label: label, DemoCount: n})
	}

	for _, root := range installRoots() {
		// A BAR install keeps replays under <root>/data/demos; a bare data
		// directory keeps them under <root>/demos.
		add(filepath.Join(root.path, "data", "demos"), root.label)
		add(filepath.Join(root.path, "demos"), root.label)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].DemoCount > out[j].DemoCount })
	return out
}

type installRoot struct {
	path  string
	label string
}

// installRoots lists the directories a BAR install is plausibly rooted at.
func installRoots() []installRoot {
	var roots []installRoot
	add := func(p, label string) {
		if p != "" {
			roots = append(roots, installRoot{path: p, label: label})
		}
	}

	home, _ := os.UserHomeDir()

	if runtime.GOOS == "windows" {
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		localAppData := os.Getenv("LOCALAPPDATA")
		appData := os.Getenv("APPDATA")

		add(filepath.Join(programFiles, "Beyond-All-Reason"), "Beyond All Reason")
		add(filepath.Join(localAppData, "Programs", "Beyond-All-Reason"), "Beyond All Reason (user install)")
		add(filepath.Join(appData, "Beyond-All-Reason"), "Beyond All Reason (app data)")
		if home != "" {
			add(filepath.Join(home, "Documents", "My Games", "Spring"), "Spring")
		}
		for _, lib := range steamLibraries(programFilesX86, programFiles) {
			add(filepath.Join(lib, "steamapps", "common", "Beyond All Reason"), "Beyond All Reason (Steam)")
		}
		return roots
	}

	// Linux and macOS.
	if home != "" {
		add(filepath.Join(home, ".local", "share", "Beyond-All-Reason"), "Beyond All Reason")
		add(filepath.Join(home, ".spring"), "Spring")
		add(filepath.Join(home, ".local", "share", "Steam", "steamapps", "common", "Beyond All Reason"), "Beyond All Reason (Steam)")
		add(filepath.Join(home, "Library", "Application Support", "Beyond-All-Reason"), "Beyond All Reason")
	}
	return roots
}

// steamLibraries returns Steam library roots, including secondary drives
// declared in libraryfolders.vdf.
func steamLibraries(candidates ...string) []string {
	var libs []string
	for _, base := range candidates {
		if base == "" {
			continue
		}
		steam := filepath.Join(base, "Steam")
		if _, err := os.Stat(steam); err != nil {
			continue
		}
		libs = append(libs, steam)
		libs = append(libs, parseLibraryFolders(filepath.Join(steam, "steamapps", "libraryfolders.vdf"))...)
	}
	return libs
}

// parseLibraryFolders extracts library paths from Steam's VDF file. The format
// is a simple quoted key/value tree; only "path" entries are of interest, so a
// line scan is sufficient and avoids a VDF dependency.
func parseLibraryFolders(vdf string) []string {
	data, err := os.ReadFile(vdf)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"path"`) {
			continue
		}
		parts := strings.Split(line, `"`)
		// "path"  "D:\\SteamLibrary"  splits to [ , path, \t , D:\SteamLibrary, ]
		if len(parts) >= 4 {
			out = append(out, strings.ReplaceAll(parts[3], `\\`, `\`))
		}
	}
	return out
}

// countDemos counts replay files in dir without recursing.
func countDemos(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), demoExt) {
			n++
		}
	}
	return n
}

// ValidateDemosPath checks that a user-supplied folder exists and is readable.
// It deliberately allows a folder with no replays yet, so a fresh install can
// be pointed at before the first match is played.
func ValidateDemosPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}
