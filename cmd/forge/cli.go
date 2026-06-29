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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/jelmersnoeck/forge/internal/config"
	"github.com/jelmersnoeck/forge/internal/credentials"
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

// taskTracker holds live state for a background task being polled by the LLM.
// Instead of showing repeated [TaskGet] lines, the CLI renders a single
// spinner + description block with rolling output for each tracked task.
type taskTracker struct {
	taskID      string
	description string
	status      string
	outputTail  []string  // last 5 lines of output
	startTime   time.Time // for duration display on completion
	duration    string    // set when terminal
}

type model struct {
	gateway         string
	sessionID       string
	interactiveMode bool // true if talking directly to agent, false if via gateway
	textArea        textarea.Model
	queue           []string
	ready           bool
	quitting        bool
	exitAttempts    int    // track number of exit attempts
	working         bool   // track if agent is currently working
	thinking        bool   // track if agent is currently thinking
	toolProgress    string // current tool progress message (cleared on next event)
	spinnerFrame    int    // spinner animation frame
	width           int
	height          int
	err             error

	// Extracted subsystems. out owns scrollback + scroll state, tasks owns the
	// live task trackers, costAcc owns cumulative usage + model name, and events
	// owns the event-handling switch + streaming-text state.
	out         *OutputBuffer
	tasks       *TaskTrackerSet
	costAcc     *CostAccumulator
	events      *EventHandler
	renderer    *glamour.TermRenderer
	costTracker *cost.Tracker // persistent cost tracker (passed to events)

	// Worktree info
	worktreePath   string // path to worktree if created
	worktreeBranch string // branch name if worktree created
	cwd            string // working directory (worktree or original)

	// Session title (human-readable, may differ from sessionID)
	sessionTitle   string
	titleGenerated bool // whether we've already tried generating a title

	// Spec mode
	initialPrompt string // auto-sent on startup (e.g. from --spec)

	// PR tracking
	prURL string // PR URL from pr_monitor or orchestrator finalize

	// cursor blink cmd captured from ta.Focus() before model creation
	cursorBlinkCmd tea.Cmd

	// Model selector overlay (triggered by bare /model)
	modelSelectorActive  bool          // selector overlay is showing
	modelSelectorLoading bool          // still fetching the model list
	modelSelectorItems   []modelChoice // selectable models
	modelSelectorCursor  int           // highlighted index
}

// modelChoice is a single selectable entry in the /model selector overlay.
type modelChoice struct {
	id    string // value passed to set the model (alias or full ID)
	label string // human-readable display label
}

type serverEvent types.OutboundEvent
type errMsg error
type tickMsg time.Time
type sessionTitleMsg string // async session title from Haiku

