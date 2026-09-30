package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type mode int

const (
	modeList mode = iota
	modeCreate
	modeConfirmDelete
	modeConfirmForce
	modeConfirmBranchForce
	modeSetCommand
)

type (
	listedMsg struct {
		list []Worktree
		err  error
	}
	createdMsg struct {
		path string
		err  error
	}
	removedMsg struct {
		force bool
		err   error
	}
	branchDeletedMsg struct {
		force bool
		err   error
	}
	openedMsg struct{ err error }
	prunedMsg struct{ err error }
)

const (
	inBranch = iota
	inBase
	inPath
)

type model struct {
	repo      *Repo
	worktrees []Worktree
	cursor    int
	offset    int
	width     int
	height    int

	mode       mode
	busy       bool
	status     string
	statusErr  bool
	selectPath string // select this worktree after the next reload

	openCmd   string
	openScope string

	inputs     []textinput.Model
	focus      int
	pathEdited bool

	cmdInput  textinput.Model
	cmdGlobal bool

	target    Worktree
	delBranch bool
	lastErr   string
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	branchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	keyStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("7"))
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
)

func newModel(repo *Repo) model {
	labels := []string{"branch name", "blank = HEAD, or the matching remote branch", "worktree path"}
	inputs := make([]textinput.Model, len(labels))
	for i, ph := range labels {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.Prompt = ""
		ti.CharLimit = 512
		inputs[i] = ti
	}
	ci := textinput.New()
	ci.Prompt = ""
	ci.Placeholder = "e.g. code {path}   (blank = $SHELL)"
	ci.CharLimit = 1024

	m := model{repo: repo, inputs: inputs, cmdInput: ci, busy: true}
	m.openCmd, m.openScope = repo.OpenCommand()
	return m
}

func (m model) Init() tea.Cmd { return m.load() }

func (m model) load() tea.Cmd {
	return func() tea.Msg {
		list, err := m.repo.List()
		return listedMsg{list, err}
	}
}

func (m model) selected() (Worktree, bool) {
	if m.cursor < 0 || m.cursor >= len(m.worktrees) {
		return Worktree{}, false
	}
	return m.worktrees[m.cursor], true
}

func (m model) mainPath() string {
	if len(m.worktrees) > 0 {
		return m.worktrees[0].Path
	}
	return m.repo.Current
}

