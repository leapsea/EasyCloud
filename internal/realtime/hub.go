// Package realtime pushes server events over one WebSocket per tab with topic subscriptions.
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/httpx"
)

// Message is sent to clients.
type Message struct {
	Topic string `json:"topic"`
	Data  any    `json:"data"`
}

type clientMsg struct {
	Op    string `json:"op"` // sub | unsub
	Topic string `json:"topic"`
}

type client struct {
	sub    pathguard.Subject
	send   chan Message
	mu     sync.Mutex
	topics map[string]bool
}

type Hub struct {
	guard pathguard.Authorizer
	log   *slog.Logger

	mu      sync.RWMutex
	clients map[*client]struct{}
}

func NewHub(guard pathguard.Authorizer, log *slog.Logger) *Hub {
	return &Hub{guard: guard, log: log, clients: map[*client]struct{}{}}
}

// Broadcast delivers data to every client subscribed to topic.
func (h *Hub) Broadcast(topic string, data any) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.mu.Lock()
		ok := c.topics[topic]
		c.mu.Unlock()
		if ok {
			select {
			case c.send <- Message{Topic: topic, Data: data}:
			default: // slow client; it will resync after reconnect
			}
		}
	}
}

// authorize checks a topic subscription. Directory topics go through PathGuard.
func (h *Hub) authorize(ctx context.Context, sub pathguard.Subject, topic string) bool {
	switch {
	case strings.HasPrefix(topic, "dir:"):
		p, err := pathguard.Parse(strings.TrimPrefix(topic, "dir:"))
		if err != nil {
			return false
		}
		_, err = h.guard.Visible(ctx, sub, p)
		return err == nil
	case topic == "metrics":
		return sub.IsAdmin
	case topic == "notify":
		return true
	case strings.HasPrefix(topic, "tasks:"):
		return topic == "tasks:"+sub.UserID // only your own task progress
	}
	return false
}

// ServeHTTP upgrades an authenticated request. The auth middleware has already run;
// Origin is verified again here because browsers do not apply CORS to WebSockets.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !httpx.OriginAllowed(r) {
		http.Error(w, "cross-origin websocket rejected", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // origin checked above
	if err != nil {
		return
	}
	c := &client{sub: httpx.Subject(r), send: make(chan Message, 64), topics: map[string]bool{}}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go h.writeLoop(ctx, conn, c, cancel)

	conn.SetReadLimit(4096)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			conn.CloseNow()
			return
		}
		var m clientMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Op {
		case "sub":
			if !h.authorize(ctx, c.sub, m.Topic) {
				c.send <- Message{Topic: m.Topic, Data: map[string]string{"error": "NOT_FOUND"}}
				continue
			}
			c.mu.Lock()
			if len(c.topics) < 100 {
				c.topics[m.Topic] = true
			}
			c.mu.Unlock()
		case "unsub":
			c.mu.Lock()
			delete(c.topics, m.Topic)
			c.mu.Unlock()
		}
	}
}

func (h *Hub) writeLoop(ctx context.Context, conn *websocket.Conn, c *client, cancel func()) {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.send:
			b, _ := json.Marshal(m)
			wctx, done := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, b)
			done()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, done := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			done()
			if err != nil {
				return
			}
		}
	}
}
