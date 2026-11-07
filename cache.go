package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const refreshInterval = 30 * time.Second

type fileSystem interface {
	Stat(string) (fs.FileInfo, error)
	ReadDir(string) ([]fs.DirEntry, error)
	Open(string) (io.ReadCloser, error)
}

type localFiles struct{}

func (localFiles) Stat(path string) (fs.FileInfo, error)      { return os.Stat(path) }
func (localFiles) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
func (localFiles) Open(path string) (io.ReadCloser, error)    { return os.Open(path) }

type reader struct {
	files    fileSystem
	home     string
	cwd      string
	cacheDir string
}

type cachedSnapshot struct {
	CheckedAt time.Time
	ModTime   time.Time
	Data      snapshot
	Size      int64
}

func cacheKey(parts ...string) string {
	return fmt.Sprintf("%x.json", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func (r reader) read(now time.Time) snapshot {
	resultFile := "result-" + cacheKey(r.home, r.cwd)
	var result cachedSnapshot
	if r.load(resultFile, &result) && !result.CheckedAt.After(now) && now.Sub(result.CheckedAt) < refreshInterval &&
		!result.Data.crossedReset(result.CheckedAt, now) {
		return result.Data
	}
	root, descendants := r.projectRoot()
	var data snapshot
	var walk func(string)
	walk = func(dir string) {
		entries, err := r.files.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			file := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				walk(file)
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".jsonl") || entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			saved := r.readFile(file, now)
			if saved.Session != nil && matches(saved.Session.CWD, root, descendants) &&
				(data.Session == nil || saved.Session.Timestamp.After(data.Session.Timestamp)) {
				data.Session = saved.Session
			}
			if saved.Usage != nil && (data.Usage == nil || saved.Usage.Timestamp.After(data.Usage.Timestamp)) {
				data.Usage = saved.Usage
			}
		}
	}
	walk(filepath.Join(r.home, "sessions"))
	r.save(resultFile, cachedSnapshot{CheckedAt: now, Data: data})
	return data
}

func (r reader) projectRoot() (string, bool) {
	for dir := r.cwd; ; dir = filepath.Dir(dir) {
		if _, err := r.files.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		if filepath.Dir(dir) == dir {
			return r.cwd, false
		}
	}
}

func (r reader) readFile(file string, now time.Time) snapshot {
	stat, err := r.files.Stat(file)
	if err != nil || !stat.Mode().IsRegular() {
		return snapshot{}
	}
	key := "file-" + cacheKey(file)
	var saved cachedSnapshot
	if r.load(key, &saved) && saved.Size == stat.Size() && saved.ModTime.Equal(stat.ModTime()) &&
		!saved.CheckedAt.After(now) && !saved.Data.crossedReset(saved.CheckedAt, now) {
		return saved.Data
	}
	input, err := r.files.Open(file)
	if err != nil {
		return snapshot{}
	}
	defer input.Close()
	data := parse(input)
	r.save(key, cachedSnapshot{CheckedAt: now, ModTime: stat.ModTime(), Size: stat.Size(), Data: data})
	return data
}

func (r reader) load(name string, value *cachedSnapshot) bool {
	if r.cacheDir == "" {
		return false
	}
	input, err := os.Open(filepath.Join(r.cacheDir, name))
	if err != nil {
		return false
	}
	defer input.Close()
	return json.NewDecoder(io.LimitReader(input, 1024*1024)).Decode(value) == nil
}

func (r reader) save(name string, value cachedSnapshot) {
	if r.cacheDir == "" {
		return
	}
	if err := os.MkdirAll(r.cacheDir, 0700); err != nil {
		return
	}
	file, err := os.CreateTemp(r.cacheDir, ".snapshot-*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return
	}
	// Concurrent prompts must never observe a partially written cache entry.
	_ = os.Rename(file.Name(), filepath.Join(r.cacheDir, name))
}
