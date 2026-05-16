package add

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
)

func (m AddModel) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.filterMode {
		switch key.String() {
		case "esc":
			m.filterMode = false
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			return m, nil
		case "enter":
			m.filterMode = false
			m.filterInput.Blur()
			m.cursor = 0
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.cursor = 0
		return m, cmd
	}

	view := m.filteredView()

	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(view)-1 {
			m.cursor++
		}
	case "i":
		m.transitionTo(addStateManual)
		m.manualInput.SetValue("")
		m.manualErr = ""
		return m, m.manualInput.Focus()
	case "/":
		m.filterMode = true
		return m, m.filterInput.Focus()
	case "enter":
		if len(view) == 0 {
			return m, nil
		}
		// Bulk path: any URLs marked → confirm them all at once.
		if len(m.selectedURLs) > 0 {
			m.transitionTo(addStateBulkConfirm)
			return m, nil
		}
		// Single path: edit the cursor row.
		s := view[m.cursor]
		m.editFields = m.editFromSuggestion(s)
		m.editFocus = 0
		m.editErr = ""
		m.transitionTo(addStateEdit)
		return m, nil
	case " ":
		// Toggle the cursor row in the bulk-select set. The selection
		// is keyed by RemoteURL so it survives filter changes and
		// re-sorts.
		if len(view) == 0 {
			return m, nil
		}
		s := view[m.cursor]
		if s.RemoteURL == "" {
			return m, nil
		}
		if m.selectedURLs == nil {
			m.selectedURLs = make(map[string]bool)
		}
		if m.selectedURLs[s.RemoteURL] {
			delete(m.selectedURLs, s.RemoteURL)
		} else {
			m.selectedURLs[s.RemoteURL] = true
		}
		return m, nil
	case "a":
		// Mark every visible (filtered) suggestion. Toggle: if all
		// visible are already selected, clear them.
		if len(view) == 0 {
			return m, nil
		}
		if m.selectedURLs == nil {
			m.selectedURLs = make(map[string]bool)
		}
		allMarked := true
		for _, s := range view {
			if !m.selectedURLs[s.RemoteURL] {
				allMarked = false
				break
			}
		}
		if allMarked {
			for _, s := range view {
				delete(m.selectedURLs, s.RemoteURL)
			}
		} else {
			for _, s := range view {
				if s.RemoteURL != "" {
					m.selectedURLs[s.RemoteURL] = true
				}
			}
		}
		return m, nil
	case "esc":
		// Esc with selections clears them; esc on a clean browse exits.
		if len(m.selectedURLs) > 0 {
			m.selectedURLs = nil
			return m, nil
		}
		done := m.toDone()
		if m.standalone {
			return done, tea.Sequence(emit(m.doneMsg()), tea.Quit)
		}
		return done, emit(m.doneMsg())
	}
	return m, nil
}

