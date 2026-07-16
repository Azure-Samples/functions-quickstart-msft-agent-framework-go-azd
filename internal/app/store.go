package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/microsoft/agent-framework-go/agent"
)

// SessionStore persists MAF sessions in Cosmos DB, keyed by
// conversationId. Each Function invocation reads the document, hands
// the session to the agent, then writes the mutated session back —
// the classic serverless "load / mutate / save" cycle around an
// otherwise stateless worker.
type SessionStore struct {
	container *azcosmos.ContainerClient
}

// sessionDoc is the wire format actually stored in Cosmos. We keep
// MAF's session bytes as a RawMessage so this storage layer doesn't
// couple to MAF's internal session schema — if MAF changes the shape
// of a Session between versions, this file needs no changes.
//
// The container is partitioned by /conversationId (see cosmos.bicep),
// so the doc MUST carry that field for Cosmos to extract the partition
// key from the body during upsert.
type sessionDoc struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversationId"`
	Session        json.RawMessage `json:"session"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

func NewSessionStore(ctx context.Context, cred azcore.TokenCredential) (*SessionStore, error) {
	endpoint := requireEnv("COSMOS_ENDPOINT")
	database := requireEnv("COSMOS_DATABASE")
	container := envOr("COSMOS_CONTAINER", "conversations")

	client, err := azcosmos.NewClient(endpoint, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("cosmos client: %w", err)
	}
	cc, err := client.NewContainer(database, container)
	if err != nil {
		return nil, fmt.Errorf("cosmos container: %w", err)
	}
	return &SessionStore{container: cc}, nil
}

// Load returns the raw session JSON for a conversation, or nil bytes
// if the conversation doesn't exist yet. 404s are NOT treated as an
// error: the first message in a new conversation is a normal case,
// not a failure.
func (s *SessionStore) Load(ctx context.Context, conversationID string) ([]byte, error) {
	pk := azcosmos.NewPartitionKeyString(conversationID)
	resp, err := s.container.ReadItem(ctx, pk, conversationID, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cosmos read: %w", err)
	}
	var doc sessionDoc
	if err := json.Unmarshal(resp.Value, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal doc: %w", err)
	}
	return doc.Session, nil
}

// Save upserts (rather than creates) so the endpoint is idempotent
// across retries — a repeated POST with the same conversationId on
// a transient error won't fail with "already exists".
func (s *SessionStore) Save(ctx context.Context, conversationID string, sess *agent.Session) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	doc := sessionDoc{
		ID:             conversationID,
		ConversationID: conversationID,
		Session:        raw,
		UpdatedAt:      time.Now().UTC(),
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal doc: %w", err)
	}
	pk := azcosmos.NewPartitionKeyString(conversationID)
	if _, err := s.container.UpsertItem(ctx, pk, body, nil); err != nil {
		return fmt.Errorf("cosmos upsert: %w", err)
	}
	return nil
}

func (s *SessionStore) Delete(ctx context.Context, conversationID string) error {
	pk := azcosmos.NewPartitionKeyString(conversationID)
	if _, err := s.container.DeleteItem(ctx, pk, conversationID, nil); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("cosmos delete: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}