func runCLI(args []string) int {
	fs := flag.NewFlagSet("forge", flag.ExitOnError)
	resume := fs.String("resume", "", "session ID to resume")
	gatewayFlag := fs.String("gateway", "", "connect to remote forge gateway (e.g. http://localhost:3000)")
	serverFlag := fs.String("server", "", "deprecated: use --gateway instead")
	skipWorktree := fs.Bool("skip-worktree", false, "skip worktree creation in interactive mode")
	specPath := fs.String("spec", "", "path to a spec file to implement directly")
	issue := fs.String("issue", "", "GitHub issue URL or #N to use as initial prompt")
	branch := fs.String("branch", "", "branch to check out (reuses existing worktree if found)")
	mode := fs.String("mode", "", "agent mode: swe (default), spec, code, review")
	modelFlag := fs.String("model", "", "model to use (e.g. sonnet, opus, claude-sonnet-4-20250514)")
	_ = fs.Parse(args[1:])

	if *branch != "" && *skipWorktree {
		fmt.Fprintln(os.Stderr, errorStyle.Render("cannot use --branch with --skip-worktree"))
		os.Exit(1)
	}

	// Validate --mode flag.
	switch *mode {
	case "", "swe", "spec", "code", "review":
		// valid
	default:
		fmt.Fprintln(os.Stderr, errorStyle.Render("invalid --mode: "+*mode+" (valid: swe, spec, code, review)"))
		os.Exit(1)
	}

	// Validate mode/spec conflicts.
	if *mode == "spec" && *specPath != "" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("cannot use --spec with --mode spec (spec creator writes specs, it doesn't consume them)"))
		os.Exit(1)
	}
	if *mode == "code" && *specPath == "" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("coder mode requires a spec (use --spec path/to/spec.md)"))
		os.Exit(1)
	}

	// Validate --issue conflicts.
	if *issue != "" && *specPath != "" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("cannot use --issue with --spec"))
		os.Exit(1)
	}
	if *issue != "" && *mode == "spec" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("cannot use --issue with --mode spec"))
		os.Exit(1)
	}

	// Default mode: always swe. The orchestrator runs spec → code → review.
	effectiveMode := *mode
	if effectiveMode == "" {
		effectiveMode = "swe"
	}

	// Handle deprecated --server flag
	if *serverFlag != "" {
		fmt.Fprintln(os.Stderr, "note: --server is deprecated, use --gateway")
		if *gatewayFlag == "" {
			*gatewayFlag = *serverFlag
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("could not determine working directory: "+err.Error()))
		os.Exit(1)
	}

	// Determine mode: interactive (default) or remote gateway
	var sessionID string
	var gatewayURL string
	var agentCleanup func()
	var worktreePath string
	var worktreeBranch string

	// Handle --spec: read spec file and prepare initial prompt.
	// Done early so the prompt is available for session naming.
	var initialPrompt string
	var namingHint string // override for session name generation (e.g. issue title)
	if *specPath != "" {
		specContent, err := os.ReadFile(*specPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("could not read spec file: "+err.Error()))
			os.Exit(1)
		}
		initialPrompt = fmt.Sprintf(
			"Implement the following spec. The spec is the source of truth "+
				"— follow its Behavior, Constraints, and Interfaces sections precisely.\n\n"+
				"Spec file: %s\n\n%s\n\n"+
				"When done, reconcile this spec file with any corrections or "+
				"discoveries made during implementation.",
			*specPath, string(specContent),
		)
	}

	// Handle --issue: fetch GitHub issue and prepare initial prompt.
	var issueNum int
	var issueURL string
	if *issue != "" {
		prompt, title, num, iURL, err := fetchGitHubIssue(*issue, cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
			os.Exit(1)
		}
		initialPrompt = prompt
		namingHint = title
		issueNum = num
		issueURL = iURL
	}

	if *gatewayFlag != "" {
		// Remote gateway mode
		gatewayURL = *gatewayFlag
		if *resume != "" {
			sessionID = *resume
		} else {
			sid, err := createSession(gatewayURL, cwd)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("could not connect to forge gateway at "+gatewayURL))
				fmt.Fprintf(os.Stderr, "  %v\n\nhint: start the gateway with `just dev-gateway`\n", err)
				os.Exit(1)
			}
			sessionID = sid
		}
	} else {
		// Interactive mode (default) - spawn local agent
		if *resume != "" {
			fmt.Fprintln(os.Stderr, errorStyle.Render("cannot resume in interactive mode"))
			fmt.Fprintln(os.Stderr, "  hint: use --gateway to connect to a persistent gateway")
			os.Exit(1)
		}

		sid, url, wtPath, wtBranch, cleanup, err := spawnLocalAgent(cwd, *skipWorktree, *branch, initialPrompt, effectiveMode, *specPath, *modelFlag, namingHint, issueNum, issueURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("failed to spawn local agent: "+err.Error()))
			os.Exit(1)
		}
		sessionID = sid
		gatewayURL = url
		worktreePath = wtPath
		worktreeBranch = wtBranch
		agentCleanup = cleanup
		defer agentCleanup()

		// Setup signal handler to ensure cleanup on interrupt
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			agentCleanup()
			os.Exit(0)
		}()
	}

	// Renderer will be initialized with proper width after first WindowSizeMsg
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80), // initial fallback, will be updated
	)

	// Initialize cost tracker
	costTracker, err := cost.NewTracker()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("warning: could not initialize cost tracker: "+err.Error()))
		// Continue without cost tracking
	}

	// Initialize textarea for multiline input
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Shift+Enter for newline)"
	ta.CharLimit = 0 // no limit
	ta.ShowLineNumbers = false
	ta.Prompt = "> "
	ta.SetHeight(1)
	ta.MaxHeight = 10
	ta.SetWidth(76) // will be updated on WindowSizeMsg
	ta.EndOfBufferCharacter = ' '
	// Remap: Shift+Enter inserts newline, Enter is handled by us for sending
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter", "ctrl+j"),
		key.WithHelp("shift+enter", "insert newline"),
	)
	// Disable textarea's internal viewport scrolling keys — we handle scrolling ourselves
	ta.KeyMap.LineNext = key.NewBinding(key.WithKeys("down"), key.WithHelp("down", ""))
	ta.KeyMap.LinePrevious = key.NewBinding(key.WithKeys("up"), key.WithHelp("up", ""))
	// Style: no visible border (the outer inputBorderStyle provides the border)
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = promptStyle.Inline(true)
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = promptStyle.Inline(true)
	cursorBlinkCmd := ta.Focus()

	// Determine effective working directory
	effectiveCWD := cwd
	if worktreePath != "" {
		effectiveCWD = worktreePath
	}

	// Try to detect an existing PR for the current branch.
	prURL := detectCurrentPR(effectiveCWD)

	out := NewOutputBuffer()
	tasks := NewTaskTrackerSet()
	costAcc := &CostAccumulator{}

	m := model{
		gateway:         gatewayURL,
		sessionID:       sessionID,
		interactiveMode: (*gatewayFlag == ""),
		textArea:        ta,
		queue:           []string{},
		out:             out,
		tasks:           tasks,
		costAcc:         costAcc,
		events:          NewEventHandler(out, tasks, costAcc, costTracker, renderer, 0, sessionID),
		renderer:        renderer,
		costTracker:     costTracker,
		worktreePath:    worktreePath,
		worktreeBranch:  worktreeBranch,
		cwd:             effectiveCWD,
		initialPrompt:   initialPrompt,
		working:         initialPrompt != "", // show spinner immediately for --spec
		sessionTitle:    sessionID,
		titleGenerated:  initialPrompt != "", // already named via Haiku if we had a prompt
		prURL:           prURL,
		cursorBlinkCmd:  cursorBlinkCmd,
	}

	// Add welcome message
	modeDesc := "interactive"
	resumeHint := ""
	if *gatewayFlag != "" {
		modeDesc = "remote"
		resumeHint = dimStyle.Render("Press Ctrl+C to interrupt work, twice to exit. Resume: forge --gateway " + *gatewayFlag + " --resume " + sessionID)
	} else {
		if worktreeBranch != "" {
			resumeHint = dimStyle.Render("Press Ctrl+C to interrupt work, twice to exit. Resume: forge --branch " + worktreeBranch)
		} else {
			resumeHint = dimStyle.Render("Press Ctrl+C to interrupt work, twice to exit.")
		}
	}

	m.out.Append(
		headerStyle.Render("forge cli")+" "+dimStyle.Render("— "+modeDesc+" — "+m.sessionTitle),
		dimStyle.Render("gateway: "+gatewayURL),
	)

	// Add worktree info if present
	if worktreePath != "" {
		m.out.Append(dimStyle.Render("worktree: " + worktreePath))
		if worktreeBranch != "" {
			m.out.Append(dimStyle.Render("branch: " + worktreeBranch))
		}
	}

	// Add mode info if not default
	if effectiveMode != "" {
		m.out.Append(dimStyle.Render("mode: " + effectiveMode))
	}

	// Add spec info if present
	if *specPath != "" {
		m.out.Append(dimStyle.Render("spec: " + *specPath))
		m.working = true // mark as working since we'll auto-send
	}

	// Add issue info if present
	if *issue != "" {
		m.out.Append(dimStyle.Render("issue: " + *issue))
		m.working = true // mark as working since we'll auto-send
	}

	m.out.Append(
		"",
		resumeHint,
		"",
	)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Start SSE listener in background
	go listenEvents(p, gatewayURL, sessionID, m.interactiveMode)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick()}
	if m.cursorBlinkCmd != nil {
		cmds = append(cmds, m.cursorBlinkCmd)
	}
	if m.initialPrompt != "" {
		cmds = append(cmds, m.sendMessage(m.initialPrompt))
	}
	return tea.Batch(cmds...)
}