func (m *model) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		w := m.boxWidth() - 12
		for i := range m.inputs {
			m.inputs[i].Width = w
		}
		m.cmdInput.Width = w
		m.clampScroll()
		return m, nil

	case listedMsg:
		m.busy = false
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.worktrees = msg.list
		if m.selectPath != "" {
			for i, wt := range m.worktrees {
				if realpath(wt.Path) == realpath(m.selectPath) {
					m.cursor = i
				}
			}
			m.selectPath = ""
		}
		m.cursor = min(m.cursor, max(len(m.worktrees)-1, 0))
		m.clampScroll()
		return m, nil

	case createdMsg:
		m.busy = false
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.mode = modeList
		m.selectPath = msg.path
		m.setStatus("Created "+m.displayPath(msg.path), false)
		m.busy = true
		return m, m.load()

	case removedMsg:
		m.busy = false
		if msg.err != nil {
			if !msg.force {
				m.lastErr = msg.err.Error()
				m.mode = modeConfirmForce
				return m, nil
			}
			m.mode = modeList
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.setStatus("Removed "+m.displayPath(m.target.Path), false)
		if m.delBranch && m.target.Branch != "" {
			m.busy = true
			return m, m.deleteBranch(false)
		}
		m.mode = modeList
		m.busy = true
		return m, m.load()

	case branchDeletedMsg:
		m.busy = false
		if msg.err != nil && !msg.force {
			m.lastErr = msg.err.Error()
			m.mode = modeConfirmBranchForce
			return m, nil
		}
		m.mode = modeList
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else {
			m.setStatus(fmt.Sprintf("Removed %s and deleted branch %s", m.displayPath(m.target.Path), m.target.Branch), false)
		}
		m.busy = true
		return m, m.load()

	case openedMsg:
		if msg.err != nil {
			m.setStatus("open command failed: "+msg.err.Error(), true)
		}
		m.busy = true
		return m, m.load()

	case prunedMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else {
			m.setStatus("Pruned stale worktree entries", false)
		}
		return m, m.load()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeCreate:
			return m.updateCreate(msg)
		case modeSetCommand:
			return m.updateSetCommand(msg)
		default:
			return m.updateConfirm(msg)
		}
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.worktrees)-1, 0))
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(len(m.worktrees)-1, 0)
	case "r":
		m.busy = true
		m.setStatus("", false)
		return m, m.load()
	case "p":
		m.busy = true
		return m, func() tea.Msg { return prunedMsg{m.repo.Prune()} }

	case "enter", "o":
		wt, ok := m.selected()
		if !ok {
			return m, nil
		}
		if wt.Prunable || wt.Bare {
			m.setStatus("Can't open a bare or missing worktree", true)
			return m, nil
		}
		m.setStatus("", false)
		return m, tea.ExecProcess(OpenExec(m.openCmd, wt), func(err error) tea.Msg { return openedMsg{err} })

	case "n", "a":
		m.mode = modeCreate
		m.focus = inBranch
		m.pathEdited = false
		for i := range m.inputs {
			m.inputs[i].SetValue("")
			m.inputs[i].Blur()
		}
		m.inputs[inBranch].Focus()
		m.setStatus("", false)
		return m, textinput.Blink

	case "d", "x":
		wt, ok := m.selected()
		if !ok {
			return m, nil
		}
		switch {
		case wt.Main:
			m.setStatus("Can't remove the main worktree", true)
		case m.repo.Current != "" && realpath(wt.Path) == m.repo.Current:
			m.setStatus("Can't remove the worktree you're currently in", true)
		default:
			m.target, m.delBranch = wt, false
			m.mode = modeConfirmDelete
			m.setStatus("", false)
		}

	case "c":
		m.mode = modeSetCommand
		m.cmdInput.SetValue(m.openCmd)
		m.cmdInput.CursorEnd()
		m.cmdGlobal = m.openScope == "global"
		m.setStatus("", false)
		return m, tea.Batch(m.cmdInput.Focus(), textinput.Blink)
	}
	m.clampScroll()
	return m, nil
}

func (m model) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.setStatus("", false)
		return m, nil
	case "tab", "down":
		return m, m.focusInput((m.focus + 1) % len(m.inputs))
	case "shift+tab", "up":
		return m, m.focusInput((m.focus + len(m.inputs) - 1) % len(m.inputs))
	case "enter":
		branch := strings.TrimSpace(m.inputs[inBranch].Value())
		if branch == "" {
			m.setStatus("Branch name is required", true)
			return m, m.focusInput(inBranch)
		}
		base := strings.TrimSpace(m.inputs[inBase].Value())
		path := strings.TrimSpace(m.inputs[inPath].Value())
		if path == "" {
			path = DefaultPath(m.mainPath(), branch)
		}
		path = ResolvePath(path, m.mainPath())
		m.busy = true
		m.setStatus("Creating worktree…", false)
		return m, func() tea.Msg { return createdMsg{path, m.repo.Add(path, branch, base)} }
	}

	var cmd tea.Cmd
	before := m.inputs[m.focus].Value()
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	after := m.inputs[m.focus].Value()
	if m.focus == inPath && before != after {
		m.pathEdited = after != ""
	}
	if m.focus == inBranch && !m.pathEdited {
		if b := strings.TrimSpace(after); b != "" {
			m.inputs[inPath].SetValue(tildify(DefaultPath(m.mainPath(), b)))
		} else {
			m.inputs[inPath].SetValue("")
		}
	}
	return m, cmd
}

func (m *model) focusInput(i int) tea.Cmd {
	m.inputs[m.focus].Blur()
	m.focus = i
	m.inputs[i].CursorEnd()
	return m.inputs[i].Focus()
}

