package whatsapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
