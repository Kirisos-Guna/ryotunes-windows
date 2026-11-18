package webplayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// cdp is a minimal Chrome DevTools Protocol client for one page target.
type cdp struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan cdpReply
	closed  chan struct{}
	err     error

	// events receives every CDP event (method + params) in order.
	events chan cdpEvent
}

type cdpReply struct {
	Result json.RawMessage
	Err    error
}

type cdpEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func dialCDP(ctx context.Context, url string) (*cdp, error) {
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(64 << 20)
	c := &cdp{
		conn:    conn,
		pending: map[int64]chan cdpReply{},
		closed:  make(chan struct{}),
		events:  make(chan cdpEvent, 512),
	}
	go c.readLoop()
	return c, nil
}

func (c *cdp) readLoop() {
	defer func() {
		c.mu.Lock()
		for id, ch := range c.pending {
			ch <- cdpReply{Err: errors.New("browser connection closed")}
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.events)
		close(c.closed)
	}()
	for {
		var msg struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := c.conn.ReadJSON(&msg); err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				reply := cdpReply{Result: msg.Result}
				if msg.Error != nil {
					reply.Err = errors.New(msg.Error.Message)
				}
				ch <- reply
			}
			continue
		}
		if msg.Method != "" {
			select {
			case c.events <- cdpEvent{Method: msg.Method, Params: msg.Params}:
			default: // a stalled consumer must not block replies
			}
		}
	}
}

// send runs one CDP method and returns its result.
func (c *cdp) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("browser connection closed: %w", c.err)
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if params == nil {
		params = struct{}{}
	}
	c.writeMu.Lock()
	err := c.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case reply := <-ch:
		return reply.Result, reply.Err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *cdp) close() {
	_ = c.conn.Close()
}
