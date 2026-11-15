package main

import (
	"bytes"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CoT (Cursor on Target) relay.
//
// TAK clients open a TCP connection and stream <event>…</event> XML documents
// with no framing. We split the stream into events, forward each one to every
// other connected client, and remember the latest position/marker per uid so a
// client that connects later immediately sees where everyone is.

const (
	maxEventBytes   = 2 << 20 // drop a client that sends more than this without finishing an event
	maxClients      = 64
	maxCachedEvents = 2000
	clientQueueLen  = 512
)

var (
	evOpen  = []byte("<event")
	evClose = []byte("</event>")

	reUID   = regexp.MustCompile(`(?:^|[ \t\r\n])uid\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reType  = regexp.MustCompile(`(?:^|[ \t\r\n])type\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reStale = regexp.MustCompile(`(?:^|[ \t\r\n])stale\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reLink  = regexp.MustCompile(`<link\b[^>]*?[ \t\r\n]uid\s*=\s*"([^"]*)"`)
)

func attr(re *regexp.Regexp, tag []byte) string {
	m := re.FindSubmatch(tag)
	if m == nil {
		return ""
	}
	if len(m[1]) > 0 {
		return string(m[1])
	}
	return string(m[2])
}

// partialPrefix returns how many trailing bytes of b could be the start of "<event".
func partialPrefix(b []byte) int {
	max := len(evOpen) - 1
	if len(b) < max {
		max = len(b)
	}
	for n := max; n > 0; n-- {
		if bytes.HasPrefix(evOpen, b[len(b)-n:]) {
			return n
		}
	}
	return 0
}

// nextEvent finds the next complete event in b. It returns the event, how many
// bytes of b are finished with (including skipped junk), and whether an event
// was found. When ok is false, used may still be >0 (junk that can be dropped).
func nextEvent(b []byte) (ev []byte, used int, ok bool) {
	pos := 0
	for {
		i := bytes.Index(b[pos:], evOpen)
		if i < 0 {
			return nil, len(b) - partialPrefix(b[pos:]), false
		}
		i += pos
		after := i + len(evOpen)
		if after >= len(b) {
			return nil, i, false
		}
		switch b[after] {
		case ' ', '\t', '\r', '\n', '>', '/':
		default: // e.g. "<events", not ours
			pos = after
			continue
		}
		gt := bytes.IndexByte(b[after:], '>')
		if gt < 0 {
			return nil, i, false
		}
		gt += after
		if b[gt-1] == '/' { // self-closed <event .../>
			return b[i : gt+1], gt + 1, true
		}
		end := bytes.Index(b[gt:], evClose)
		if end < 0 {
			return nil, i, false
		}
		end += gt + len(evClose)
		return b[i:end], end, true
	}
}

type client struct {
	addr    string
	conn    net.Conn
	out     chan []byte
	lastUID atomic.Value // string
	dropped atomic.Uint64
}

type cached struct {
	raw   []byte
	stale time.Time
}

type hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}

	cmu   sync.Mutex
	cache map[string]cached

	eventsIn atomic.Uint64
	dropped  atomic.Uint64
}

func newHub() *hub {
	return &hub{clients: map[*client]struct{}{}, cache: map[string]cached{}}
}

func (h *hub) clientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *hub) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("cot: accept stopped: %v", err)
			return
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(30 * time.Second)
			tc.SetNoDelay(true)
		}
		go h.serve(conn)
	}
}

func (h *hub) serve(conn net.Conn) {
	c := &client{addr: conn.RemoteAddr().String(), conn: conn, out: make(chan []byte, clientQueueLen)}
	c.lastUID.Store("")

	h.mu.Lock()
	if len(h.clients) >= maxClients {
		h.mu.Unlock()
		log.Printf("cot: refusing %s (max %d clients)", c.addr, maxClients)
		conn.Close()
		return
	}
	h.clients[c] = struct{}{}
	n := len(h.clients)
	h.mu.Unlock()
	log.Printf("cot: + %s (%d connected)", c.addr, n)

	go c.writeLoop()
	h.replayTo(c)
	h.readLoop(c)

	h.mu.Lock()
	delete(h.clients, c)
	n = len(h.clients)
	h.mu.Unlock()
	close(c.out) // safe: no sender can reach c.out once it is out of h.clients
	conn.Close()
	uid, _ := c.lastUID.Load().(string)
	log.Printf("cot: - %s uid=%q (%d connected, %d events dropped)", c.addr, uid, n, c.dropped.Load())
}