func (m model) updateSetCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		return m, nil
	case "tab":
		m.cmdGlobal = !m.cmdGlobal
		return m, nil
	case "enter":
		cmd := strings.TrimSpace(m.cmdInput.Value())
		if err := m.repo.SetOpenCommand(cmd, m.cmdGlobal); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.openCmd, m.openScope = m.repo.OpenCommand()
		m.mode = modeList
		if cmd == "" {
			m.setStatus("Cleared open command", false)
		} else {
			m.setStatus("Saved open command", false)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "n" || key == "esc" || key == "q" {
		if m.mode == modeConfirmBranchForce {
			m.setStatus(fmt.Sprintf("Removed %s; kept branch %s", m.displayPath(m.target.Path), m.target.Branch), false)
			m.mode = modeList
			m.busy = true
			return m, m.load()
		}
		m.mode = modeList
		m.setStatus("Cancelled", false)
		return m, nil
	}

	switch m.mode {
	case modeConfirmDelete:
		switch key {
		case "y":
			m.delBranch = false
		case "b":
			if m.target.Branch == "" {
				return m, nil
			}
			m.delBranch = true
		default:
			return m, nil
		}
		m.busy = true
		return m, m.remove(false)
	case modeConfirmForce:
		if key == "y" {
			m.busy = true
			return m, m.remove(true)
		}
	case modeConfirmBranchForce:
		if key == "y" {
			m.busy = true
			return m, m.deleteBranch(true)
		}
	}
	return m, nil
}

func (m model) remove(force bool) tea.Cmd {
	path := m.target.Path
	return func() tea.Msg { return removedMsg{force, m.repo.Remove(path, force)} }
}

func (m model) deleteBranch(force bool) tea.Cmd {
	branch := m.target.Branch
	return func() tea.Msg { return branchDeletedMsg{force, m.repo.DeleteBranch(branch, force)} }
}

// listHeight is the number of rows available for worktree entries.
func (m model) listHeight() int {
	if m.height == 0 {
		return 20
	}
	return max(m.height-8, 3)
}

func (m *model) clampScroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(min(m.offset, len(m.worktrees)-h), 0)
}

func (m model) View() string {
	var b strings.Builder

	name := strings.TrimSuffix(filepath.Base(m.mainPath()), ".git")
	b.WriteString(titleStyle.Render("🌳 tree-goblin") + dimStyle.Render("  "+name) + "\n")
	if m.openCmd == "" {
		b.WriteString(dimStyle.Render("open: $SHELL (default)") + "\n\n")
	} else {
		b.WriteString(dimStyle.Render("open: ") + m.openCmd + dimStyle.Render("  ("+m.openScope+")") + "\n\n")
	}

	switch m.mode {
	case modeCreate:
		b.WriteString(m.viewCreate())
	case modeSetCommand:
		b.WriteString(m.viewSetCommand())
	default:
		b.WriteString(m.viewList())
	}

	b.WriteString("\n")
	if m.status != "" {
		if m.statusErr {
			b.WriteString(errStyle.Render(m.status))
		} else {
			b.WriteString(okStyle.Render(m.status))
		}
	}
	b.WriteString("\n" + m.viewHelp())

	if m.width > 0 {
		lines := strings.Split(b.String(), "\n")
		trunc := lipgloss.NewStyle().MaxWidth(m.width)
		for i, l := range lines {
			lines[i] = trunc.Render(l)
		}
		return strings.Join(lines, "\n")
	}
	return b.String()
}

func (m model) viewList() string {
	if len(m.worktrees) == 0 {
		if m.busy {
			return dimStyle.Render("  loading…") + "\n"
		}
		return dimStyle.Render("  no worktrees") + "\n"
	}

	branchW, flagsW := 0, 0
	for _, wt := range m.worktrees {
		branchW = max(branchW, lipgloss.Width(branchLabel(wt)))
		flagsW = max(flagsW, lipgloss.Width(m.flags(wt)))
	}

	var b strings.Builder
	end := min(m.offset+m.listHeight(), len(m.worktrees))
	for i := m.offset; i < end; i++ {
		wt := m.worktrees[i]
		cursor := "  "
		bs, ps := branchStyle, lipgloss.NewStyle()
		if i == m.cursor {
			cursor = selStyle.Render("▸ ")
			bs, ps = selStyle, selStyle
		}
		head := wt.Head
		if len(head) > 7 {
			head = head[:7]
		}
		b.WriteString(cursor +
			bs.Render(pad(branchLabel(wt), branchW)) + "  " +
			pad(m.flags(wt), flagsW) + "  " +
			dimStyle.Render(head) + "  " +
			ps.Render(m.displayPath(wt.Path)) + "\n")
	}
	if len(m.worktrees) > m.listHeight() {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d", m.cursor+1, len(m.worktrees))) + "\n")
	}

	switch m.mode {
	case modeConfirmDelete:
		q := fmt.Sprintf("Remove worktree %s?", m.displayPath(m.target.Path))
		opts := keyStyle.Render("y") + " remove  "
		if m.target.Branch != "" {
			opts += keyStyle.Render("b") + " remove + delete branch " + branchStyle.Render(m.target.Branch) + "  "
		}
		opts += keyStyle.Render("n") + " cancel"
		b.WriteString("\n" + boxStyle.Render(warnStyle.Render(q)+"\n"+opts) + "\n")
	case modeConfirmForce:
		b.WriteString("\n" + boxStyle.Width(m.boxWidth()).Render(errStyle.Render(m.lastErr)+"\n"+
			warnStyle.Render("Force remove? Uncommitted changes will be lost.")+"\n"+
			keyStyle.Render("y")+" force remove  "+keyStyle.Render("n")+" cancel") + "\n")
	case modeConfirmBranchForce:
		b.WriteString("\n" + boxStyle.Width(m.boxWidth()).Render(errStyle.Render(m.lastErr)+"\n"+
			warnStyle.Render("Force delete branch "+m.target.Branch+"?")+"\n"+
			keyStyle.Render("y")+" delete (-D)  "+keyStyle.Render("n")+" keep branch") + "\n")
	}
	return b.String()
}

