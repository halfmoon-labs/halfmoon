package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/halfmoon-labs/halfmoon/pkg/bus"
	"github.com/halfmoon-labs/halfmoon/pkg/channels"
	"github.com/halfmoon-labs/halfmoon/pkg/config"
	"github.com/halfmoon-labs/halfmoon/pkg/identity"
	"github.com/halfmoon-labs/halfmoon/pkg/logger"
	"github.com/halfmoon-labs/halfmoon/pkg/utils"
)

const (
	reconnectInitial    = 5 * time.Second
	reconnectMax        = 5 * time.Minute
	reconnectMultiplier = 2.0

	writeTimeout     = 10 * time.Second
	handshakeTimeout = 10 * time.Second
)

type WhatsAppChannel struct {
	*channels.BaseChannel
	conn         *websocket.Conn
	config       config.WhatsAppConfig
	url          string
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	connected    bool
	reconnectMu  sync.Mutex
	reconnecting bool
	stopping     atomic.Bool
	wg           sync.WaitGroup
}

func NewWhatsAppChannel(cfg config.WhatsAppConfig, bus *bus.MessageBus) (*WhatsAppChannel, error) {
	base := channels.NewBaseChannel(
		"whatsapp",
		cfg,
		bus,
		cfg.AllowFrom,
		channels.WithMaxMessageLength(65536),
		channels.WithReasoningChannelID(cfg.ReasoningChannelID),
	)

	return &WhatsAppChannel{
		BaseChannel: base,
		config:      cfg,
		url:         cfg.BridgeURL,
	}, nil
}

func (c *WhatsAppChannel) Start(ctx context.Context) error {
	logger.InfoCF("whatsapp", "Starting WhatsApp channel", map[string]any{
		"bridge_url": c.url,
	})

	c.ctx, c.cancel = context.WithCancel(ctx)
	c.stopping.Store(false)

	c.SetRunning(true)

	if err := c.dial(); err != nil {
		// Bridge not available yet — start reconnect in background.
		// The channel is "running" so incoming messages will be processed
		// once the connection is established.
		logger.WarnCF("whatsapp", "Initial connection failed, will retry", map[string]any{
			"error": err.Error(),
		})
		c.startReconnect()
	} else {
		logger.InfoC("whatsapp", "WhatsApp channel connected")
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.listen()
		}()
	}

	return nil
}

func (c *WhatsAppChannel) Stop(ctx context.Context) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp channel...")

	c.reconnectMu.Lock()
	c.stopping.Store(true)
	c.reconnectMu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	c.closeConn()

	// Wait for background goroutines (listen, reconnect) with context timeout.
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		logger.WarnCF("whatsapp", "Stop context canceled before goroutines finished", map[string]any{
			"error": ctx.Err().Error(),
		})
	}

	c.SetRunning(false)
	return nil
}

func (c *WhatsAppChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("whatsapp connection not established: %w", channels.ErrTemporary)
	}

	payload := map[string]any{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		_ = c.conn.SetWriteDeadline(time.Time{})
		return fmt.Errorf("whatsapp send: %w", channels.ErrTemporary)
	}
	_ = c.conn.SetWriteDeadline(time.Time{})

	return nil
}

// StartTyping implements channels.TypingCapable.
// It sends a composing indicator to the bridge and returns a stop function
// that sends a paused indicator. The bridge is responsible for translating
// these into the appropriate WhatsApp presence updates.
func (c *WhatsAppChannel) StartTyping(_ context.Context, chatID string) (func(), error) {
	c.sendTypingAction(chatID, "composing")

	return func() {
		c.sendTypingAction(chatID, "paused")
	}, nil
}

func (c *WhatsAppChannel) sendTypingAction(chatID, action string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return
	}

	payload, err := json.Marshal(map[string]string{
		"type":   "typing",
		"to":     chatID,
		"action": action,
	})
	if err != nil {
		return
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		logger.DebugCF("whatsapp", "typing indicator send failed", map[string]any{
			"action": action,
			"error":  err.Error(),
		})
	}
	_ = c.conn.SetWriteDeadline(time.Time{})
}

