package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, diagnostic io.Writer) int {
	flags := flag.NewFlagSet("codex-prompt", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	home := flags.String("codex-home", "", "Codex home directory (default: CODEX_HOME or ~/.codex)")
	cwd := flags.String("cwd", "", "project directory (default: current directory)")
	cacheDir := flags.String("cache-dir", "", "cache directory (default: user cache directory)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostic, "codex-prompt: unexpected positional arguments")
		return 2
	}
	userHome, _ := os.UserHomeDir()
	if *home == "" {
		*home = os.Getenv("CODEX_HOME")
	}
	if *home == "" {
		*home = filepath.Join(userHome, ".codex")
	}
	if *home == "~" || strings.HasPrefix(*home, "~/") {
		*home = filepath.Join(userHome, strings.TrimPrefix(*home, "~"))
	}
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}
	if *cacheDir == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			*cacheDir = filepath.Join(dir, "oh-my-posh-codex-prompt", "v1")
		}
	}
	absoluteHome, err := filepath.Abs(*home)
	if err != nil {
		return 0
	}
	absoluteCWD, err := filepath.Abs(*cwd)
	if err != nil {
		return 0
	}
	r := reader{files: localFiles{}, home: absoluteHome, cwd: absoluteCWD, cacheDir: *cacheDir}
	now := time.Now()
	text := format(r.read(now), time.Now())
	if text != "" {
		fmt.Fprintln(out, text)
	}
	return 0
}

func format(data snapshot, now time.Time) string {
	var parts []string
	if data.Session != nil {
		// The output is embedded in a prompt: exclude controls and markup delimiters.
		model := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.IsSpace(r) || r == '<' || r == '>' {
				return -1
			}
			return r
		}, data.Session.Model)
		if model != "" {
			parts = append(parts, model)
		}
	}
	if data.Usage != nil && data.Usage.Primary != nil {
		w := data.Usage.Primary
		if w.ResetsAt.Unix() <= 0 || w.ResetsAt.After(now) {
			parts = append(parts, fmt.Sprintf("%d%%", w.Remaining))
			if w.ResetsAt.Unix() > 0 {
				parts = append(parts, "↻"+countdown(w.ResetsAt.Sub(now)))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "cx " + strings.Join(parts, " ")
}

func countdown(remaining time.Duration) string {
	minutes := int(remaining / time.Minute)
	if minutes < 1 {
		return "<1m"
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
}
