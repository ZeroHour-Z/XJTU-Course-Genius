package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type ConfigFile struct {
	Course     [][]string `json:"course"`
	DelCourses [][]string `json:"delcourses"`
}

var (
	cfg     ConfigFile
	cfgPath string
	mu      sync.RWMutex
)

// Dir returns the per-user data directory, used for the log and port files on
// every platform and for config.json on macOS (see configPath).
func Dir() string {
	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = os.Getenv("APPDATA")
		if dir == "" {
			dir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
	case "darwin":
		dir = filepath.Join(os.Getenv("HOME"), "Library", "Application Support")
	default:
		dir = os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			dir = filepath.Join(os.Getenv("HOME"), ".config")
		}
	}
	return filepath.Join(dir, "xjtu-genius")
}

// exeConfigPath is config.json beside the executable — the location used on
// Windows and Linux, where the install dir is writable.
func exeConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "config.json")
}

// configPath picks where config.json lives.
// Windows and Linux keep it beside the executable
// macOS cannot: the .app bundle is read-only under sandbox
func configPath() string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return filepath.Join(Dir(), "config.json")
	}
	if p := exeConfigPath(); p != "" {
		return p
	}
	return filepath.Join(Dir(), "config.json")
}

func init() {
	cfgPath = configPath()
	Load()
}

func Path() string { return cfgPath }

func Load() {
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		// macOS only: pick up a config left beside the executable by an older
		// build, so existing wish lists survive the move to the data directory.
		if old := exeConfigPath(); old != "" && old != cfgPath {
			if data, err = os.ReadFile(old); err == nil {
				if json.Unmarshal(data, &cfg) == nil {
					return
				}
			}
		}
		cfg = ConfigFile{Course: [][]string{}, DelCourses: [][]string{}}
		return
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = ConfigFile{Course: [][]string{}, DelCourses: [][]string{}}
	}
}

func Save() {
	mu.RLock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	mu.RUnlock()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		log.Printf("[config] failed to create config dir: %v", err)
		return
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		log.Printf("[config] failed to save %s: %v", cfgPath, err)
	}
}

func Get() ConfigFile {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}

func SetCourse(course [][]string, delcourses [][]string) {
	mu.Lock()
	cfg.Course = course
	cfg.DelCourses = delcourses
	mu.Unlock()
}

func UpdateAt(idx int, course []string) {
	mu.Lock()
	defer mu.Unlock()
	if idx >= 0 && idx < len(cfg.Course) {
		cfg.Course[idx] = course
	}
}
