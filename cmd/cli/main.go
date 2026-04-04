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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/jelmersnoeck/forge/internal/runtime/cost"
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
	thinkingStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Italic(true)
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
	server          string
	sessionID       string
	interactiveMode bool // true if talking directly to agent, false if via gateway
	input           string
	queue           []string
	output          []string
	ready           bool
	quitting        bool
	exitAttempts    int  // track number of exit attempts
	working         bool // track if agent is currently working
	thinking        bool // track if agent is currently thinking
	spinnerFrame    int  // spinner animation frame
	width           int
	height          int
	renderer        *glamour.TermRenderer
	textBuf         string
	err             error
	scrollOffset    int  // how many lines scrolled up from bottom
	autoScroll      bool // auto-scroll to bottom on new content

	// Cost tracking
	totalUsage types.TokenUsage // session total usage
	modelName  string           // model name for cost calculation
}

type serverEvent types.OutboundEvent
type errMsg error
type tickMsg time.Time

func main() {
	resume := flag.String("resume", "", "session ID to resume")
	server := flag.String("server", "", "connect to remote forge server (e.g. http://localhost:3000)")
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("could not determine working directory: "+err.Error()))
		os.Exit(1)
	}

	// Determine mode: interactive (default) or remote server
	var sessionID string
	var serverURL string
	var agentCleanup func()

	if *server != "" {
		// Remote server mode
		serverURL = *server
		if *resume != "" {
			sessionID = *resume
		} else {
			sid, err := createSession(serverURL, cwd)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("could not connect to forge server at "+serverURL))
				fmt.Fprintf(os.Stderr, "  %v\n\nhint: start the server with `just dev-server`\n", err)
				os.Exit(1)
			}
			sessionID = sid
		}
	} else {
		// Interactive mode (default) - spawn local agent
		if *resume != "" {
			fmt.Fprintln(os.Stderr, errorStyle.Render("cannot resume in interactive mode"))
			fmt.Fprintln(os.Stderr, "  hint: use --server to connect to a persistent server")
			os.Exit(1)
		}

		sid, url, cleanup, err := spawnLocalAgent(cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("failed to spawn local agent: "+err.Error()))
			os.Exit(1)
		}
		sessionID = sid
		serverURL = url
		agentCleanup = cleanup
		defer agentCleanup()
	}

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)

	m := model{
		server:          serverURL,
		sessionID:       sessionID,
		interactiveMode: (*server == ""),
		output:          []string{},
		queue:           []string{},
		renderer:        renderer,
		autoScroll:      true, // start with auto-scroll enabled
	}

	// Add welcome message
	modeDesc := "interactive"
	resumeHint := ""
	if *server != "" {
		modeDesc = "remote"
		resumeHint = dimStyle.Render("Press Ctrl+C to interrupt work, twice to exit. Resume: forge-cli --server " + *server + " --resume " + sessionID)
	} else {
		resumeHint = dimStyle.Render("Press Ctrl+C to interrupt work, twice to exit. (ephemeral session)")
	}

	m.output = append(m.output,
		headerStyle.Render("forge cli")+" "+dimStyle.Render("— "+modeDesc+" — session "+sessionID),
		dimStyle.Render("server: "+serverURL),
		"",
		resumeHint,
		"",
	)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Start SSE listener in background
	go listenEvents(p, serverURL, sessionID, m.interactiveMode)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (m model) Init() tea.Cmd {
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.thinking {
			m.spinnerFrame++
		}
		return m, tick()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			// Scroll up (like pressing up arrow)
			outputHeight := m.getOutputHeight()
			maxOffset := len(m.output) - outputHeight
			if maxOffset > 0 && m.scrollOffset < maxOffset {
				m.scrollOffset += 3 // scroll 3 lines at a time for smoother trackpad feel
				if m.scrollOffset > maxOffset {
					m.scrollOffset = maxOffset
				}
				m.autoScroll = false
			}
			return m, nil

		case tea.MouseButtonWheelDown:
			// Scroll down (like pressing down arrow)
			if m.scrollOffset > 0 {
				m.scrollOffset -= 3 // scroll 3 lines at a time
				if m.scrollOffset < 0 {
					m.scrollOffset = 0
				}
				if m.scrollOffset == 0 {
					m.autoScroll = true
				}
			}
			return m, nil
		}

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

		case tea.KeyUp:
			// Scroll up one line
			outputHeight := m.getOutputHeight()
			maxOffset := len(m.output) - outputHeight
			if maxOffset > 0 && m.scrollOffset < maxOffset {
				m.scrollOffset++
				m.autoScroll = false
			}
			return m, nil

		case tea.KeyDown:
			// Scroll down one line
			if m.scrollOffset > 0 {
				m.scrollOffset--
				if m.scrollOffset == 0 {
					m.autoScroll = true
				}
			}
			return m, nil

		case tea.KeyEnter:
			if m.input == "" {
				return m, nil
			}
			text := m.input
			m.input = ""       // Clear input immediately
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
		case "thinking":
			m.thinking = true
		case "text", "tool_use":
			m.thinking = false
			m.working = true
			m.exitAttempts = 0 // reset exit attempts when work starts
		case "done", "error":
			m.thinking = false
			m.working = false
		}

		// Auto-scroll to bottom on new content
		if m.autoScroll {
			m.scrollOffset = 0
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

func (m model) getOutputHeight() int {
	queueHeight := 0
	if len(m.queue) > 0 {
		queueHeight = len(m.queue) + 2 // header + messages + separator
	}
	thinkingHeight := 0
	if m.thinking {
		thinkingHeight = 1 // thinking indicator
	}
	costHeight := 0
	if m.totalUsage.InputTokens > 0 || m.totalUsage.OutputTokens > 0 {
		costHeight = 1 // cost tracker line (below input)
	}
	inputHeight := 3 // border + content
	return m.height - queueHeight - thinkingHeight - costHeight - inputHeight - 1
}

func (m model) spinner() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[m.spinnerFrame%len(frames)]
}

