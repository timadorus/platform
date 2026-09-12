package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/auth"
	"github.com/timadorus/platform/internal/bus"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	"github.com/timadorus/platform/internal/realtimehub"
)

// This is the first *_test.go file for any cmd/* binary in this codebase — every other binary's
// main.go is pure wiring with nothing worth testing directly. cmd/realtime's main.go has real
// logic (resolving events, matching filters, streaming) worth an end-to-end proof, so this lives
// in `package main` (an internal test) so it can call subscribeAll/streamHandler directly.

func newTestNATSURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcnats.Run(ctx, "nats:2.10-alpine")
	if err != nil {
		t.Fatalf("start nats container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return connStr
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			// Mirrors the exact list internal/aggregateresolve/testutil_test.go's own newTestPool
			// uses — campaign and character read-model migrations are needed for
			// TestStreamHandler_CharacterEventFiltersByCampaignID's seeded rows and query-based
			// resolve path below; object is included too, for parity with that list, even though
			// no current test here needs it.
			"../../internal/projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../internal/projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../../internal/projection/campaign/migrations/0003_campaign_configuration.up.sql",
			"../../internal/projection/entity/migrations/0001_entity_read_model.up.sql",
			"../../internal/projection/object/migrations/0001_object_read_model.up.sql",
			"../../internal/projection/character/migrations/0001_character_read_model.up.sql",
			"../../internal/projection/character/migrations/0002_character_info.up.sql",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

const testHMACSecret = "test-secret-at-least-32-bytes-long!"
const testKeyID = "test"

// newTestVerifierAndToken mirrors test/e2e/internal/jwtsecret.go's own MintToken/NewStaticSecretKeySet
// pairing, self-contained here rather than imported (that package is for the real-cluster e2e
// suite specifically, not a shared test dependency for individual packages).
func newTestVerifierAndToken(t *testing.T) (*auth.Verifier, string) {
	t.Helper()
	keySet, err := auth.NewStaticSecretKeySet(testKeyID, []byte(testHMACSecret))
	if err != nil {
		t.Fatalf("new static secret key set: %v", err)
	}
	verifier := auth.NewVerifier(keySet, "", "")

	key, err := jwk.Import([]byte(testHMACSecret))
	if err != nil {
		t.Fatalf("import key: %v", err)
	}
	if err := key.Set(jwk.KeyIDKey, testKeyID); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	now := time.Now()
	token, err := jwt.NewBuilder().Subject(uuid.NewString()).IssuedAt(now).Expiration(now.Add(time.Hour)).Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.HS256(), key))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return verifier, string(signed)
}

// readSSEData reads a single "data: <json>\n\n" frame and returns its data payload (without the
// "data: " prefix). Returns "" on any read error (including EOF/context-cancelled) — callers pass
// the result through a channel and check it in the test's own goroutine (never call t.Fatal from
// inside a spawned goroutine — the testing package requires Fatal/FailNow to run on the goroutine
// executing the test itself).
func readSSEData(r *bufio.Reader) string {
	var data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return ""
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return data
		}
		data = strings.TrimPrefix(line, "data: ")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return url.QueryEscape(string(b))
}