func tick() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.working || m.tasks.Len() > 0 || m.modelSelectorLoading {
			m.spinnerFrame++
		}
		if m.events.HasPendingText() {
			m.events.flushRawText()
		}
		return m, tick()

	case sessionTitleMsg:
		if title := string(msg); title != "" {
			m.sessionTitle = title
			// Update the header line (first line of output)
			if lines := m.out.Lines(); len(lines) > 0 {
				modeDesc := "interactive"
				if !m.interactiveMode {
					modeDesc = "remote"
				}
				lines[0] = headerStyle.Render("forge cli") + " " + dimStyle.Render("— "+modeDesc+" — "+m.sessionTitle)
			}
		}
		return m, nil

	case modelSwitchedMsg:
		name := string(msg)
		if name != "" {
			m.costAcc.SetModel(name)
			m.out.Append(dimStyle.Render("Model switched to " + name))
		}
		return m, nil

	case modelsListMsg:
		if m.modelSelectorActive {
			m.modelSelectorLoading = false
			m.modelSelectorItems = buildModelChoices(msg)
			if m.modelSelectorCursor >= len(m.modelSelectorItems) {
				m.modelSelectorCursor = 0
			}
			return m, nil
		}
		m.out.Append(renderModelList(msg)...)
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		// Update textarea width
		m.textArea.SetWidth(msg.Width - 6) // account for border + padding
		m.events.SetWidth(msg.Width)
		// Reinitialize renderer with updated width for proper wrapping
		if m.width > 0 {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(m.width-4), // account for padding/margins
			)
			m.events.SetRenderer(m.renderer)
		}
		return m, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			// Scroll up (like pressing up arrow)
			outputHeight := m.getOutputHeight()
			maxOffset := m.out.Len() - outputHeight
			m.out.ScrollUp(3, maxOffset) // 3 lines at a time for smoother trackpad feel
			return m, nil

		case tea.MouseButtonWheelDown:
			// Scroll down (like pressing down arrow)
			m.out.ScrollDown(3) // 3 lines at a time
			return m, nil
		}

	case tea.KeyMsg:
		// Model selector overlay intercepts navigation keys before normal
		// input handling. Picking an entry sets the model globally.
		if m.modelSelectorActive {
			switch msg.Type {
			case tea.KeyUp:
				if !m.modelSelectorLoading && m.modelSelectorCursor > 0 {
					m.modelSelectorCursor--
				}
				return m, nil
			case tea.KeyDown:
				if !m.modelSelectorLoading && m.modelSelectorCursor < len(m.modelSelectorItems)-1 {
					m.modelSelectorCursor++
				}
				return m, nil
			case tea.KeyEsc:
				m.modelSelectorActive = false
				m.modelSelectorLoading = false
				m.modelSelectorItems = nil
				m.out.Append(dimStyle.Render("Model selection cancelled"))
				return m, nil
			case tea.KeyEnter:
				if m.modelSelectorLoading || len(m.modelSelectorItems) == 0 {
					return m, nil
				}
				choice := m.modelSelectorItems[m.modelSelectorCursor]
				m.modelSelectorActive = false
				m.modelSelectorItems = nil
				newM, cmd := m.applyModelSwitch(choice.id, true)
				return newM, cmd
			default:
				// Swallow other keys while the selector is open.
				return m, nil
			}
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			// First exit attempt: send interrupt if agent is working
			if m.exitAttempts == 0 {
				m.exitAttempts++
				if m.working {
					// Send interrupt to agent
					m.out.Append("", queueStyle.Render("⚠ Interrupting agent... (press Ctrl+C again to exit)"))
					return m, m.sendInterrupt()
				} else {
					// Not working, just show the message
					m.out.Append("", dimStyle.Render("(press Ctrl+C again to exit)"))
					return m, nil
				}
			}
			// Second exit attempt: actually quit
			m.quitting = true
			return m, tea.Quit

		case tea.KeyUp:
			// If textarea has multiple lines, let it handle cursor movement
			if m.textArea.LineCount() > 1 {
				var cmd tea.Cmd
				m.textArea, cmd = m.textArea.Update(msg)
				return m, cmd
			}
			// Scroll up one line
			outputHeight := m.getOutputHeight()
			maxOffset := m.out.Len() - outputHeight
			m.out.ScrollUp(1, maxOffset)
			return m, nil

		case tea.KeyDown:
			// If textarea has multiple lines, let it handle cursor movement
			if m.textArea.LineCount() > 1 {
				var cmd tea.Cmd
				m.textArea, cmd = m.textArea.Update(msg)
				return m, cmd
			}
			// Scroll down one line
			m.out.ScrollDown(1)
			return m, nil

		case tea.KeyTab:
			// Slash command tab-completion
			if completed := m.trySlashComplete(); completed {
				return m, nil
			}
			// Otherwise let textarea handle it
			var cmd tea.Cmd
			m.textArea, cmd = m.textArea.Update(msg)
			return m, cmd

		case tea.KeyEnter:
			if m.textArea.Value() == "" {
				return m, nil
			}
			text := m.textArea.Value()
			m.textArea.Reset()
			m.textArea.SetHeight(1) // Reset height after send
			m.exitAttempts = 0      // Reset exit attempts on new message

			// If nothing in queue and not working, send immediately without queuing
			if len(m.queue) == 0 && !m.working {
				// Check for /review command
				if isReviewCommand(text) {
					baseBranch := parseReviewBase(text)
					m.out.Append("")
					m.out.Append(headerStyle.Render("Starting code review...") + " " + dimStyle.Render("("+reviewProviderSummary()+")"))
					m.working = true
					return m, m.sendReview(baseBranch)
				}

				// Check for /model command
				if isModelCommand(text) {
					arg := parseModelArg(text)
					global := parseModelGlobal(text)
					if global && arg == "" {
						m.out.Append("")
						m.out.Append(errorStyle.Render("a model name is required"))
						return m, nil
					}
					switch arg {
					case "":
						// Bare /model opens an interactive selector. Picking an entry
						// sets the model globally (persists model.default).
						if !m.interactiveMode {
							_, display := m.costAcc.Summary()
							if display == "" {
								display = "(not yet known)"
							}
							m.out.Append("")
							m.out.Append(dimStyle.Render("Current model: " + display))
							return m, nil
						}
						m.modelSelectorActive = true
						m.modelSelectorLoading = true
						m.modelSelectorItems = nil
						m.modelSelectorCursor = 0
						m.out.Append("")
						m.out.Append(dimStyle.Render("Fetching available models..."))
						return m, m.fetchModelList()
					case "list":
						m.out.Append("")
						m.out.Append(dimStyle.Render("Fetching available models..."))
						return m, m.fetchModelList()
					default:
						newM, cmd := m.applyModelSwitch(arg, global)
						return newM, cmd
					}
				}

				// Display the user's message in the output with wrapping
				m.out.Append("")
				maxWidth := m.width - 7 // account for "You: "
				if maxWidth < 40 {
					maxWidth = 80
				}
				wrapped := wrapText(text, maxWidth)
				for i, line := range wrapped {
					if i == 0 {
						m.out.Append(userMsgStyle.Render("You: ") + line)
					} else {
						m.out.Append("     " + line)
					}
				}
				m.working = true
				cmds := []tea.Cmd{m.sendMessage(text)}
				// Generate session title from first user message
				if !m.titleGenerated {
					m.titleGenerated = true
					cmds = append(cmds, m.generateTitle(text))
				}
				return m, tea.Batch(cmds...)
			}

			// Agent is busy — send as a steering message (injected mid-turn).
			// Display with a distinct label so the user knows it'll be
			// picked up between LLM iterations, not queued for later.
			m.out.Append("")
			maxWidth := m.width - 7
			if maxWidth < 40 {
				maxWidth = 80
			}
			wrapped := wrapText(text, maxWidth)
			for i, line := range wrapped {
				if i == 0 {
					m.out.Append(thinkingStyle.Render("You (steering): ") + line)
				} else {
					m.out.Append("                " + line)
				}
			}
			return m, m.sendMessage(text)

		default:
			// Let textarea handle all other keys (including navigation)
			var cmd tea.Cmd
			m.textArea, cmd = m.textArea.Update(msg)
			m.exitAttempts = 0 // Reset on any input
			// Auto-resize textarea height based on content
			m.resizeTextArea()
			return m, cmd
		}

	case serverEvent:
		event := types.OutboundEvent(msg)
		result := m.events.Handle(event)
		if result.PRURL != "" {
			m.prURL = result.PRURL
		}

		// Track working state
		switch event.Type {
		case "thinking":
			m.thinking = true
			m.toolProgress = ""
		case "tool_progress":
			// Keep working/thinking state, just update progress
			m.toolProgress = event.Content
		case "text", "tool_use", "task_status", "review_start", "review_finding",
			"phase_start", "phase_handoff", "steering":
			m.thinking = false
			m.working = true
			m.toolProgress = ""
			m.exitAttempts = 0 // reset exit attempts when work starts
		case "done", "error", "interrupted":
			m.thinking = false
			m.working = false
			m.toolProgress = ""
		}

		// Auto-scroll to bottom on new content
		m.out.ResetScrollIfAuto()

		// If done and queue has messages, send next
		if event.Type == "done" && len(m.queue) > 0 {
			text := m.queue[0]
			m.queue = m.queue[1:]
			// Display the user's message in the output with wrapping
			maxWidth := m.width - 7 // account for "You: "
			if maxWidth < 40 {
				maxWidth = 80
			}
			wrapped := wrapText(text, maxWidth)
			for i, line := range wrapped {
				if i == 0 {
					m.out.Append(userMsgStyle.Render("You: ") + line)
				} else {
					m.out.Append("     " + line)
				}
			}
			m.working = true
			return m, m.sendMessage(text)
		}

		return m, nil

	case errMsg:
		m.err = msg
		return m, nil
	}

	// Forward unhandled messages to textarea (cursor blink, etc.)
	var cmd tea.Cmd
	m.textArea, cmd = m.textArea.Update(msg)
	return m, cmd
}

