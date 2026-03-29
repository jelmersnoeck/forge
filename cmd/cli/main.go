// forge cli — interactive TUI for the forge server.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/jelmersnoeck/forge/internal/types"
)

var (
	headerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	dimStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	toolStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	errorStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	promptStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	queueStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	queueHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	userMsgStyle     = lipgloss.NewStyle().
				Background(lipgloss.Color("236")).
				Foreground(lipgloss.Color("15")).
				Padding(0, 1).
				Bold(true)
	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("8")).
				Padding(0, 1)
)

type model struct {
	server       string
	sessionID    string
	input        string
	queue        []string
	output       []string
	ready        bool
	quitting     bool
	exitAttempts int  // track number of exit attempts
	working      bool // track if agent is currently working
	width        int
	height       int
	renderer     *glamour.TermRenderer
	textBuf      string
	err          error
}

type serverEvent types.OutboundEvent
type errMsg error

func main() {
	resume := flag.String("resume", "", "session ID to resume")
	flag.Parse()

	server := os.Getenv("FORGE_URL")
	if server == "" {
		server = "http://localhost:3000"
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("could not determine working directory: "+err.Error()))
		os.Exit(1)
	}

	var sessionID string
	if *resume != "" {
		sessionID = *resume
	} else {
		sid, err := createSession(server, cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("could not connect to forge server at "+server))
			fmt.Fprintf(os.Stderr, "  %v\n\nhint: start the server with `just dev-server`\n", err)
			os.Exit(1)
		}
		sessionID = sid
	}

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)

	m := model{
		server:    server,
		sessionID: sessionID,
		output:    []string{},
		queue:     []string{},
		renderer:  renderer,
	}

	// Add welcome message
	m.output = append(m.output,
		headerStyle.Render("forge cli")+" "+dimStyle.Render("— session "+sessionID),
		dimStyle.Render("server: "+server),
		"",
		dimStyle.Render("Press Ctrl+C to interrupt work, twice to exit. Session: forge-cli --resume "+sessionID),
		"",
	)

	p := tea.NewProgram(m, tea.WithAltScreen())

	// Start SSE listener in background
	go listenEvents(p, server, sessionID)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			// First exit attempt: send interrupt if agent is working
			if m.exitAttempts == 0 {
				m.exitAttempts++
				if m.working {
					// Send interrupt to agent
					m.output = append(m.output, "", queueStyle.Render("⚠ Interrupting agent... (press Ctrl+C again to exit)"))
					return m, m.sendInterrupt()
				} else {
					// Not working, just show the message
					m.output = append(m.output, "", dimStyle.Render("(press Ctrl+C again to exit)"))
					return m, nil
				}
			}
			// Second exit attempt: actually quit
			m.quitting = true
			return m, tea.Quit

		case tea.KeyEnter:
			if m.input == "" {
				return m, nil
			}
			text := m.input
			m.input = "" // Clear input immediately
			m.exitAttempts = 0 // Reset exit attempts on new message
			
			// If nothing in queue and not working, send immediately without queuing
			if len(m.queue) == 0 && !m.working {
				// Display the user's message in the output with distinct styling
				m.output = append(m.output, "", userMsgStyle.Render("You: ")+text)
				return m, m.sendMessage(text)
			}
			
			// Otherwise, add to queue (will be sent when current work completes)
			m.queue = append(m.queue, text)
			return m, nil

		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			m.exitAttempts = 0 // Reset on any input
			return m, nil

		case tea.KeySpace:
			m.input += " "
			m.exitAttempts = 0 // Reset on any input
			return m, nil

		case tea.KeyRunes:
			m.input += string(msg.Runes)
			m.exitAttempts = 0 // Reset on any input
			return m, nil
		}

	case serverEvent:
		event := types.OutboundEvent(msg)
		m.handleEvent(event)

		// Track working state
		switch event.Type {
		case "text", "tool_use":
			m.working = true
			m.exitAttempts = 0 // reset exit attempts when work starts
		case "done", "error":
			m.working = false
		}

		// If done and queue has messages, send next
		if event.Type == "done" && len(m.queue) > 0 {
			text := m.queue[0]
			m.queue = m.queue[1:]
			// Display the user's message in the output with distinct styling
			m.output = append(m.output, userMsgStyle.Render("You: ")+text)
			return m, m.sendMessage(text)
		}

		return m, nil

	case errMsg:
		m.err = msg
		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	if m.quitting {
		return dimStyle.Render("To resume: forge-cli --resume " + m.sessionID + "\n")
	}

	// Calculate available space
	queueHeight := 0
	if len(m.queue) > 0 {
		queueHeight = len(m.queue) + 2 // header + messages + separator
	}
	inputHeight := 3 // border + content
	outputHeight := m.height - queueHeight - inputHeight - 1

	// Build output area (scrollable)
	var outputArea string
	if len(m.output) > outputHeight {
		// Show last N lines
		outputArea = strings.Join(m.output[len(m.output)-outputHeight:], "\n")
	} else {
		outputArea = strings.Join(m.output, "\n")
	}

	// Build queue display
	var queueArea string
	if len(m.queue) > 0 {
		queueLines := []string{queueHeaderStyle.Render("Queued messages:")}
		for i, msg := range m.queue {
			prefix := "  "
			if i == 0 {
				prefix = "→ "
			}
			display := msg
			if len(msg) > 80 {
				display = msg[:77] + "..."
			}
			queueLines = append(queueLines, prefix+queueStyle.Render(display))
		}
		queueLines = append(queueLines, "")
		queueArea = strings.Join(queueLines, "\n")
	}

	// Build input area with cursor
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("│")
	var inputContent string
	if m.input == "" {
		inputDisplay := dimStyle.Render("Type your message...")
		inputContent = promptStyle.Render("> ") + cursor + " " + inputDisplay
	} else {
		inputContent = promptStyle.Render("> ") + m.input + cursor
	}
	// Make input area full width (accounting for border and padding)
	inputArea := inputBorderStyle.Width(m.width - 4).Render(inputContent)

	// Combine all areas
	var parts []string
	if outputArea != "" {
		parts = append(parts, outputArea)
	}
	if queueArea != "" {
		parts = append(parts, queueArea)
	}
	parts = append(parts, inputArea)

	return strings.Join(parts, "\n")
}