func (c *client) writeLoop() {
	for raw := range c.out {
		c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := c.conn.Write(raw); err != nil {
			c.conn.Close() // makes readLoop return
			for range c.out {
			}
			return
		}
	}
}

func (h *hub) readLoop(c *client) {
	tmp := make([]byte, 16<<10)
	pending := make([]byte, 0, 32<<10)
	for {
		n, err := c.conn.Read(tmp)
		if n > 0 {
			pending = append(pending, tmp[:n]...)
			off := 0
			for {
				ev, used, ok := nextEvent(pending[off:])
				off += used
				if !ok {
					break
				}
				h.handleEvent(c, ev)
			}
			if off > 0 {
				pending = pending[:copy(pending, pending[off:])]
			}
			if len(pending) > maxEventBytes {
				log.Printf("cot: %s sent an event over %d bytes, disconnecting", c.addr, maxEventBytes)
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *hub) handleEvent(from *client, ev []byte) {
	raw := append([]byte(nil), ev...)
	gt := bytes.IndexByte(raw, '>')
	tag := raw[len(evOpen) : gt+1]
	typ := attr(reType, tag)
	uid := attr(reUID, tag)
	h.eventsIn.Add(1)

	if *flagVerbose {
		log.Printf("cot: %s uid=%q type=%q %d bytes", from.addr, uid, typ, len(raw))
	}
	if uid != "" && strings.HasPrefix(typ, "a-f-") {
		from.lastUID.Store(uid)
	}

	switch {
	case typ == "t-x-c-t": // ping; nothing to relay
		return
	case typ == "t-x-d-d": // delete: forget the referenced uids
		h.cmu.Lock()
		for _, m := range reLink.FindAllSubmatch(raw, -1) {
			delete(h.cache, string(m[1]))
		}
		h.cmu.Unlock()
	case uid != "" && (strings.HasPrefix(typ, "a-") || strings.HasPrefix(typ, "b-m-p")):
		stale := time.Now().Add(5 * time.Minute)
		if s := attr(reStale, tag); s != "" {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				stale = t
			}
		}
		h.cmu.Lock()
		if _, exists := h.cache[uid]; exists || len(h.cache) < maxCachedEvents {
			h.cache[uid] = cached{raw: raw, stale: stale}
		}
		h.cmu.Unlock()
	}

	h.mu.RLock()
	for c := range h.clients {
		if c == from {
			continue
		}
		select {
		case c.out <- raw:
		default:
			c.dropped.Add(1)
			h.dropped.Add(1)
		}
	}
	h.mu.RUnlock()
}

// replayTo sends the latest known (non-stale) positions and markers to a new client.
func (h *hub) replayTo(c *client) {
	now := time.Now()
	h.cmu.Lock()
	var batch [][]byte
	for _, e := range h.cache {
		if e.stale.After(now) {
			batch = append(batch, e.raw)
		}
	}
	h.cmu.Unlock()
	for _, raw := range batch {
		select {
		case c.out <- raw:
		default:
			return
		}
	}
	if len(batch) > 0 {
		log.Printf("cot: replayed %d cached events to %s", len(batch), c.addr)
	}
}

func (h *hub) prune() int {
	now := time.Now()
	h.cmu.Lock()
	defer h.cmu.Unlock()
	for uid, e := range h.cache {
		if !e.stale.After(now) {
			delete(h.cache, uid)
		}
	}
	return len(h.cache)
}

func (h *hub) cacheSize() int {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	return len(h.cache)
}