func (m model) getOutputHeight() int {
	queueHeight := 0
	if len(m.queue) > 0 {
		queueHeight = len(m.queue) + 2 // header + messages + separator
	}
	thinkingHeight := 0
	if m.toolProgress != "" || m.thinking || (m.working && !m.events.Streaming()) {
		thinkingHeight = 1 // thinking/progress/working indicator
	}
	trackerHeight := m.tasks.Height()
	inputHeight := m.textArea.LineCount() + 2 // border top/bottom + content lines
	statusHeight := 1                         // cwd + cost line (always shown)
	return m.height - queueHeight - thinkingHeight - trackerHeight - inputHeight - statusHeight - 1
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
		if m.worktreeBranch != "" {
			return dimStyle.Render("Resume: forge --branch "+m.worktreeBranch) + "\n"
		}
		return "\n"
	}

	// Calculate available space
	outputHeight := m.getOutputHeight()

	// Build output area (scrollable)
	outputArea := m.out.View(outputHeight)

	// Build thinking/progress indicator
	// Priority: toolProgress > thinking > working (when no text streaming)
	var thinkingIndicator string
	switch {
	case m.toolProgress != "":
		thinkingIndicator = thinkingStyle.Render(m.spinner() + " " + m.toolProgress)
	case m.thinking:
		thinkingIndicator = thinkingStyle.Render(m.spinner() + " thinking...")
	case m.working && !m.events.Streaming():
		thinkingIndicator = thinkingStyle.Render(m.spinner() + " working...")
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
			// Wrap queue messages instead of truncating
			maxWidth := m.width - 4 // account for prefix
			if maxWidth < 40 {
				maxWidth = 80
			}
			wrapped := wrapText(msg, maxWidth)
			for j, line := range wrapped {
				if j == 0 {
					queueLines = append(queueLines, prefix+queueStyle.Render(line))
				} else {
					// Indent continuation lines
					queueLines = append(queueLines, "  "+queueStyle.Render(line))
				}
			}
		}
		queueLines = append(queueLines, "")
		queueArea = strings.Join(queueLines, "\n")
	}

	// Build input area with textarea component
	inputArea := inputBorderStyle.Width(m.width - 4).Render(
		m.textArea.View(),
	)

	// Build status line below input: cwd (left) | PR (center) | cost (right)
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Faint(true)
	prStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("4")).
		Faint(true)

	cwdDisplay := m.cwd
	// Shorten home directory prefix
	if home, err := os.UserHomeDir(); err == nil {
		cwdDisplay = strings.Replace(cwdDisplay, home, "~", 1)
	}

	totalUsage, modelName := m.costAcc.Summary()
	var costPart string
	if totalUsage.InputTokens > 0 || totalUsage.OutputTokens > 0 {
		tokens := fmt.Sprintf("in: %d | out: %d", totalUsage.InputTokens, totalUsage.OutputTokens)
		totalCost := cost.Calculate(modelName, totalUsage)
		costStr := cost.FormatCost(totalCost)
		costPart = fmt.Sprintf("%s | %s", tokens, costStr)
	}
	if modelName != "" {
		short := shortModelName(modelName)
		if costPart != "" {
			costPart = short + " | " + costPart
		} else {
			costPart = short
		}
	}

	// Build left side: cwd + optional PR link
	leftParts := []string{"  " + cwdDisplay}
	if m.prURL != "" {
		leftParts = append(leftParts, prStyle.Render(m.prURL))
	}
	left := statusStyle.Render(strings.Join(leftParts, "  "))

	var statusLine string
	switch {
	case costPart != "":
		right := statusStyle.Render(costPart + "  ")
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		statusLine = left + strings.Repeat(" ", gap) + right
	default:
		statusLine = left
	}

	// Combine all areas
	var parts []string
	if outputArea != "" {
		parts = append(parts, outputArea)
	}
	// Task progress trackers sit between output and thinking indicator
	if trackerArea := m.tasks.Render(m.spinner(), m.width); trackerArea != "" {
		parts = append(parts, trackerArea)
	}
	if thinkingIndicator != "" {
		parts = append(parts, thinkingIndicator)
	}
	if m.modelSelectorActive {
		parts = append(parts, m.renderModelSelector())
	}
	if queueArea != "" {
		parts = append(parts, queueArea)
	}
	parts = append(parts, inputArea)
	parts = append(parts, statusLine)

	return strings.Join(parts, "\n")
}

