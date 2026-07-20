package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── ANSI helpers ─────────────────────────────────────────────────────────────

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b][^\x07]*\x07|\r|\x1b[()][AB012]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// fitWidth strips ANSI from s, truncates to w runes, then pads to exactly w.
func fitWidth(s string, w int) string {
	s = stripANSI(s)
	r := []rune(s)
	if len(r) > w {
		return string(r[:w])
	}
	return s + strings.Repeat(" ", w-len(r))
}

// ── Messages ─────────────────────────────────────────────────────────────────

type lineMsg struct {
	idx  int
	line string
}
type exitMsg struct {
	idx  int
	code int
}
type allDoneMsg struct{}
type tickMsg struct{}

// ── Panel ────────────────────────────────────────────────────────────────────

type panel struct {
	label      string
	cmd        string
	lines      []string
	mu         sync.RWMutex
	scroll     int
	autoScroll bool
	status     string // "running" | "done" | "failed"
	exitCode   int
}

func newPanel(label, cmd string) *panel {
	return &panel{label: label, cmd: cmd, autoScroll: true, status: "running"}
}

func (p *panel) push(line string) {
	p.mu.Lock()
	p.lines = append(p.lines, line)
	p.mu.Unlock()
}

func (p *panel) snap() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := make([]string, len(p.lines))
	copy(cp, p.lines)
	return cp
}

// ── Runner ───────────────────────────────────────────────────────────────────

func runPanel(idx int, p *panel, ch chan<- tea.Msg) {
	r, w, err := os.Pipe()
	if err != nil {
		ch <- exitMsg{idx, 1}
		return
	}
	cmd := exec.Command("sh", "-c", p.cmd)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		w.Close()
		r.Close()
		ch <- exitMsg{idx, 1}
		return
	}
	w.Close()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 512*1024), 512*1024)
	for sc.Scan() {
		ch <- lineMsg{idx, stripANSI(sc.Text())}
	}
	r.Close()

	code := 0
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	ch <- exitMsg{idx, code}
}

// ── Styles ───────────────────────────────────────────────────────────────────

var (
	hdrActive = lipgloss.NewStyle().
			Background(lipgloss.Color("6")).
			Foreground(lipgloss.Color("0")).
			Bold(true)
	hdrIdle = lipgloss.NewStyle().
		Background(lipgloss.Color("8")).
		Foreground(lipgloss.Color("15"))
	barStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("0")).
			Foreground(lipgloss.Color("7"))
	divStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func statusFrame(p *panel, tick int) string {
	switch p.status {
	case "done":
		return "✓"
	case "failed":
		return "✗"
	default:
		return spinnerFrames[tick%len(spinnerFrames)]
	}
}

// ── Model ────────────────────────────────────────────────────────────────────

type model struct {
	panels  []*panel
	focus   int
	width   int
	height  int
	tick    int
	ch      chan tea.Msg
	running int
}

func newModel(panels []*panel) model {
	ch := make(chan tea.Msg, 4096)
	var wg sync.WaitGroup
	for i, p := range panels {
		wg.Add(1)
		go func(idx int, p *panel) {
			defer wg.Done()
			runPanel(idx, p, ch)
		}(i, p)
	}
	go func() {
		wg.Wait()
		ch <- allDoneMsg{}
	}()
	return model{panels: panels, ch: ch, running: len(panels)}
}

