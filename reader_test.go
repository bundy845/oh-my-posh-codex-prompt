package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func rollout(cwd, model string, updated, reset time.Time, used float64) string {
	return fmt.Sprintf("{\"timestamp\":%q,\"type\":\"session_meta\",\"payload\":{\"id\":\"session\",\"cwd\":%q}}\n"+
		"{\"timestamp\":%q,\"type\":\"turn_context\",\"payload\":{\"model\":%q}}\n"+
		"{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{\"last_token_usage\":{\"total_tokens\":25}},"+
		"\"rate_limits\":{\"primary\":{\"used_percent\":%g,\"resets_at\":%d}}}}\n",
		updated.Add(-time.Minute).Format(time.RFC3339Nano), cwd, updated.Format(time.RFC3339Nano), model,
		updated.Format(time.RFC3339Nano), used, reset.Unix())
}

func TestParse(t *testing.T) {
	valid := rollout("/project", "test-model", testNow, testNow.Add(time.Hour), 5)
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "combined", input: valid, want: "cx test-model 95% ↻1h0m"},
		{name: "malformed before valid", input: "invalid\n" + valid, want: "cx test-model 95% ↻1h0m"},
		{name: "oversized before valid", input: strings.Repeat("x", 2*1024*1024) + "\n" + valid, want: "cx test-model 95% ↻1h0m"},
		{name: "partial append", input: valid + `{"type":`, want: "cx test-model 95% ↻1h0m"},
		{name: "no trailing newline", input: strings.TrimSpace(valid), want: "cx test-model 95% ↻1h0m"},
		{name: "null update", input: valid + `{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":null}}`, want: "cx test-model 95% ↻1h0m"},
		{name: "conversation only", input: `{"type":"response_item","payload":{"type":"message","content":"private transcript"}}`},
		{name: "empty"},
		{name: "clamp high", input: rollout("/project", "model", testNow, time.Unix(0, 0), 120), want: "cx model 0%"},
		{name: "clamp low", input: rollout("/project", "model", testNow, time.Unix(0, 0), -20), want: "cx model 100%"},
		{name: "round", input: rollout("/project", "model", testNow, time.Unix(0, 0), 5.5), want: "cx model 94%"},
		{name: "out of order", input: valid + rollout("/other", "old", testNow.Add(-time.Hour), testNow.Add(time.Hour), 90), want: "cx test-model 95% ↻1h0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := format(parse(strings.NewReader(tc.input)), testNow); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		remaining int
		reset     time.Time
		quota     bool
		want      string
	}{
		{name: "quota only", remaining: 95, reset: testNow.Add(35*time.Minute + 59*time.Second), quota: true, want: "cx 95% ↻35m"},
		{name: "model only", model: "gpt-test", want: "cx gpt-test"},
		{name: "unknown reset", remaining: 20, quota: true, want: "cx 20%"},
		{name: "under one minute", remaining: 5, reset: testNow.Add(59 * time.Second), quota: true, want: "cx 5% ↻<1m"},
		{name: "hours", remaining: 40, reset: testNow.Add(135 * time.Minute), quota: true, want: "cx 40% ↻2h15m"},
		{name: "at reset", remaining: 95, reset: testNow, quota: true},
		{name: "after reset", model: "model", reset: testNow.Add(-time.Second), quota: true, want: "cx model"},
		{name: "strip controls and markup", model: "model\n\x1b<bad>", want: "cx modelbad"},
		{name: "no data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var data snapshot
			if tc.model != "" {
				data.Session = &session{Model: tc.model}
			}
			if tc.quota {
				data.Usage = &usage{Primary: &window{Remaining: tc.remaining, ResetsAt: tc.reset}, Secondary: &window{Remaining: 99}}
			}
			if got := format(data, testNow); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func writeFile(t *testing.T, file, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectSelection(t *testing.T) {
	cases := []struct {
		name string
		git  bool
		cwd  string
		want string
	}{
		{name: "exact outside git", cwd: "project", want: "cx project-model 80% ↻1h0m"},
		{name: "subdirectory without git", cwd: "project/sub", want: "cx 80% ↻1h0m"},
		{name: "git root includes descendants", git: true, cwd: "project/sub", want: "cx child-model 80% ↻1h0m"},
		{name: "directory boundary", cwd: "project-other", want: "cx 80% ↻1h0m"},
		{name: "unrelated directory still gets account quota", cwd: "unrelated", want: "cx 80% ↻1h0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "codex")
			project := filepath.Join(root, "project")
			if tc.git {
				// Worktrees use a file rather than a directory as their .git marker.
				writeFile(t, filepath.Join(project, ".git"), "gitdir: /unused")
			}
			for i, fixture := range []struct{ cwd, model string }{
				{cwd: project, model: "project-model"},
				{cwd: filepath.Join(project, "child"), model: "child-model"},
				{cwd: filepath.Join(root, "other"), model: "other-model"},
			} {
				writeFile(t, filepath.Join(home, "sessions", "2026", fmt.Sprintf("%d.jsonl", i)),
					rollout(fixture.cwd, fixture.model, testNow.Add(time.Duration(i)*time.Second), testNow.Add(time.Hour), float64(i*10)))
			}
			r := reader{files: localFiles{}, home: home, cwd: filepath.Join(root, tc.cwd), cacheDir: filepath.Join(root, "cache")}
			if got := format(r.read(testNow), testNow); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

type countedFiles struct {
	localFiles
	opens int
	lists int
}

func (f *countedFiles) Open(path string) (io.ReadCloser, error) {
	f.opens++
	return f.localFiles.Open(path)
}

func (f *countedFiles) ReadDir(path string) ([]os.DirEntry, error) {
	f.lists++
	return f.localFiles.ReadDir(path)
}

func TestCache(t *testing.T) {
	cases := []struct {
		name      string
		elapsed   time.Duration
		reset     time.Duration
		change    bool
		corrupt   bool
		wantReads int
		wantLists int
		want      string
	}{
		{name: "warm cache updates countdown", elapsed: 20 * time.Second, reset: 35 * time.Minute, wantReads: 1, wantLists: 1, want: "cx model 95% ↻34m"},
		{name: "unchanged files reused after ttl", elapsed: 31 * time.Second, reset: time.Hour, wantReads: 1, wantLists: 2, want: "cx model 95% ↻59m"},
		{name: "changed file refreshed", elapsed: 31 * time.Second, reset: time.Hour, change: true, wantReads: 2, wantLists: 2, want: "cx next-model 95% ↻59m"},
		{name: "reset bypasses ttl and unchanged stat", elapsed: 10 * time.Second, reset: 10 * time.Second, wantReads: 2, wantLists: 2, want: "cx model"},
		{name: "corrupt result cache recovered", elapsed: time.Second, reset: time.Hour, corrupt: true, wantReads: 1, wantLists: 2, want: "cx model 95% ↻59m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "codex")
			file := filepath.Join(home, "sessions", "rollout.jsonl")
			content := rollout(root, "model", testNow, testNow.Add(tc.reset), 5)
			writeFile(t, file, content)
			files := &countedFiles{}
			r := reader{files: files, home: home, cwd: root, cacheDir: filepath.Join(root, "cache")}
			r.read(testNow)
			if tc.change {
				writeFile(t, file, rollout(root, "next-model", testNow, testNow.Add(tc.reset), 5))
			}
			if tc.corrupt {
				writeFile(t, filepath.Join(r.cacheDir, "result-"+cacheKey(home, root)), "invalid")
			}
			later := testNow.Add(tc.elapsed)
			for range 2 {
				if got := format(r.read(later), later); got != tc.want {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			}
			if files.opens != tc.wantReads || files.lists != tc.wantLists {
				t.Fatalf("transcript reads/listings = %d/%d, want %d/%d", files.opens, files.lists, tc.wantReads, tc.wantLists)
			}
		})
	}
}

func TestRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		code int
	}{
		{name: "missing sessions"},
		{name: "valid", want: "cx fixture-model 95% ↻"},
		{name: "unknown flag", args: []string{"--unknown"}, code: 2},
		{name: "help", args: []string{"--help"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "codex")
			if tc.want != "" {
				writeFile(t, filepath.Join(home, "sessions", "rollout.jsonl"), rollout(root, "fixture-model", time.Now(), time.Now().Add(time.Hour), 5))
			}
			args := append([]string{"--codex-home", home, "--cwd", root, "--cache-dir", filepath.Join(root, "cache")}, tc.args...)
			var out, diagnostics bytes.Buffer
			if code := run(args, &out, &diagnostics); code != tc.code {
				t.Fatalf("exit = %d, want %d", code, tc.code)
			}
			if !strings.HasPrefix(out.String(), tc.want) || tc.want == "" && out.Len() != 0 {
				t.Fatalf("unexpected stdout %q", out.String())
			}
			if tc.code == 0 && tc.name != "help" && diagnostics.Len() != 0 {
				t.Fatalf("unexpected diagnostics %q", diagnostics.String())
			}
		})
	}
}