// wrapText wraps text to fit within maxWidth, breaking on word boundaries when possible
func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{text}
	}

	// Handle empty or short text
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	remaining := text

	for len(remaining) > 0 {
		switch {
		case len(remaining) <= maxWidth:
			// Remaining text fits on one line
			lines = append(lines, remaining)
			remaining = ""

		default:
			// Need to break the line
			breakAt := maxWidth

			// Look for last space before maxWidth
			lastSpace := strings.LastIndex(remaining[:maxWidth], " ")
			if lastSpace > maxWidth/2 {
				// Found a reasonable break point
				breakAt = lastSpace
			}

			// Extract line and update remaining
			lines = append(lines, remaining[:breakAt])
			remaining = strings.TrimLeft(remaining[breakAt:], " ")
		}
	}

	return lines
}

func (m model) sendMessage(text string) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"text": text})

		var url string
		if m.interactiveMode {
			url = fmt.Sprintf("%s/messages", m.gateway)
		} else {
			url = fmt.Sprintf("%s/sessions/%s/messages", m.gateway, m.sessionID)
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

// generateTitle fires an async Haiku call to produce a session title from the
// user's first message. The result updates the CLI header display only — the
// branch and worktree names are not changed after creation.
func (m model) generateTitle(prompt string) tea.Cmd {
	return func() tea.Msg {
		title := generateSessionName(newLightweightProvider(), prompt)
		return sessionTitleMsg(title)
	}
}

func (m model) sendInterrupt() tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"interrupt": "true"})

		var url string
		if m.interactiveMode {
			url = fmt.Sprintf("%s/interrupt", m.gateway)
		} else {
			url = fmt.Sprintf("%s/sessions/%s/interrupt", m.gateway, m.sessionID)
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

func listenEvents(p *tea.Program, gatewayURL, sessionID string, interactiveMode bool) {
	var url string
	if interactiveMode {
		url = fmt.Sprintf("%s/events", gatewayURL)
	} else {
		url = fmt.Sprintf("%s/sessions/%s/events", gatewayURL, sessionID)
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

// isDefaultBranch returns true for branches that should trigger ephemeral
// worktree mode rather than branch-reuse mode (main, master, HEAD/detached).
func (m *model) trySlashComplete() bool {
	val := m.textArea.Value()
	if !strings.HasPrefix(val, "/") {
		return false
	}
	// Find matching commands
	var matches []string
	for _, name := range slashCommandNames() {
		if strings.HasPrefix(name, val) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		m.textArea.SetValue(matches[0])
		return true
	}
	return false
}

// resizeTextArea adjusts the textarea height to fit content, clamped to [1, 10].
func (m *model) resizeTextArea() {
	lines := m.textArea.LineCount()
	h := lines
	if h < 1 {
		h = 1
	}
	if h > 10 {
		h = 10
	}
	m.textArea.SetHeight(h)
}

// isReviewCommand checks if the input is a /review command.
func isReviewCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed == "/review" || strings.HasPrefix(trimmed, "/review ")
}

// parseReviewBase extracts the --base flag from a /review command.
// "/review --base main" → "main", "/review" → ""
func parseReviewBase(text string) string {
	parts := strings.Fields(text)
	for i, part := range parts {
		if part == "--base" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// isModelCommand checks if the input is a /model command.
func isModelCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed == "/model" || strings.HasPrefix(trimmed, "/model ")
}

// parseModelArg extracts the model name from a /model command, stripping any
// "--global" token.
// "/model sonnet" → "sonnet", "/model sonnet --global" → "sonnet", "/model" → ""
func parseModelArg(text string) string {
	trimmed := strings.TrimSpace(text)
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/model"))
	fields := strings.Fields(rest)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "--global" {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// parseModelGlobal reports whether the "--global" flag is present in a /model
// command. Position-agnostic: "/model opus --global" and "/model --global opus"
// both report true.
func parseModelGlobal(text string) bool {
	for _, f := range strings.Fields(text) {
		if f == "--global" {
			return true
		}
	}
	return false
}

// shortModelName strips the "claude-" prefix and date suffix for status bar display.
//
//	"claude-sonnet-4-20250514" → "sonnet-4"
//	"claude-opus-4-6"          → "opus-4"
//	"sonnet"                   → "sonnet"
//
// renderModelList formats provider model lists for TUI output.
func renderModelList(providers []types.ProviderModels) []string {
	if len(providers) == 0 {
		return []string{
			dimStyle.Render("No providers configured. Set ANTHROPIC_API_KEY, OPENAI_API_KEY, or install claude CLI."),
		}
	}

	var lines []string
	lines = append(lines, dimStyle.Render("Available models:"))
	lines = append(lines, "")

	for _, pm := range providers {
		if pm.Error != "" {
			lines = append(lines, headerStyle.Render(pm.Provider))
			lines = append(lines, dimStyle.Render("  Error: "+pm.Error))
			lines = append(lines, "")
			continue
		}

		// Claude CLI gets special alias display
		if pm.Provider == "Claude CLI" {
			lines = append(lines, headerStyle.Render("Claude CLI (aliases)"))
			for _, pair := range modelAliasesForDisplay() {
				lines = append(lines, dimStyle.Render(fmt.Sprintf("  %-10s → %s", pair[0], pair[1])))
			}
			lines = append(lines, "")
			continue
		}

		lines = append(lines, headerStyle.Render(pm.Provider))
		for _, m := range pm.Models {
			switch m.DisplayName {
			case "":
				lines = append(lines, dimStyle.Render("  "+m.ID))
			default:
				lines = append(lines, dimStyle.Render(fmt.Sprintf("  %-30s %s", m.ID, m.DisplayName)))
			}
		}
		lines = append(lines, "")
	}

	return lines
}

// modelAliasesForDisplay returns the ordered alias→full-ID mapping for CLI display.
func modelAliasesForDisplay() [][2]string {
	return [][2]string{
		{"opus", "claude-opus-4-6"},
		{"sonnet", "claude-sonnet-4-20250514"},
		{"haiku", "claude-haiku-4-20250506"},
	}
}

func shortModelName(name string) string {
	s := strings.TrimPrefix(name, "claude-")
	// Strip trailing date suffix: -YYYYMMDD or -N (short version numbers)
	// Match the last segment if it looks like a date (8 digits).
	if idx := strings.LastIndex(s, "-"); idx > 0 {
		suffix := s[idx+1:]
		if len(suffix) == 8 {
			allDigits := true
			for _, c := range suffix {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				s = s[:idx]
			}
		}
	}
	return s
}

type modelSwitchedMsg string
type modelsListMsg []types.ProviderModels

// applyModelSwitch performs a session model switch and, when global is true,
// persists model.default to ~/.forge/config.toml. A failed global write is
// surfaced but the in-session switch still proceeds.
func (m model) applyModelSwitch(arg string, global bool) (model, tea.Cmd) {
	if !m.interactiveMode {
		m.out.Append("")
		m.out.Append(errorStyle.Render("model switching is not supported in gateway mode"))
		return m, nil
	}
	if global {
		if err := config.SetValue("model.default", arg); err != nil {
			m.out.Append("")
			m.out.Append(errorStyle.Render("failed to save model.default: " + err.Error()))
		} else {
			m.out.Append("")
			m.out.Append(dimStyle.Render("Saved model.default = " + arg + " to ~/.forge/config.toml"))
		}
	}
	m.out.Append("")
	m.out.Append(dimStyle.Render("Switching model to " + arg + "..."))
	return m, m.sendSetModel(arg)
}

// buildModelChoices flattens provider model lists into a single ordered list of
// selectable entries for the /model selector overlay. Claude CLI aliases are
// listed first (most common), then each provider's concrete model IDs.
func buildModelChoices(providers []types.ProviderModels) []modelChoice {
	var choices []modelChoice
	for _, pm := range providers {
		if pm.Error != "" {
			continue
		}
		if pm.Provider == "Claude CLI" {
			for _, pair := range modelAliasesForDisplay() {
				choices = append(choices, modelChoice{
					id:    pair[0],
					label: fmt.Sprintf("%-10s → %s", pair[0], pair[1]),
				})
			}
			continue
		}
		for _, e := range pm.Models {
			label := e.ID
			if e.DisplayName != "" {
				label = fmt.Sprintf("%-30s %s", e.ID, e.DisplayName)
			}
			choices = append(choices, modelChoice{id: e.ID, label: label})
		}
	}
	return choices
}

// renderModelSelector renders the interactive /model picker overlay.
func (m model) renderModelSelector() string {
	var lines []string
	lines = append(lines, headerStyle.Render("Select a model")+dimStyle.Render("  (↑/↓ move · enter select · esc cancel)"))
	switch {
	case m.modelSelectorLoading:
		lines = append(lines, dimStyle.Render("  "+m.spinner()+" loading models..."))
	case len(m.modelSelectorItems) == 0:
		lines = append(lines, dimStyle.Render("  no models available"))
	default:
		for i, item := range m.modelSelectorItems {
			cursor := "  "
			style := dimStyle
			if i == m.modelSelectorCursor {
				cursor = "▸ "
				style = headerStyle
			}
			lines = append(lines, cursor+style.Render(item.label))
		}
	}
	return strings.Join(lines, "\n")
}

// sendSetModel sends a model change request to the agent.
func (m model) sendSetModel(modelName string) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"model": modelName})
		url := fmt.Sprintf("%s/model", m.gateway)

		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return errMsg(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return errMsg(fmt.Errorf("model switch failed: %d %s", resp.StatusCode, b))
		}

		var result struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return modelSwitchedMsg(result.Model)
	}
}

// fetchModelList queries the agent for available models from all providers.
func (m model) fetchModelList() tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/models", m.gateway)

		resp, err := http.Get(url)
		if err != nil {
			return errMsg(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return errMsg(fmt.Errorf("model list failed: %d %s", resp.StatusCode, b))
		}

		var result []types.ProviderModels
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return errMsg(fmt.Errorf("decode model list: %w", err))
		}
		return modelsListMsg(result)
	}
}

