package selfcontext

import (
	"strings"
	"time"
)

// panelEntriesLimit is how many recorded rows State() exposes to the panel:
// enough to review the recent timeline, small enough that every state
// broadcast stays a few KB even with the in-memory buffer at its 500-entry
// ceiling (C2-5, mirrors clipboard C2-1).
const panelEntriesLimit = 30

// maxPreview caps every text field of a panel row. Window titles and VLM
// summaries are untrusted, potentially endless content: the cap keeps each
// state broadcast bounded no matter what was on screen.
const maxPreview = 120

// PanelEntry is one row of the recorded-context list surfaced through
// State()["entries"] (C2-5). It is a bounded, read-only view: every text
// field is flattened and truncated, and the timestamp is RFC3339 — the panel
// renders the fields verbatim via textContent, never as markup.
type PanelEntry struct {
	Title   string `json:"title"`
	Process string `json:"process,omitempty"`
	// Summary 是 modeScreen 下 VLM 生成的画面描述，其余模式为空。
	Summary string `json:"summary,omitempty"`
	Time    string `json:"time"` // RFC3339；零值时间为空串
}

// panelEntryViews renders the newest panelEntriesLimit records, newest first,
// matching the order the panel's history list displays. The input must be a
// snapshot taken under f.mu (Feature.State copies before calling). The result
// is never nil: the panel distinguishes "no records yet" (empty array) from a
// missing key.
func panelEntryViews(all []Entry) []PanelEntry {
	if n := len(all); n > panelEntriesLimit {
		all = all[n-panelEntriesLimit:]
	}
	out := make([]PanelEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		row := PanelEntry{
			Title:   excerpt(e.Title, maxPreview),
			Process: excerpt(e.Process, maxPreview),
			Summary: excerpt(e.Summary, maxPreview),
		}
		if !e.Timestamp.IsZero() {
			row.Time = e.Timestamp.Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out
}

// excerpt flattens text to a single line and caps it at max runes (plus an
// ellipsis when truncated).
func excerpt(s string, max int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}