func (m model) flags(wt Worktree) string {
	var f []string
	if m.repo.Current != "" && realpath(wt.Path) == m.repo.Current {
		f = append(f, okStyle.Render("● here"))
	}
	if wt.Main {
		f = append(f, dimStyle.Render("main"))
	}
	if wt.Dirty > 0 {
		f = append(f, warnStyle.Render(fmt.Sprintf("✚%d", wt.Dirty)))
	}
	if wt.Locked {
		f = append(f, warnStyle.Render("locked"))
	}
	if wt.Prunable {
		f = append(f, errStyle.Render("missing (p to prune)"))
	}
	return strings.Join(f, " ")
}

func (m model) boxWidth() int { return min(max(m.width-2, 40), 100) }

// displayPath shows p relative to the directory containing the main worktree.
func (m model) displayPath(p string) string {
	if rel, err := filepath.Rel(filepath.Dir(m.mainPath()), p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return tildify(p)
}

func (m model) viewCreate() string {
	labels := []string{"Branch", "Base  ", "Path  "}
	var rows []string
	rows = append(rows, titleStyle.Render("New worktree"), "")
	for i, in := range m.inputs {
		label := dimStyle.Render(labels[i])
		if i == m.focus {
			label = selStyle.Render(labels[i])
		}
		rows = append(rows, label+"  "+in.View())
	}
	return boxStyle.Width(m.boxWidth()).Render(strings.Join(rows, "\n")) + "\n"
}

func (m model) viewSetCommand() string {
	scope := "repo"
	if m.cmdGlobal {
		scope = "global"
	}
	rows := []string{
		titleStyle.Render("Open command"),
		dimStyle.Render("Runs in the worktree dir. Placeholders: {path} {branch} {name}; env: $TG_PATH $TG_BRANCH"),
		"",
		m.cmdInput.View(),
		"",
		dimStyle.Render("save to: ") + selStyle.Render(scope),
	}
	return boxStyle.Width(m.boxWidth()).Render(strings.Join(rows, "\n")) + "\n"
}

func (m model) viewHelp() string {
	var keys [][2]string
	switch m.mode {
	case modeList:
		keys = [][2]string{{"↑↓", "move"}, {"enter", "open"}, {"n", "new"}, {"d", "delete"}, {"c", "set command"}, {"p", "prune"}, {"r", "refresh"}, {"q", "quit"}}
	case modeCreate:
		keys = [][2]string{{"tab", "next field"}, {"enter", "create"}, {"esc", "cancel"}}
	case modeSetCommand:
		keys = [][2]string{{"enter", "save"}, {"tab", "toggle repo/global"}, {"esc", "cancel"}}
	default:
		return ""
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = keyStyle.Render(k[0]) + dimStyle.Render(" "+k[1])
	}
	return strings.Join(parts, dimStyle.Render(" • "))
}

func branchLabel(wt Worktree) string {
	switch {
	case wt.Bare:
		return "(bare)"
	case wt.Branch != "":
		return wt.Branch
	default:
		return "(detached)"
	}
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

func tildify(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + p[len(home):]
	}
	return p
}