// reviewProviderSummary returns a human-readable summary of available review providers.
func reviewProviderSummary() string {
	var providers []string
	if _, ok := credentials.Default().Get(credentials.AnthropicAPIKey); ok {
		providers = append(providers, "Anthropic")
	}
	if _, ok := credentials.Default().Get(credentials.OpenAIAPIKey); ok {
		providers = append(providers, "OpenAI")
	}
	if len(providers) == 0 {
		return "no providers configured"
	}
	return strings.Join(providers, " + ")
}

// sendReview sends a review request to the agent/gateway.
func (m model) sendReview(baseBranch string) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"base": baseBranch})

		var url string
		switch m.interactiveMode {
		case true:
			url = fmt.Sprintf("%s/review", m.gateway)
		default:
			url = fmt.Sprintf("%s/sessions/%s/review", m.gateway, m.sessionID)
		}

		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return errMsg(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusAccepted {
			b, _ := io.ReadAll(resp.Body)
			return errMsg(fmt.Errorf("review request failed: %d %s", resp.StatusCode, b))
		}
		return nil
	}
}

// formatReviewFinding formats a review finding for display.
func formatReviewFinding(content string, width int) string {
	// Content is JSON of review.Finding — parse it for nice display.
	var finding struct {
		Reviewer    string `json:"reviewer"`
		Provider    string `json:"provider"`
		Severity    string `json:"severity"`
		File        string `json:"file"`
		StartLine   int    `json:"startLine"`
		EndLine     int    `json:"endLine"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(content), &finding); err != nil {
		return dimStyle.Render("  [finding] ") + content
	}

	severityStyle := dimStyle
	severityIcon := " "
	switch finding.Severity {
	case "critical":
		severityStyle = errorStyle
		severityIcon = "!!"
	case "warning":
		severityStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
		severityIcon = " !"
	case "suggestion":
		severityStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		severityIcon = " ~"
	}

	header := severityStyle.Render(severityIcon+" ["+finding.Severity+"]") + " " +
		dimStyle.Render(finding.Reviewer+" ("+finding.Provider+")")

	location := ""
	if finding.File != "" {
		location = dimStyle.Render(" " + finding.File)
		if finding.StartLine > 0 {
			location += dimStyle.Render(fmt.Sprintf(":%d", finding.StartLine))
			if finding.EndLine > 0 && finding.EndLine != finding.StartLine {
				location += dimStyle.Render(fmt.Sprintf("-%d", finding.EndLine))
			}
		}
	}

	maxDescWidth := width - 6
	if maxDescWidth < 40 {
		maxDescWidth = 40
	}
	descLines := wrapText(finding.Description, maxDescWidth)

	var result string
	result = header + location
	for _, line := range descLines {
		result += "\n    " + line
	}
	return result
}
