// Package session implements structured JSONL session persistence.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── Entry Types ──────────────────────────────────────────────

// EntryType identifies the type of session entry.
type EntryType string

const (
	EntryUser       EntryType = "user"
	EntryAssistant  EntryType = "assistant"
	EntrySystem     EntryType = "system"
	EntryAttachment EntryType = "attachment"
	EntryProgress   EntryType = "progress" // Ephemeral, not loaded on resume
)

// ── Base Entry ───────────────────────────────────────────────

// Entry is the base structure for all JSONL entries.
type Entry struct {
	UUID       string    `json:"uuid"`
	ParentUUID string    `json:"parentUuid,omitempty"`
	SessionID  string    `json:"sessionId"`
	Type       EntryType `json:"type"`
	Timestamp  int64     `json:"timestamp"`
	Data       any       `json:"data"`
}

// ── User Message ─────────────────────────────────────────────

// UserEntry represents a user message.
type UserEntry struct {
	Text string `json:"text"`
}

// ── Assistant Message ────────────────────────────────────────

// AssistantEntry represents an assistant response.
type AssistantEntry struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stopReason,omitempty"`
	Usage      *TokenUsage    `json:"usage,omitempty"`
}

// ContentBlock is a block within an assistant message.
type ContentBlock struct {
	Type  string         `json:"type"` // "text", "tool_use"
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// ── System Message ───────────────────────────────────────────

// SystemEntry represents a system notification or marker.
type SystemEntry struct {
	Message           string `json:"message"`
	IsCompactBoundary bool   `json:"isCompactBoundary,omitempty"`
}

// ── Attachment ───────────────────────────────────────────────

// AttachmentEntry represents context injected into the conversation.
type AttachmentEntry struct {
	Type    string `json:"type"` // "file", "memory", "skill", etc.
	Content string `json:"content"`
	Path    string `json:"path,omitempty"`
}

// ── Progress ─────────────────────────────────────────────────

// ProgressEntry tracks ephemeral tool progress (not loaded on resume).
type ProgressEntry struct {
	ToolUseID string `json:"toolUseId"`
	ToolName  string `json:"toolName"`
	Data      any    `json:"data"`
}

// ── Session Writer ───────────────────────────────────────────

// Writer writes session entries to a JSONL file.
type Writer struct {
	mu        sync.Mutex
	sessionID string
	path      string
	file      *os.File
	encoder   *json.Encoder
	lastUUID  string // UUID of last written entry (for parent chain)
}

// NewWriter creates a session writer. The file is created if it doesn't exist.
func NewWriter(sessionID, sessionsDir string) (*Writer, error) {
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	path := filepath.Join(sessionsDir, sessionID+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}

	return &Writer{
		sessionID: sessionID,
		path:      path,
		file:      file,
		encoder:   json.NewEncoder(file),
	}, nil
}

// WriteUser writes a user message entry.
func (w *Writer) WriteUser(text string) (string, error) {
	return w.write(EntryUser, UserEntry{Text: text})
}

// WriteAssistant writes an assistant response entry.
func (w *Writer) WriteAssistant(content []ContentBlock, stopReason string, usage *TokenUsage) (string, error) {
	return w.write(EntryAssistant, AssistantEntry{
		Content:    content,
		StopReason: stopReason,
		Usage:      usage,
	})
}

// WriteSystem writes a system message entry.
func (w *Writer) WriteSystem(message string, isCompactBoundary bool) (string, error) {
	return w.write(EntrySystem, SystemEntry{
		Message:           message,
		IsCompactBoundary: isCompactBoundary,
	})
}

// WriteAttachment writes an attachment entry.
func (w *Writer) WriteAttachment(attachType, content, path string) (string, error) {
	return w.write(EntryAttachment, AttachmentEntry{
		Type:    attachType,
		Content: content,
		Path:    path,
	})
}

// WriteProgress writes an ephemeral progress entry.
func (w *Writer) WriteProgress(toolUseID, toolName string, data any) (string, error) {
	return w.write(EntryProgress, ProgressEntry{
		ToolUseID: toolUseID,
		ToolName:  toolName,
		Data:      data,
	})
}

// write is the internal method that writes any entry type.
func (w *Writer) write(entryType EntryType, data any) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entryUUID := uuid.New().String()
	entry := Entry{
		UUID:       entryUUID,
		ParentUUID: w.lastUUID,
		SessionID:  w.sessionID,
		Type:       entryType,
		Timestamp:  time.Now().UnixMilli(),
		Data:       data,
	}

	if err := w.encoder.Encode(entry); err != nil {
		return "", fmt.Errorf("encode entry: %w", err)
	}

	// Update parent chain
	w.lastUUID = entryUUID
	return entryUUID, nil
}

// Close closes the session file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// ── Session Reader ───────────────────────────────────────────

// Reader reads session entries from a JSONL file.
type Reader struct {
	sessionID string
	path      string
}

// NewReader creates a session reader.
func NewReader(sessionID, sessionsDir string) *Reader {
	return &Reader{
		sessionID: sessionID,
		path:      filepath.Join(sessionsDir, sessionID+".jsonl"),
	}
}

// ReadAll reads all entries from the session file.
// Progress entries are skipped (ephemeral UI state).
func (r *Reader) ReadAll() ([]Entry, error) {
	file, err := os.Open(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No session file yet
		}
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var entries []Entry
	scanner := bufio.NewScanner(file)

	// Increase buffer size for large tool results
	buf := make([]byte, 0, 1024*1024) // 1MB buffer
	scanner.Buffer(buf, 10*1024*1024) // 10MB max line size

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("parse line %d: %w", lineNum, err)
		}

		// Skip ephemeral progress entries
		if entry.Type == EntryProgress {
			continue
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session file: %w", err)
	}

	return entries, nil
}

// ReadAfter reads entries after the given UUID (for resuming mid-conversation).
func (r *Reader) ReadAfter(afterUUID string) ([]Entry, error) {
	all, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	if afterUUID == "" {
		return all, nil
	}

	// Find the index of afterUUID
	startIdx := -1
	for i, entry := range all {
		if entry.UUID == afterUUID {
			startIdx = i + 1
			break
		}
	}

	if startIdx == -1 {
		return nil, fmt.Errorf("entry %s not found", afterUUID)
	}

	if startIdx >= len(all) {
		return nil, nil // No entries after this one
	}

	return all[startIdx:], nil
}

// Exists returns true if the session file exists.
func (r *Reader) Exists() bool {
	_, err := os.Stat(r.path)
	return err == nil
}

// ── Validation ───────────────────────────────────────────────

// ValidateChain checks that the parent UUID chain is intact.
// Returns the first broken link, or nil if the chain is valid.
func ValidateChain(entries []Entry) *Entry {
	uuidSet := make(map[string]bool)
	for _, entry := range entries {
		uuidSet[entry.UUID] = true
	}

	for i, entry := range entries {
		// First entry should have no parent
		if i == 0 {
			if entry.ParentUUID != "" {
				return &entry
			}
			continue
		}

		// All other entries must reference a previous UUID
		if entry.ParentUUID == "" || !uuidSet[entry.ParentUUID] {
			return &entry
		}
	}

	return nil
}