func (m model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	if m.quitting {
		return dimStyle.Render("To resume: forge-cli --resume " + m.sessionID + "\n")
	}

	// Calculate available space
	outputHeight := m.getOutputHeight()

	// Build output area (scrollable)
	var outputArea string
	if len(m.output) > outputHeight {
		// Calculate which slice of output to show based on scroll offset
		endIdx := len(m.output) - m.scrollOffset
		startIdx := endIdx - outputHeight
		if startIdx < 0 {
			startIdx = 0
		}
		outputArea = strings.Join(m.output[startIdx:endIdx], "\n")
	} else {
		outputArea = strings.Join(m.output, "\n")
	}

	// Build thinking indicator
	var thinkingIndicator string
	if m.thinking {
		thinkingIndicator = thinkingStyle.Render(m.spinner() + " thinking...")
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

	// Build cost tracker (below input, transparent)
	costTrackerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Faint(true)
	var costTracker string
	if m.totalUsage.InputTokens > 0 || m.totalUsage.OutputTokens > 0 {
		tokens := fmt.Sprintf("in: %d | out: %d", m.totalUsage.InputTokens, m.totalUsage.OutputTokens)
		totalCost := cost.Calculate(m.modelName, m.totalUsage)
		costStr := cost.FormatCost(totalCost)
		costTracker = costTrackerStyle.Render(fmt.Sprintf("  %s | %s", tokens, costStr))
	}

	// Combine all areas
	var parts []string
	if outputArea != "" {
		parts = append(parts, outputArea)
	}
	if thinkingIndicator != "" {
		parts = append(parts, thinkingIndicator)
	}
	if queueArea != "" {
		parts = append(parts, queueArea)
	}
	parts = append(parts, inputArea)
	if costTracker != "" {
		parts = append(parts, costTracker)
	}

	return strings.Join(parts, "\n")
}

func (m *model) handleEvent(event types.OutboundEvent) {
	switch event.Type {
	case "model":
		m.modelName = event.Content

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

	case "usage":
		// Loop sends cumulative totalUsage
		if event.Usage != nil {
			m.totalUsage = *event.Usage
		}
		// Track model name for cost calculation
		if event.Model != "" {
			m.modelName = event.Model
		}

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

		var url string
		if m.interactiveMode {
			url = fmt.Sprintf("%s/messages", m.server)
		} else {
			url = fmt.Sprintf("%s/sessions/%s/messages", m.server, m.sessionID)
		}

		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return errMsg(err)
		}
		defer func() { _ = resp.Body.Close() }()

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

		var url string
		if m.interactiveMode {
			url = fmt.Sprintf("%s/interrupt", m.server)
		} else {
			url = fmt.Sprintf("%s/sessions/%s/interrupt", m.server, m.sessionID)
		}

		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return errMsg(err)
		}
		defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.SessionID, nil
}

func listenEvents(p *tea.Program, server, sessionID string, interactiveMode bool) {
	var url string
	if interactiveMode {
		url = fmt.Sprintf("%s/events", server)
	} else {
		url = fmt.Sprintf("%s/sessions/%s/events", server, sessionID)
	}

	resp, err := http.Get(url)
	if err != nil {
		p.Send(errMsg(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

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

// spawnLocalAgent starts a forge-agent process and returns (sessionID, serverURL, cleanup, error).
// The agent runs on a random port and auto-terminates when cleanup is called.
func spawnLocalAgent(cwd string) (string, string, func(), error) {
	// Find forge-agent binary (prefer same dir as CLI, fallback to PATH)
	agentBin := "forge-agent"
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "forge-agent")
		if _, err := os.Stat(candidate); err == nil {
			agentBin = candidate
		}
	}

	// Generate session ID
	sessionID := "cli-" + time.Now().Format("20060102-150405")

	// Spawn agent on random port (0 = OS picks)
	cmd := exec.Command(agentBin,
		"--port", "0",
		"--cwd", cwd,
		"--session-id", sessionID,
	)

	// Capture stdout to read the port
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Send stderr to /dev/null (agent logs are noise in interactive mode)
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return "", "", nil, fmt.Errorf("start agent: %w", err)
	}

	// Read port from first line of stdout (JSON: {"port": 12345})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		_ = cmd.Process.Kill()
		return "", "", nil, fmt.Errorf("agent did not emit port")
	}

	var portMsg struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &portMsg); err != nil {
		_ = cmd.Process.Kill()
		return "", "", nil, fmt.Errorf("parse agent port: %w", err)
	}

	serverURL := fmt.Sprintf("http://localhost:%d", portMsg.Port)

	// Wait for agent to be ready (health check with retries)
	ready := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get(serverURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !ready {
		_ = cmd.Process.Kill()
		return "", "", nil, fmt.Errorf("agent did not become healthy")
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	return sessionID, serverURL, cleanup, nil
}
