package main

import (
	"bufio"
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"
)

type snapshot struct {
	Session *session
	Usage   *usage
}

type session struct {
	Timestamp time.Time
	ID        string
	CWD       string
	Model     string
}

type usage struct {
	Timestamp time.Time
	Primary   *window
	Secondary *window
}

type window struct {
	ResetsAt  time.Time
	Remaining int
}

type record struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Info *struct {
			Total  json.RawMessage `json:"total_token_usage"`
			Last   json.RawMessage `json:"last_token_usage"`
			Window *int            `json:"model_context_window"`
		} `json:"info"`
		Limits *struct {
			Primary   *windowRecord `json:"primary"`
			Secondary *windowRecord `json:"secondary"`
		} `json:"rate_limits"`
		Type  string `json:"type"`
		ID    string `json:"id"`
		CWD   string `json:"cwd"`
		Model string `json:"model"`
	} `json:"payload"`
}

type windowRecord struct {
	Used  *float64 `json:"used_percent"`
	Reset int64    `json:"resets_at"`
}

func parse(reader io.Reader) snapshot {
	var data snapshot
	buffered := bufio.NewReader(reader)
	var line []byte
	oversized := false
	for {
		fragment, more, err := buffered.ReadLine()
		if err != nil {
			return data
		}
		// Tool output can produce enormous records; skip them without losing later updates.
		if len(line)+len(fragment) > 1024*1024 {
			oversized = true
		}
		if !oversized {
			line = append(line, fragment...)
		}
		if more {
			continue
		}
		if !oversized && len(line) != 0 {
			var r record
			if json.Unmarshal(line, &r) == nil {
				data.update(&r)
			}
		}
		line = line[:0]
		oversized = false
	}
}

func (d *snapshot) update(r *record) {
	p := r.Payload
	valid := r.Type == "session_meta" && p.ID != "" || r.Type == "turn_context" && p.Model != ""
	valid = valid || r.Type == "event_msg" && p.Type == "token_count" && p.Info != nil &&
		(p.Info.Total != nil || p.Info.Last != nil || p.Info.Window != nil)
	if valid && (d.Session == nil || !r.Timestamp.Before(d.Session.Timestamp)) {
		if d.Session == nil {
			d.Session = &session{}
		}
		d.Session.Timestamp = r.Timestamp
		if p.ID != "" {
			d.Session.ID = p.ID
		}
		if p.CWD != "" {
			d.Session.CWD = p.CWD
		}
		if p.Model != "" {
			d.Session.Model = p.Model
		}
	}
	if r.Type != "event_msg" || p.Type != "token_count" || p.Limits == nil {
		return
	}
	primary, secondary := parseWindow(p.Limits.Primary), parseWindow(p.Limits.Secondary)
	if primary == nil && secondary == nil {
		return
	}
	if d.Usage != nil && r.Timestamp.Before(d.Usage.Timestamp) {
		return
	}
	d.Usage = &usage{Timestamp: r.Timestamp, Primary: primary, Secondary: secondary}
}

func parseWindow(w *windowRecord) *window {
	if w == nil || w.Used == nil {
		return nil
	}
	used := int(math.Round(min(100, max(0, *w.Used))))
	return &window{ResetsAt: time.Unix(w.Reset, 0), Remaining: 100 - used}
}

func matches(cwd, root string, descendants bool) bool {
	if cwd == "" {
		return false
	}
	cwd, root = filepath.Clean(cwd), filepath.Clean(root)
	if cwd == root {
		return true
	}
	return descendants && strings.HasPrefix(cwd, strings.TrimRight(root, string(filepath.Separator))+string(filepath.Separator))
}

func (d snapshot) crossedReset(since, now time.Time) bool {
	if d.Usage == nil {
		return false
	}
	for _, w := range []*window{d.Usage.Primary, d.Usage.Secondary} {
		if w != nil && w.ResetsAt.Unix() > 0 && w.ResetsAt.After(since) && !w.ResetsAt.After(now) {
			return true
		}
	}
	return false
}