func (m *model) handleEvent(event types.OutboundEvent) {
	switch event.Type {
	case "text":
		m.textBuf += event.Content

	case "tool_use":
		m.flushText()
		if event.Content != "" {
			m.output = append(m.output, toolStyle.Render("  ["+event.ToolName+"]")+" "+dimStyle.Render(event.Content))
		} else {
			m.output = append(m.output, toolStyle.Render("  ["+event.ToolName+"]"))
		}

	case "queued_task_result":
		m.flushText()
		m.output = append(m.output, queueStyle.Render("  [queued] ")+dimStyle.Render(event.Content))

	case "queued_task_error":
		m.flushText()
		m.output = append(m.output, errorStyle.Render("  [queued error] ")+event.Content)

	case "queue_immediate":
		m.flushText()
		m.output = append(m.output, queueStyle.Render("  ⏱  Queued immediate: ")+dimStyle.Render(event.Content))

	case "queue_on_complete":
		m.flushText()
		m.output = append(m.output, queueStyle.Render("  ⏱  Queued on complete: ")+dimStyle.Render(event.Content))

	case "error":
		m.flushText()
		m.output = append(m.output, errorStyle.Render("error: "+event.Content))

	case "done":
		m.flushText()
		m.output = append(m.output, "")
	}
}

func (m *model) flushText() {
	text := m.textBuf
	m.textBuf = ""
	if text == "" {
		return
	}

	rendered, err := m.renderer.Render(text)
	if err != nil {
		m.output = append(m.output, text)
		return
	}

	// Split into lines and add to output
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	m.output = append(m.output, lines...)
}

func (m model) sendMessage(text string) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"text": text})
		resp, err := http.Post(
			fmt.Sprintf("%s/sessions/%s/messages", m.server, m.sessionID),
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			return errMsg(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			b, _ := io.ReadAll(resp.Body)
			return errMsg(fmt.Errorf("%d %s", resp.StatusCode, b))
		}
		return nil
	}
}

func (m model) sendInterrupt() tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"interrupt": "true"})
		resp, err := http.Post(
			fmt.Sprintf("%s/sessions/%s/interrupt", m.server, m.sessionID),
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			return errMsg(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return errMsg(fmt.Errorf("interrupt failed: %d %s", resp.StatusCode, b))
		}
		return nil
	}
}

func createSession(server, cwd string) (string, error) {
	body, _ := json.Marshal(map[string]string{"cwd": cwd})
	resp, err := http.Post(server+"/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.SessionID, nil
}

func listenEvents(p *tea.Program, server, sessionID string) {
	resp, err := http.Get(fmt.Sprintf("%s/sessions/%s/events", server, sessionID))
	if err != nil {
		p.Send(errMsg(err))
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var event types.OutboundEvent
		if err := json.Unmarshal([]byte(line[6:]), &event); err != nil {
			continue
		}

		p.Send(serverEvent(event))
	}

	if err := scanner.Err(); err != nil {
		p.Send(errMsg(err))
	}
}
