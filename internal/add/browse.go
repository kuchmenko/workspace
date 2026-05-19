package add

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m AddModel) updateBrowse(msg tui.Msg) (tui.Model, tui.Cmd) {
	key, ok := msg.(tui.KeyMsg)
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
		var cmd tui.Cmd
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

		if len(m.selectedURLs) > 0 {
			m.transitionTo(addStateBulkConfirm)
			return m, nil
		}

		s := view[m.cursor]
		m.editFields = m.editFromSuggestion(s)
		m.editFocus = 0
		m.editErr = ""
		m.transitionTo(addStateEdit)
		return m, nil
	case " ":

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

		if len(m.selectedURLs) > 0 {
			m.selectedURLs = nil
			return m, nil
		}
		done := m.toDone()
		if m.standalone {
			return done, tui.Sequence(emit(m.doneMsg()), tui.Quit)
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

func renderSelectionPreview(s *Suggestion) string {
	var b strings.Builder

	b.WriteString("  " + addPreviewName.Render(s.Name))
	if u := shortURL(*s); u != "" {
		b.WriteString("  " + addDim.Render(u))
	}
	b.WriteString("\n")

	desc := strings.TrimSpace(s.Description)
	if desc == "" {
		desc = "(no description)"
		b.WriteString("  " + addDim.Render(truncate(desc, 100)) + "\n")
	} else {
		desc = strings.ReplaceAll(desc, "\n", " ")
		b.WriteString("  " + truncate(desc, 100) + "\n")
	}

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

type browseRowKind int

const (
	rowGroup browseRowKind = iota
	rowItem
)

type browseRow struct {
	kind       browseRowKind
	text       string
	suggestion *Suggestion
}

func buildBrowseRows(view []Suggestion) []browseRow {
	if len(view) == 0 {
		return nil
	}

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

func renderItemLine(cursor string, s *Suggestion) string {
	nameStyle := addItemName
	suffix := ""
	urlStyle := addDim

	switch {
	case s.RegisteredPath != "":

		nameStyle = addExists
		suffix = " " + addExistsTag.Render(
			fmt.Sprintf("● cloned at %s", s.RegisteredPath))
	case s.DiskPath != "":

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
		hay := strings.ToLower(s.Name + " " + s.RemoteURL + " " + s.InferredGrp + " " + s.Description)
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

func (m AddModel) editFromSuggestion(s Suggestion) editFields {
	cat := config.CategoryPersonal

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
