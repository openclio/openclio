package storage

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/kg"
)

// Embedder generates vector embeddings for messages.
// It mirrors the context embedder interface to avoid package coupling.
type Embedder interface {
	Embed(text string) ([]float32, error)
	Dimensions() int
}

// Message represents a single message in a conversation.
type Message struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"` // user, assistant, tool_result, system
	Content   string    `json:"content"`
	Summary   *string   `json:"summary,omitempty"`
	Tokens    int       `json:"tokens"`
	CreatedAt time.Time `json:"created_at"`
}

// MessageStore provides operations for messages.
type MessageStore struct {
	db              *sql.DB
	embedder        Embedder
	embeddingErrors *EmbeddingErrorStore
	knowledgeGraph  *KnowledgeGraphStore
}

// NewMessageStore creates a new MessageStore.
func NewMessageStore(db *DB, embedders ...Embedder) *MessageStore {
	var embedder Embedder
	if len(embedders) > 0 {
		embedder = embedders[0]
	}
	return &MessageStore{db: db.Conn(), embedder: embedder}
}

// SetEmbedder sets or replaces the embedder used for background message indexing.
func (m *MessageStore) SetEmbedder(embedder Embedder) {
	m.embedder = embedder
}

// SetEmbeddingErrorStore attaches optional embedding error tracking.
func (m *MessageStore) SetEmbeddingErrorStore(store *EmbeddingErrorStore) {
	m.embeddingErrors = store
}

// SetKnowledgeGraphStore attaches optional knowledge graph persistence.
func (m *MessageStore) SetKnowledgeGraphStore(store *KnowledgeGraphStore) {
	m.knowledgeGraph = store
}

// SearchKnowledge proxies knowledge graph node lookup when attached.
func (m *MessageStore) SearchKnowledge(query, nodeType string, limit int) ([]KGNode, error) {
	if m.knowledgeGraph == nil {
		return nil, nil
	}
	return m.knowledgeGraph.SearchNodes(query, nodeType, limit)
}

// Insert adds a new message to a session.
// Write failures caused by lock contention are automatically retried (up to 3 attempts).
func (m *MessageStore) Insert(sessionID, role, content string, tokens int) (*Message, error) {
	now := time.Now().UTC()

	result, err := ExecWithRetry(m.db,
		"INSERT INTO messages (session_id, role, content, tokens, created_at) VALUES (?, ?, ?, ?, ?)",
		sessionID, role, content, tokens, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting message: %w", err)
	}

	id, _ := result.LastInsertId()
	m.embedMessageAsync(id, role, content)
	m.extractKnowledgeGraphAsync(id, role, content)
	return &Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Tokens:    tokens,
		CreatedAt: now,
	}, nil
}