// TestStreamHandler_DeliversOnlyMatchingEvent is cmd/realtime's one end-to-end proof: a real NATS
// event, resolved through the real subscribeAll wiring, reaches a connection whose filter matches
// and does NOT reach one whose filter doesn't — proving the whole pipeline (NATS -> resolve ->
// hub -> SSE wire format) does what the design spec says, not just each piece in isolation.
func TestStreamHandler_DeliversOnlyMatchingEvent(t *testing.T) {
	natsURL := newTestNATSURL(t)
	pool := newTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	verifier, token := newTestVerifierAndToken(t)

	hub := realtimehub.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := subscribeAll(ctx, natsURL, pool, hub, logger); err != nil {
		t.Fatalf("subscribeAll: %v", err)
	}
	// subscribeAll's goroutines subscribe asynchronously — give NATS a moment to establish the
	// consumers before publishing, or the first event could be missed the same way any live-only
	// (non-durable) subscriber can miss anything published before it's ready.
	time.Sleep(200 * time.Millisecond)

	srv := httptest.NewServer(streamHandler(hub, verifier, logger))
	defer srv.Close()

	universeID, otherUniverseID, entityID := uuid.New(), uuid.New(), uuid.New()

	matchingReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/changes/stream?access_token=%s&watch=%s",
		srv.URL, token, mustJSON(t, []map[string]string{{"type": "entity", "universeId": universeID.String()}})), nil)
	matchingResp, err := http.DefaultClient.Do(matchingReq)
	if err != nil {
		t.Fatalf("connect matching client: %v", err)
	}
	defer matchingResp.Body.Close()
	if matchingResp.StatusCode != http.StatusOK {
		t.Fatalf("matching client got status %d, want 200", matchingResp.StatusCode)
	}

	nonMatchingReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/changes/stream?access_token=%s&watch=%s",
		srv.URL, token, mustJSON(t, []map[string]string{{"type": "entity", "universeId": otherUniverseID.String()}})), nil)
	nonMatchingResp, err := http.DefaultClient.Do(nonMatchingReq)
	if err != nil {
		t.Fatalf("connect non-matching client: %v", err)
	}
	defer nonMatchingResp.Body.Close()

	pub, err := bus.NewPublisher(natsURL, watermill.NewSlogLogger(logger))
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	env := bus.Envelope{
		GlobalSeq: 1, AggregateID: entityID, AggregateType: entityevents.AggregateType, Version: 1,
		EventType: entityevents.TypeEntityCreated,
		Payload: mustJSONRaw(t, entityevents.EntityCreated{
			ID: entityID, Name: "Test Entity", UniverseID: universeID, OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := pub.Publish(bus.Subject(entityevents.AggregateType), message.NewMessage(watermill.NewUUID(), body)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	matchingCh := make(chan string, 1)
	go func() { matchingCh <- readSSEData(bufio.NewReader(matchingResp.Body)) }()
	select {
	case data := <-matchingCh:
		if data == "" {
			t.Fatal("matching client: read failed or connection closed unexpectedly")
		}
		var got realtimehub.Change
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", data, err)
		}
		if got.AggregateID != entityID.String() {
			t.Fatalf("got AggregateID %s, want %s", got.AggregateID, entityID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("matching client received nothing within 5s")
	}

	nonMatchingCh := make(chan string, 1)
	go func() { nonMatchingCh <- readSSEData(bufio.NewReader(nonMatchingResp.Body)) }()
	select {
	case data := <-nonMatchingCh:
		if data != "" {
			t.Fatalf("non-matching client received an event it should have been filtered out of: %s", data)
		}
	case <-time.After(time.Second):
		// expected: nothing arrived within the window
	}
}

func mustJSONRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// seedCampaign inserts a minimal campaigns_read_model row — same shape as
// internal/aggregateresolve/testutil_test.go's own seedCampaign, reused here so
// resolveCharacter's query-based resolve path has a real campaign row to join against.
func seedCampaign(t *testing.T, pool *pgxpool.Pool, campaignID, universeID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
}

// seedCharacter inserts a minimal characters_read_model row — same column shape as
// internal/aggregateresolve/resolve_test.go's TestCharacter_OtherEvent_ResolvesBothViaJoin uses.
func seedCharacter(t *testing.T, pool *pgxpool.Pool, characterID, campaignID, entityID, playerID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, 'Aragorn', $2, $3, $4, '', false, now())`,
		characterID, campaignID, entityID, playerID,
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}
}

// TestStreamHandler_CharacterEventFiltersByCampaignID is the end-to-end proof for the one
// genuinely new piece of data this whole plan adds: resolveCharacter's CampaignID hand-off.
// aggregateresolve's own tests prove the resolver returns the right CampaignID in isolation, and
// realtimehub's own tests prove a campaignId clause matches in isolation — this proves the two are
// actually wired together correctly through cmd/realtime itself. It publishes
// character.renamed.v1 (not CharacterCreated), which forces aggregateresolve.Character down its
// query/join resolve path (the payload alone carries no CampaignID for this event), against a
// Postgres testcontainer actually seeded and queried — unlike
// TestStreamHandler_DeliversOnlyMatchingEvent above, whose EntityCreated event resolves straight
// from the envelope payload and never touches Postgres at all.
func TestStreamHandler_CharacterEventFiltersByCampaignID(t *testing.T) {
	natsURL := newTestNATSURL(t)
	pool := newTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	verifier, token := newTestVerifierAndToken(t)

	campaignID, universeID := uuid.New(), uuid.New()
	otherCampaignID := uuid.New()
	seedCampaign(t, pool, campaignID, universeID)

	characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New()
	seedCharacter(t, pool, characterID, campaignID, entityID, playerID)

	hub := realtimehub.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := subscribeAll(ctx, natsURL, pool, hub, logger); err != nil {
		t.Fatalf("subscribeAll: %v", err)
	}
	// See TestStreamHandler_DeliversOnlyMatchingEvent's identical comment: subscribeAll's
	// goroutines subscribe asynchronously.
	time.Sleep(200 * time.Millisecond)

	srv := httptest.NewServer(streamHandler(hub, verifier, logger))
	defer srv.Close()

	matchingReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/changes/stream?access_token=%s&watch=%s",
		srv.URL, token, mustJSON(t, []map[string]string{{"type": "character", "campaignId": campaignID.String()}})), nil)
	matchingResp, err := http.DefaultClient.Do(matchingReq)
	if err != nil {
		t.Fatalf("connect matching client: %v", err)
	}
	defer matchingResp.Body.Close()
	if matchingResp.StatusCode != http.StatusOK {
		t.Fatalf("matching client got status %d, want 200", matchingResp.StatusCode)
	}

	// Watches a DIFFERENT campaignId than the seeded Character actually belongs to — proving the
	// CampaignID-based filter genuinely discriminates by campaign, not just "any character event
	// matches any character watch".
	nonMatchingReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/changes/stream?access_token=%s&watch=%s",
		srv.URL, token, mustJSON(t, []map[string]string{{"type": "character", "campaignId": otherCampaignID.String()}})), nil)
	nonMatchingResp, err := http.DefaultClient.Do(nonMatchingReq)
	if err != nil {
		t.Fatalf("connect non-matching client: %v", err)
	}
	defer nonMatchingResp.Body.Close()

	pub, err := bus.NewPublisher(natsURL, watermill.NewSlogLogger(logger))
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	env := bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeCharacterRenamed,
		Payload: mustJSONRaw(t, characterevents.CharacterRenamed{
			Name: "Strider", OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := pub.Publish(bus.Subject(characterevents.AggregateType), message.NewMessage(watermill.NewUUID(), body)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	matchingCh := make(chan string, 1)
	go func() { matchingCh <- readSSEData(bufio.NewReader(matchingResp.Body)) }()
	select {
	case data := <-matchingCh:
		if data == "" {
			t.Fatal("matching client: read failed or connection closed unexpectedly")
		}
		var got realtimehub.Change
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", data, err)
		}
		if got.AggregateID != characterID.String() {
			t.Fatalf("got AggregateID %s, want %s", got.AggregateID, characterID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("matching client received nothing within 5s")
	}

	nonMatchingCh := make(chan string, 1)
	go func() { nonMatchingCh <- readSSEData(bufio.NewReader(nonMatchingResp.Body)) }()
	select {
	case data := <-nonMatchingCh:
		if data != "" {
			t.Fatalf("non-matching client (different campaignId) received an event it should have been filtered out of: %s", data)
		}
	case <-time.After(time.Second):
		// expected: nothing arrived within the window
	}
}
