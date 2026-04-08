package whatsapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/halfmoon-labs/halfmoon/pkg/bus"
	"github.com/halfmoon-labs/halfmoon/pkg/channels"
	"github.com/halfmoon-labs/halfmoon/pkg/config"
)

func TestHandleIncomingMessage_DoesNotConsumeGenericCommandsLocally(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &WhatsAppChannel{
		BaseChannel: channels.NewBaseChannel("whatsapp", config.WhatsAppConfig{}, messageBus, nil),
		ctx:         context.Background(),
	}

	ch.handleIncomingMessage(map[string]any{
		"type":    "message",
		"id":      "mid1",
		"from":    "user1",
		"chat":    "chat1",
		"content": "/help",
	})

	inbound, ok := <-messageBus.InboundChan()
	if !ok {
		t.Fatal("expected inbound message to be forwarded")
	}
	if inbound.Channel != "whatsapp" {
		t.Fatalf("channel=%q", inbound.Channel)
	}
	if inbound.Content != "/help" {
		t.Fatalf("content=%q", inbound.Content)
	}
}

// newTestWSServer creates a WebSocket server that collects received frames.
// Returns the server and a channel that receives each raw JSON frame.
func newTestWSServer(t *testing.T) (*httptest.Server, <-chan map[string]string) {
	t.Helper()
	frames := make(chan map[string]string, 10)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame map[string]string
			if json.Unmarshal(msg, &frame) == nil {
				frames <- frame
			}
		}
	}))
	return srv, frames
}

func TestStartTyping_SendsComposingFrame(t *testing.T) {
	srv, frames := newTestWSServer(t)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	messageBus := bus.NewMessageBus()
	ch := &WhatsAppChannel{
		BaseChannel: channels.NewBaseChannel("whatsapp", config.WhatsAppConfig{}, messageBus, nil),
		url:         wsURL,
	}

	require.NoError(t, ch.Start(context.Background()))
	defer ch.Stop(context.Background())

	stop, err := ch.StartTyping(context.Background(), "123@s.whatsapp.net")
	require.NoError(t, err)

	select {
	case frame := <-frames:
		assert.Equal(t, "typing", frame["type"])
		assert.Equal(t, "123@s.whatsapp.net", frame["to"])
		assert.Equal(t, "composing", frame["action"])
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for composing frame")
	}

	stop()

	select {
	case frame := <-frames:
		assert.Equal(t, "typing", frame["type"])
		assert.Equal(t, "123@s.whatsapp.net", frame["to"])
		assert.Equal(t, "paused", frame["action"])
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for paused frame")
	}
}

func TestStartTyping_NoopWhenDisconnected(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &WhatsAppChannel{
		BaseChannel: channels.NewBaseChannel("whatsapp", config.WhatsAppConfig{}, messageBus, nil),
	}

	// Should not panic or error even with no connection
	stop, err := ch.StartTyping(context.Background(), "123@s.whatsapp.net")
	require.NoError(t, err)
	stop() // should not panic
}

func TestWhatsAppChannel_ImplementsTypingCapable(t *testing.T) {
	var _ channels.TypingCapable = (*WhatsAppChannel)(nil)
}

func TestStart_ReconnectsWhenBridgeUnavailable(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch, err := NewWhatsAppChannel(config.WhatsAppConfig{
		BridgeURL: "ws://127.0.0.1:1", // nothing listening
	}, messageBus)
	require.NoError(t, err)

	// Start should succeed even though bridge is down — reconnect runs in background
	require.NoError(t, ch.Start(context.Background()))
	assert.True(t, ch.IsRunning())

	// Channel should not be connected
	ch.mu.Lock()
	assert.False(t, ch.connected)
	ch.mu.Unlock()

	// Reconnect should be active
	ch.reconnectMu.Lock()
	assert.True(t, ch.reconnecting)
	ch.reconnectMu.Unlock()

	// Stop should clean up without hanging
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, ch.Stop(ctx))
	assert.False(t, ch.IsRunning())
}

func TestListen_ReconnectsOnConnectionDrop(t *testing.T) {
	// Use a server that accepts and then immediately closes the connection
	// after a short delay, ensuring the client gets a clean read error.
	var serverConn *websocket.Conn
	var serverMu sync.Mutex
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverMu.Lock()
		serverConn = conn
		serverMu.Unlock()
		// Keep connection alive until test closes it
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	messageBus := bus.NewMessageBus()
	ch, err := NewWhatsAppChannel(config.WhatsAppConfig{
		BridgeURL: wsURL,
	}, messageBus)
	require.NoError(t, err)

	require.NoError(t, ch.Start(context.Background()))

	// Verify connected
	ch.mu.Lock()
	assert.True(t, ch.connected)
	ch.mu.Unlock()

	// Wait for server connection to be established
	require.Eventually(t, func() bool {
		serverMu.Lock()
		defer serverMu.Unlock()
		return serverConn != nil
	}, 2*time.Second, 50*time.Millisecond)

	// Mark connection as lost
	ch.mu.Lock()
	origConn := ch.conn
	ch.mu.Unlock()
	require.NotNil(t, origConn)

	// Force close the server-side connection — client will get a read error
	serverMu.Lock()
	serverConn.Close()
	serverMu.Unlock()

	// Wait for reconnect to complete — the channel should get a new connection
	// (the server is still up, so reconnect succeeds immediately)
	require.Eventually(t, func() bool {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		return ch.conn != nil && ch.conn != origConn
	}, 5*time.Second, 50*time.Millisecond, "expected new connection after reconnect")

	// Clean up
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, ch.Stop(ctx))
}

func TestStart_ConnectsWhenBridgeAvailable(t *testing.T) {
	srv, _ := newTestWSServer(t)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	messageBus := bus.NewMessageBus()
	ch, err := NewWhatsAppChannel(config.WhatsAppConfig{
		BridgeURL: wsURL,
	}, messageBus)
	require.NoError(t, err)

	require.NoError(t, ch.Start(context.Background()))
	defer ch.Stop(context.Background())

	ch.mu.Lock()
	assert.True(t, ch.connected)
	ch.mu.Unlock()

	ch.reconnectMu.Lock()
	assert.False(t, ch.reconnecting, "should not be reconnecting when bridge is available")
	ch.reconnectMu.Unlock()
}