// GetBySession returns all messages for a session, ordered by creation time.
func (m *MessageStore) GetBySession(sessionID string) ([]Message, error) {
	rows, err := m.db.Query(
		"SELECT id, session_id, role, content, summary, tokens, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC",
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting messages for session %s: %w", sessionID, err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.Summary, &msg.Tokens, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// GetRecent returns the last N messages for a session.
func (m *MessageStore) GetRecent(sessionID string, limit int) ([]Message, error) {
	rows, err := m.db.Query(
		`SELECT id, session_id, role, content, summary, tokens, created_at 
		 FROM messages WHERE session_id = ? 
		   AND compacted = 0
		 ORDER BY created_at DESC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting recent messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.Summary, &msg.Tokens, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		messages = append(messages, msg)
	}

	// Reverse to get chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, rows.Err()
}

// CountBySession returns the total number of messages in a session.
func (m *MessageStore) CountBySession(sessionID string) (int, error) {
	var count int
	err := m.db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = ? AND compacted = 0", sessionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting messages: %w", err)
	}
	return count, nil
}

// InsertWithEmbedding adds a message with its vector embedding.
func (m *MessageStore) InsertWithEmbedding(sessionID, role, content string, tokens int, embedding []float32) (*Message, error) {
	now := time.Now().UTC()

	var embBlob []byte
	if len(embedding) > 0 {
		embBlob = float32sToBytes(embedding)
	}

	result, err := m.db.Exec(
		"INSERT INTO messages (session_id, role, content, tokens, embedding, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		sessionID, role, content, tokens, embBlob, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting message with embedding: %w", err)
	}

	id, _ := result.LastInsertId()
	return &Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Tokens:    tokens,
		CreatedAt: now,
	}, nil
}

// GetOldMessages returns active messages older than the most recent keepRecentTurns.
// A "turn" is approximated as two messages (user + assistant).
func (m *MessageStore) GetOldMessages(sessionID string, keepRecentTurns int) ([]Message, error) {
	if keepRecentTurns <= 0 {
		keepRecentTurns = 5
	}
	keepRecentMessages := keepRecentTurns * 2

	rows, err := m.db.Query(
		`SELECT id, session_id, role, content, summary, tokens, created_at
		 FROM messages
		 WHERE session_id = ? AND compacted = 0
		 ORDER BY created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting old messages: %w", err)
	}
	defer rows.Close()

	var active []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.Summary, &msg.Tokens, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning old message: %w", err)
		}
		active = append(active, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating old messages: %w", err)
	}

	if len(active) <= keepRecentMessages {
		return nil, nil
	}
	return active[:len(active)-keepRecentMessages], nil
}

// ArchiveMessages marks active messages up to olderThanID as compacted.
// Returns the number of archived messages.
func (m *MessageStore) ArchiveMessages(sessionID string, olderThanID int64) (int64, error) {
	result, err := ExecWithRetry(
		m.db,
		`UPDATE messages
		 SET compacted = 1
		 WHERE session_id = ? AND compacted = 0 AND id <= ?`,
		sessionID, olderThanID,
	)
	if err != nil {
		return 0, fmt.Errorf("archiving messages: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

// EmbeddedMessage is a message with its vector embedding for search.
type EmbeddedMessage struct {
	ID        int64
	SessionID string
	Role      string
	Content   string
	Summary   string
	Tokens    int
	Embedding []float32
}

// GetEmbeddings returns all messages with embeddings for a session.
func (m *MessageStore) GetEmbeddings(sessionID string) ([]EmbeddedMessage, error) {
	return m.GetStoredEmbeddings(sessionID)
}

// GetStoredEmbeddings returns recent active messages with embeddings for semantic search.
func (m *MessageStore) GetStoredEmbeddings(sessionID string) ([]EmbeddedMessage, error) {
	rows, err := m.db.Query(
		`SELECT id, session_id, role, content, COALESCE(summary, ''), tokens, embedding 
		 FROM messages 
		 WHERE session_id = ? AND embedding IS NOT NULL AND compacted = 0
		 ORDER BY created_at DESC
		 LIMIT 200`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting embeddings: %w", err)
	}
	defer rows.Close()

	var results []EmbeddedMessage
	for rows.Next() {
		var msg EmbeddedMessage
		var embBlob []byte
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.Summary, &msg.Tokens, &embBlob); err != nil {
			return nil, fmt.Errorf("scanning embedded message: %w", err)
		}
		if len(embBlob) > 0 {
			msg.Embedding = bytesToFloat32s(embBlob)
		}
		results = append(results, msg)
	}
	return results, rows.Err()
}

func (m *MessageStore) embedMessageAsync(messageID int64, role, content string) {
	if m.embedder == nil || m.embedder.Dimensions() == 0 {
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	switch role {
	case "user", "assistant", "system":
	default:
		// Tool payloads are often noisy and not useful for semantic recall.
		return
	}

	go func() {
		vec, err := m.embedder.Embed(content)
		if err != nil {
			if m.embeddingErrors != nil {
				_ = m.embeddingErrors.RecordEmbeddingError("message_index", err.Error())
			}
			return
		}
		if len(vec) == 0 {
			return
		}
		if _, err := ExecWithRetry(
			m.db,
			"UPDATE messages SET embedding = ? WHERE id = ?",
			float32sToBytes(vec), messageID,
		); err != nil && m.embeddingErrors != nil {
			_ = m.embeddingErrors.RecordEmbeddingError("message_index_store", err.Error())
		}
	}()
}

func (m *MessageStore) extractKnowledgeGraphAsync(messageID int64, role, content string) {
	if m.knowledgeGraph == nil {
		return
	}
	switch role {
	case "user", "assistant":
	default:
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}

	go func() {
		entities, relations := kg.Extract(content)
		if len(entities) == 0 && len(relations) == 0 {
			return
		}
		_ = m.knowledgeGraph.IngestExtracted(messageID, entities, relations)
	}()
}

// float32sToBytes serializes a float32 slice to little-endian raw bytes (4 bytes per float).
// Uses math.Float32bits — portable, no unsafe package required.
func float32sToBytes(fs []float32) []byte {
	buf := make([]byte, len(fs)*4)
	for i, f := range fs {
		bits := math.Float32bits(f)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// bytesToFloat32s deserializes little-endian raw bytes to a float32 slice.
func bytesToFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	fs := make([]float32, len(b)/4)
	for i := range fs {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		fs[i] = math.Float32frombits(bits)
	}
	return fs
}
