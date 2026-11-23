package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const eventHeartbeat = 25 * time.Second

type itemEvent struct {
	Type string `json:"type"` // "item.created" or "item.deleted"
	ID   string `json:"id"`
}

type subscriber struct {
	user authUser
	ch   chan itemEvent
}

// eventHub fans out item changes to connected clients (Server-Sent Events).
// Events only carry the item id; clients reload the list, so access rules stay in one place.
type eventHub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: map[*subscriber]struct{}{}}
}

func (h *eventHub) subscribe(user authUser) *subscriber {
	s := &subscriber{user: user, ch: make(chan itemEvent, 16)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *eventHub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// publish notifies every subscriber allowed to see the item. Slow clients drop events
// rather than block the request; they resync on reconnect or when the app is reopened.
func (h *eventHub) publish(eventType string, item Item) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !canAccessItem(s.user, item) {
			continue
		}
		select {
		case s.ch <- itemEvent{Type: eventType, ID: item.ID}:
		default:
		}
	}
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := a.authenticatedUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform keeps reverse proxies (e.g. Caddy's encode) from compressing/buffering the stream.
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Ask the browser to reconnect quickly after a drop.
	if _, err := fmt.Fprint(w, "retry: 3000\n: connected\n\n"); err != nil || rc.Flush() != nil {
		return
	}

	sub := a.events.subscribe(user)
	defer a.events.unsubscribe(sub)
	heartbeat := time.NewTicker(eventHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-sub.ch:
			payload, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "event: item\ndata: %s\n\n", payload); err != nil || rc.Flush() != nil {
				return
			}
		case <-heartbeat.C:
			// Drop the stream once the session or token has been revoked.
			current, ok := a.authenticatedUser(r)
			if !ok {
				return
			}
			a.events.mu.Lock()
			sub.user = current // pick up admin promotion/demotion
			a.events.mu.Unlock()
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