// dial attempts a single WebSocket connection to the bridge.
func (c *WhatsAppChannel) dial() error {
	dialer := &websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
	}

	conn, resp, err := dialer.Dial(c.url, nil)
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to connect to WhatsApp bridge: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	return nil
}

// closeConn closes the WebSocket connection if open.
func (c *WhatsAppChannel) closeConn() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			logger.DebugCF("whatsapp", "Error closing connection", map[string]any{
				"error": err.Error(),
			})
		}
		c.conn = nil
	}
	c.connected = false
}

// listen reads messages from the WebSocket connection. When the connection
// drops, it triggers a reconnect and returns.
func (c *WhatsAppChannel) listen() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()

			if conn == nil {
				return
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				select {
				case <-c.ctx.Done():
					return
				default:
				}

				logger.WarnCF("whatsapp", "WhatsApp bridge connection lost", map[string]any{
					"error": err.Error(),
				})
				c.closeConn()
				c.startReconnect()
				return
			}

			var msg map[string]any
			if err := json.Unmarshal(message, &msg); err != nil {
				logger.ErrorCF("whatsapp", "Failed to unmarshal WhatsApp message", map[string]any{
					"error": err.Error(),
				})
				continue
			}

			msgType, ok := msg["type"].(string)
			if !ok {
				continue
			}

			if msgType == "message" {
				c.handleIncomingMessage(msg)
			}
		}
	}
}

// startReconnect spawns a reconnect goroutine if one isn't already running.
func (c *WhatsAppChannel) startReconnect() {
	c.reconnectMu.Lock()
	if c.reconnecting || c.stopping.Load() {
		c.reconnectMu.Unlock()
		return
	}
	c.reconnecting = true
	c.wg.Add(1)
	c.reconnectMu.Unlock()

	go func() {
		defer c.wg.Done()
		c.reconnectWithBackoff()
	}()
}

// reconnectWithBackoff retries connecting to the bridge with exponential
// backoff (5s initial, 5m cap, 2x multiplier). Runs until connected or
// the channel is stopped.
func (c *WhatsAppChannel) reconnectWithBackoff() {
	defer func() {
		c.reconnectMu.Lock()
		c.reconnecting = false
		c.reconnectMu.Unlock()
	}()

	backoff := reconnectInitial
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		logger.InfoCF("whatsapp", "Reconnecting to WhatsApp bridge", map[string]any{
			"backoff": backoff.String(),
		})

		if err := c.dial(); err != nil {
			logger.WarnCF("whatsapp", "WhatsApp bridge reconnect failed", map[string]any{
				"error": err.Error(),
			})

			select {
			case <-c.ctx.Done():
				return
			case <-time.After(backoff):
				next := time.Duration(float64(backoff) * reconnectMultiplier)
				if next > reconnectMax {
					next = reconnectMax
				}
				backoff = next
			}
			continue
		}

		logger.InfoC("whatsapp", "WhatsApp bridge reconnected")
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.listen()
		}()
		return
	}
}

func (c *WhatsAppChannel) handleIncomingMessage(msg map[string]any) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	content, ok := msg["content"].(string)
	if !ok {
		content = ""
	}

	var mediaPaths []string
	if mediaData, ok := msg["media"].([]any); ok {
		mediaPaths = make([]string, 0, len(mediaData))
		for _, m := range mediaData {
			if path, ok := m.(string); ok {
				mediaPaths = append(mediaPaths, path)
			}
		}
	}

	metadata := make(map[string]string)
	var messageID string
	if mid, ok := msg["id"].(string); ok {
		messageID = mid
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	var peer bus.Peer
	if chatID == senderID {
		peer = bus.Peer{Kind: "direct", ID: senderID}
	} else {
		peer = bus.Peer{Kind: "group", ID: chatID}
	}

	logger.InfoCF("whatsapp", "WhatsApp message received", map[string]any{
		"sender":  senderID,
		"preview": utils.Truncate(content, 50),
	})

	sender := bus.SenderInfo{
		Platform:    "whatsapp",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("whatsapp", senderID),
	}
	if display, ok := metadata["user_name"]; ok {
		sender.DisplayName = display
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	c.HandleMessage(c.ctx, peer, messageID, senderID, chatID, content, mediaPaths, metadata, sender)
}