func (m AddModel) viewBrowse() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Add project "))
	b.WriteString("\n\n")

	view := m.filteredView()
	if len(view) == 0 {
		b.WriteString(addDim.Render("  No suggestions found.\n\n"))
		b.WriteString("  " + addHelp.Render("[i] enter URL manually   [esc] quit"))
		return b.String()
	}

	// Per-source diagnostics. Each chip reflects the status of one
	// source as of "now": completed (with count), pending (spinner),
	// or errored (with hint). Updates each frame as new sources land.
	if len(m.sources) > 0 {
		b.WriteString("  ")
		b.WriteString(renderSourceChipsLive(m.sourceOutcomes))
		if m.sourcesDone < len(m.sources) {
			fmt.Fprintf(&b, "  %s",
				addDim.Render(fmt.Sprintf("%s loading %d more...",
					m.spinner.View(), len(m.sources)-m.sourcesDone)))
		}
		b.WriteString("\n\n")
	}

	if m.filterInput.Value() != "" {
		fmt.Fprintf(&b, "  search: %s\n\n", addAccent.Render(m.filterInput.Value()))
	}

	// Build the tree: group suggestions by owner / kind. The cursor
	// (m.cursor) still indexes the flat filtered slice; the tree is a
	// pure rendering concern. We compute which "rendered row" the
	// cursor maps to and crop a window around it.
	rows := buildBrowseRows(view)
	cursorRow := -1
	itemSeen := 0
	for i, r := range rows {
		if r.kind == rowItem {
			if itemSeen == m.cursor {
				cursorRow = i
			}
			itemSeen++
		}
	}

	const visibleRows = 16
	start, end := windowAround(cursorRow, len(rows), visibleRows)
	for i := start; i < end; i++ {
		r := rows[i]
		switch r.kind {
		case rowGroup:
			fmt.Fprintf(&b, "  %s\n", r.text)
		case rowItem:
			s := r.suggestion
			selected := i == cursorRow
			marked := m.selectedURLs[s.RemoteURL]
			cursor := "    "
			if selected && marked {
				cursor = " " + addCursor.Render("▸") + addAccent.Render("●")
			} else if selected {
				cursor = "  " + addCursor.Render("▸ ")
			} else if marked {
				cursor = "  " + addAccent.Render("● ")
			}
			line := strings.TrimRight(renderItemLine(cursor, s), "\n")
			if selected {
				// Pad the line out to terminal width and apply a
				// background highlight so the entire row reads as
				// "this is what Enter will select". Width(0) is a
				// no-op when m.width hasn't been seen yet (pre
				// WindowSizeMsg) — falls back to natural length.
				rs := addCursorRow
				if m.width > 0 {
					rs = rs.Width(m.width)
				}
				line = rs.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	if start > 0 || end < len(rows) {
		fmt.Fprintf(&b, "\n  %s\n",
			addDim.Render(fmt.Sprintf("(scrolled %d/%d items)", m.cursor+1, len(view))))
	}

	// Selected-item preview: description + repo metadata. Always
	// rendered when a row is highlighted so the visible height stays
	// stable as the cursor moves.
	if cursorRow >= 0 && cursorRow < len(rows) && rows[cursorRow].kind == rowItem {
		b.WriteString("\n")
		b.WriteString(renderSelectionPreview(rows[cursorRow].suggestion))
	}

	b.WriteString("\n")
	if m.filterMode {
		b.WriteString("  search: " + m.filterInput.View() + "\n")
		b.WriteString("  " + addHelp.Render("[enter] commit   [esc] cancel"))
	} else if n := len(m.selectedURLs); n > 0 {
		fmt.Fprintf(&b, "  %s  %s\n",
			addAccent.Render(fmt.Sprintf("● %d marked", n)),
			addHelp.Render("[⏎] confirm bulk add  [space] toggle  [a] all  [esc] clear"))
		b.WriteString("  " + addHelp.Render("[↑↓] navigate  [/] search  [i] manual URL"))
	} else {
		b.WriteString("  " + addHelp.Render("[↑↓] navigate  [⏎] select  [space] mark  [a] all  [/] search  [i] manual URL  [esc] quit"))
	}
	return b.String()
}

// renderSelectionPreview shows the currently-selected suggestion's
// description and metadata (last push, activity, sources, paths).
// Always emits at least 2 lines so the screen height stays constant
// as the cursor moves between described and undescribed repos —
// otherwise the help line jumps.
func renderSelectionPreview(s *Suggestion) string {
	var b strings.Builder
	// Title line: name + URL.
	b.WriteString("  " + addPreviewName.Render(s.Name))
	if u := shortURL(*s); u != "" {
		b.WriteString("  " + addDim.Render(u))
	}
	b.WriteString("\n")

	// Description, or a placeholder so the layout doesn't shift.
	desc := strings.TrimSpace(s.Description)
	if desc == "" {
		desc = "(no description)"
		b.WriteString("  " + addDim.Render(truncate(desc, 100)) + "\n")
	} else {
		// Replace newlines so multi-line descriptions don't blow out
		// the layout. Truncate at ~100 chars for the same reason.
		desc = strings.ReplaceAll(desc, "\n", " ")
		b.WriteString("  " + truncate(desc, 100) + "\n")
	}

	// Optional metadata: pushed timestamp, activity count, registered
	// or local-disk hint repeated here for visibility (they're also
	// rendered as inline tags on the row, but the preview is where
	// the user looks for context after selecting).
	var meta []string
	if !s.PushedAt.IsZero() && s.PushedAt.Year() > 1 {
		meta = append(meta, "pushed "+relativeTime(s.PushedAt))
	}
	if s.GhActivity > 0 {
		meta = append(meta, fmt.Sprintf("%d events", s.GhActivity))
	}
	if s.RegisteredPath != "" {
		meta = append(meta, "● already at "+s.RegisteredPath)
	} else if s.DiskPath != "" {
		meta = append(meta, "● local at "+s.DiskPath)
	}
	if len(meta) > 0 {
		b.WriteString("  " + addDim.Render(strings.Join(meta, " · ")) + "\n")
	}
	return b.String()
}

// browseRowKind tags a rendered line so the windowing math can tell
// group headers (which the cursor cannot land on) from item rows
// (which it can).
type browseRowKind int

const (
	rowGroup browseRowKind = iota
	rowItem
)

type browseRow struct {
	kind       browseRowKind
	text       string      // pre-formatted header text; empty for items
	suggestion *Suggestion // non-nil for items
}

// buildBrowseRows walks an already-sorted view (sortByRelevance puts
// it in group → in-group order) and emits a header row each time the
// group key changes. This keeps m.cursor's view-index aligned with
// the position of the matching item row in the rendered tree —
// critical for the cursor marker and Enter to point at the same
// suggestion.
func buildBrowseRows(view []Suggestion) []browseRow {
	if len(view) == 0 {
		return nil
	}

	// First pass: count items per group key for the header counts.
	// Cheap because the view is small (≤ low hundreds even at scale).
	groupCounts := map[string]int{}
	for i := range view {
		k, _, _ := groupKey(view[i])
		groupCounts[k]++
	}

	var rows []browseRow
	var lastKey string
	for i := range view {
		s := &view[i]
		key, label, _ := groupKey(*s)
		if key != lastKey {
			header := fmt.Sprintf("%s %s",
				addGroupHdr.Render(label),
				addDim.Render(fmt.Sprintf("(%d)", groupCounts[key])))
			rows = append(rows, browseRow{kind: rowGroup, text: header})
			lastKey = key
		}
		rows = append(rows, browseRow{kind: rowItem, suggestion: s})
	}
	return rows
}

// groupKey returns (key, displayLabel, sortOrder) for a Suggestion.
// Sort order pins Clipboard at the top (most recent intent), then
// any disk-only entries (acting on what's already on the user's
// machine), then GitHub owners alphabetically. Mixed sources fall
// into the GitHub bucket because that's where they came from
// originally — the disk presence becomes a row-level highlight, not
// a separate bucket.
func groupKey(s Suggestion) (key, label string, order int) {
	hasGh := hasSource(s.Sources, SourceGitHub)
	hasClip := hasSource(s.Sources, SourceClipboard)
	hasDisk := hasSource(s.Sources, SourceDisk)
	hasManual := hasSource(s.Sources, SourceManual)

	switch {
	case hasClip && !hasGh:
		return "_clip", "Clipboard", 0
	case hasManual && !hasGh:
		return "_manual", "Manual", 0
	case hasDisk && !hasGh:
		return "_disk", "Local (unregistered)", 1
	case hasGh && s.InferredGrp != "":
		return "gh:" + strings.ToLower(s.InferredGrp), s.InferredGrp, 2
	default:
		return "_other", "Other", 3
	}
}

// windowAround crops [0, total) to a visible-size window centered
// around `cursor`. Used by viewBrowse to keep the cursor in view
// without scrolling the entire 300-row tree.
func windowAround(cursor, total, size int) (start, end int) {
	if total <= size {
		return 0, total
	}
	if cursor < 0 {
		return 0, size
	}
	half := size / 2
	start = cursor - half
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > total {
		end = total
		start = end - size
	}
	return start, end
}

// renderItemLine produces one suggestion-row in the browse list,
// applying the "already cloned" highlight when the suggestion has a
// disk path or a registered-path match. The cursor argument is the
// pre-rendered prefix ("  ▸ " for the selected row, "    " otherwise).
func renderItemLine(cursor string, s *Suggestion) string {
	nameStyle := addItemName
	suffix := ""
	urlStyle := addDim

	switch {
	case s.RegisteredPath != "":
		// Already in workspace.toml — would create a duplicate. The
		// highlight is loud enough to warn the user but the row
		// stays selectable so they can intentionally make a copy.
		nameStyle = addExists
		suffix = " " + addExistsTag.Render(
			fmt.Sprintf("● cloned at %s", s.RegisteredPath))
	case s.DiskPath != "":
		// Found on disk but not registered — selecting will
		// register the existing path (no clone).
		nameStyle = addExists
		suffix = " " + addExistsTag.Render(
			fmt.Sprintf("● local: %s", s.DiskPath))
	}

	url := shortURL(*s)
	return fmt.Sprintf("%s%s  %s  %s%s\n",
		cursor,
		nameStyle.Render(addPad(s.Name, 24)),
		renderSourceChips(s.Sources),
		urlStyle.Render(url),
		suffix)
}

func (m AddModel) filteredView() []Suggestion {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		return m.allSuggestions
	}
	var out []Suggestion
	for _, s := range m.allSuggestions {
		// Search across name, URL, owner/group, and the repo
		// description so the user can find a repo by what it does
		// (e.g. typing "graphql" matches any repo whose description
		// mentions GraphQL), not just by name.
		hay := strings.ToLower(s.Name + " " + s.RemoteURL + " " + s.InferredGrp + " " + s.Description)
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

func (m AddModel) editFromSuggestion(s Suggestion) editFields {
	cat := config.CategoryPersonal
	// Crude heuristic: if the inferred group looks like a work org
	// (anything other than the user's GitHub login or "personal"),
	// default to Work. The user can flip on the edit screen.
	grp := s.InferredGrp
	if grp != "" && grp != "personal" {
		cat = config.CategoryWork
	}
	return editFields{
		Name:     s.Name,
		URL:      s.RemoteURL,
		Category: cat,
		Group:    grp,
		Path:     buildPath(grp, cat, s.Name),
		FromDisk: s.DiskPath,
	}
}