func listenCh(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(listenCh(m.ch), tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	n := len(m.panels)
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tickMsg:
		m.tick++
		return m, tickCmd()

	case lineMsg:
		m.panels[msg.idx].push(msg.line)
		return m, listenCh(m.ch)

	case exitMsg:
		p := m.panels[msg.idx]
		p.mu.Lock()
		p.exitCode = msg.code
		if msg.code == 0 {
			p.status = "done"
		} else {
			p.status = "failed"
		}
		p.mu.Unlock()
		m.running--
		return m, listenCh(m.ch)

	case allDoneMsg:
		return m, nil

	case tea.KeyMsg:
		p := m.panels[m.focus]
		contentH := m.height - 2
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.focus = (m.focus + 1) % n
		case "shift+tab", "left", "h":
			m.focus = (m.focus - 1 + n) % n
		case "up", "k":
			p.scroll = imax(0, p.scroll-1)
			p.autoScroll = false
		case "down", "j":
			lines := p.snap()
			ms := imax(0, len(lines)-contentH)
			if p.scroll < ms {
				p.scroll++
			} else {
				p.autoScroll = true
			}
		case "pgup", "ctrl+u":
			p.scroll = imax(0, p.scroll-contentH)
			p.autoScroll = false
		case "pgdown", "ctrl+d":
			lines := p.snap()
			ms := imax(0, len(lines)-contentH)
			p.scroll = imin(ms, p.scroll+contentH)
			if p.scroll >= ms {
				p.autoScroll = true
			}
		case "g":
			p.scroll = 0
			p.autoScroll = false
		case "G":
			p.autoScroll = true
		}
	}
	return m, nil
}

func (m model) renderHeader(p *panel, w int, focused bool) string {
	frame := statusFrame(p, m.tick)
	text := " " + frame + " " + p.label + " "
	r := []rune(text)
	switch {
	case len(r) > w:
		text = string(r[:w])
	case len(r) < w:
		text += strings.Repeat(" ", w-len(r))
	}
	if focused {
		return hdrActive.Render(text)
	}
	return hdrIdle.Render(text)
}

func (m model) View() string {
	if m.width == 0 {
		return "Initializing…"
	}

	n := len(m.panels)
	contentH := m.height - 2 // header row + status bar

	// Distribute width: subtract N-1 divider columns, split evenly.
	available := m.width - (n - 1)
	base := available / n
	widths := make([]int, n)
	for i := range widths {
		widths[i] = base
	}
	widths[n-1] += available - base*n // remainder goes to last panel

	var sb strings.Builder

	for row := -1; row < contentH; row++ {
		for i, p := range m.panels {
			w := widths[i]

			var cell string
			if row == -1 {
				cell = m.renderHeader(p, w, i == m.focus)
			} else {
				lines := p.snap()
				if p.autoScroll {
					p.scroll = imax(0, len(lines)-contentH)
				}
				ms := imax(0, len(lines)-contentH)
				if p.scroll > ms {
					p.scroll = ms
				}
				lineIdx := p.scroll + row
				if lineIdx >= 0 && lineIdx < len(lines) {
					cell = fitWidth(lines[lineIdx], w)
				} else {
					cell = strings.Repeat(" ", w)
				}
			}

			sb.WriteString(cell)
			if i < n-1 {
				sb.WriteString(divStyle.Render("│"))
			}
		}
		sb.WriteByte('\n')
	}

	// Status bar
	okCount := 0
	for _, p := range m.panels {
		if p.status == "done" {
			okCount++
		}
	}
	var barText string
	if m.running == 0 {
		barText = fmt.Sprintf(" Done: %d/%d succeeded  [q]uit  [tab/←→/h/l]focus  [↑↓/PgUp/PgDn/j/k]scroll  [g/G]top/end", okCount, n)
	} else {
		barText = fmt.Sprintf(" Running (%d active)  [q]uit  [tab/←→/h/l]focus  [↑↓/PgUp/PgDn/j/k]scroll  [g/G]top/end", m.running)
	}
	sb.WriteString(barStyle.Render(fitWidth(barText, m.width)))

	return sb.String()
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// runTUI launches the side-by-side TUI for the given panels and blocks until
// the user quits (or all panels finish and the user acknowledges).
// Returns true if all panels succeeded, false if any failed.
func runTUI(panels []*panel) bool {
	prog := tea.NewProgram(newModel(panels), tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		return false
	}

	fmt.Println()
	fmt.Println("=== Parallel Summary ===")
	ok := true
	for _, p := range panels {
		if p.status == "done" {
			fmt.Printf("  ✓  %s\n", p.label)
		} else {
			fmt.Printf("  ✗  %s  (exit %d)\n", p.label, p.exitCode)
			ok = false
		}
	}
	return ok
}
