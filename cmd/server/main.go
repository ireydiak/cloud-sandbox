// Command server is the ECS-side WebSocket server for stack A.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

type frame struct {
	Type       string    `json:"type"`
	TaskID     string    `json:"task_id"`
	RemoteAddr string    `json:"remote_addr,omitempty"`
	ConnID     string    `json:"conn_id,omitempty"`
	Counter    int64     `json:"counter,omitempty"`
	Seq        int64     `json:"seq"`
	Message    string    `json:"message,omitempty"`
	Ts         time.Time `json:"ts"`
}

type conn struct {
	ws       *websocket.Conn
	id       string
	remote   string
	graceful bool
}

type server struct {
	taskID        string
	gracefulDef   bool
	gracefulDelay time.Duration

	mu    sync.Mutex
	conns map[*conn]struct{}
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	s := &server{
		taskID:        resolveTaskID(),
		gracefulDef:   os.Getenv("GRACEFUL_CLOSE") == "true",
		gracefulDelay: envDuration("GRACEFUL_DELAY", 10*time.Second),
		conns:         map[*conn]struct{}{},
	}
	slog.Info("starting", "task_id", s.taskID, "graceful_default", s.gracefulDef, "graceful_delay", s.gracefulDelay.String())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /whoami", s.whoami)
	mux.HandleFunc("GET /ws", s.ws)
	mux.HandleFunc("POST /broadcast", s.broadcast)

	addr := ":" + envOr("PORT", "8080")
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	got := <-sig
	s.shutdown(got)
}

// shutdown tells every client about the signal, closes graceful connections
// with 1001 after the configured delay, then waits for the rest to go away.
// Non-graceful sockets are deliberately left open: the lab wants to see who
// cuts them (the ALB at the deregistration delay, or ECS with SIGKILL).
func (s *server) shutdown(sig os.Signal) {
	start := time.Now()
	slog.Info("signal received", "signal", sig.String(), "open_conns", s.count(), "at", start.UTC())
	s.sendAll(frame{Type: "sigterm", Message: "SIGTERM received"})

	time.Sleep(s.gracefulDelay)

	var wg sync.WaitGroup
	for _, c := range s.snapshot() {
		if !c.graceful {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			slog.Info("closing 1001", "conn_id", c.id, "since_sigterm", time.Since(start).String())
			c.ws.Close(websocket.StatusGoingAway, "server shutting down")
		}()
	}
	wg.Wait()

	for s.count() > 0 {
		time.Sleep(500 * time.Millisecond)
	}
	slog.Info("all sockets gone, exiting", "since_sigterm", time.Since(start).String())
}

func (s *server) whoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"task_id": s.taskID, "remote_addr": r.RemoteAddr})
}

func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		slog.Error("accept", "err", err)
		return
	}
	cn := &conn{ws: c, id: randID(), remote: r.RemoteAddr, graceful: s.gracefulDef}
	switch q.Get("graceful") {
	case "on":
		cn.graceful = true
	case "off":
		cn.graceful = false
	}
	s.add(cn)
	defer func() { s.remove(cn); c.CloseNow() }()
	slog.Info("ws open", "conn_id", cn.id, "remote_addr", cn.remote, "graceful", cn.graceful, "ping", q.Get("ping"))

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if iv, ok := pingInterval(q.Get("ping")); ok {
		go func() {
			t := time.NewTicker(iv)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					pctx, pc := context.WithTimeout(ctx, 5*time.Second)
					err := c.Ping(pctx)
					pc()
					if err != nil {
						return
					}
				}
			}
		}()
	}

	var counter int64 // per-connection, in-memory
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			slog.Info("ws closed", "conn_id", cn.id, "remote_addr", cn.remote, "code", int(websocket.CloseStatus(err)), "err", err.Error())
			return
		}
		var in struct {
			Seq int64 `json:"seq"`
		}
		_ = json.Unmarshal(data, &in)
		counter++
		if err := writeFrame(ctx, c, frame{
			Type: "reply", TaskID: s.taskID, RemoteAddr: cn.remote, ConnID: cn.id,
			Counter: counter, Seq: in.Seq, Ts: time.Now().UTC(),
		}); err != nil {
			return
		}
	}
}

// broadcast only reaches sockets held by THIS task: there is no backplane.
func (s *server) broadcast(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = "hello"
	}
	n := s.sendAll(frame{Type: "broadcast", Message: msg})
	slog.Info("broadcast", "sent", n)
	writeJSON(w, map[string]any{"task_id": s.taskID, "sent": n})
}

func (s *server) sendAll(f frame) int {
	f.TaskID, f.Ts = s.taskID, time.Now().UTC()
	var n atomic.Int64
	var wg sync.WaitGroup
	for _, c := range s.snapshot() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if writeFrame(ctx, c.ws, f) == nil {
				n.Add(1)
			}
		}()
	}
	wg.Wait()
	return int(n.Load())
}

func (s *server) add(c *conn)    { s.mu.Lock(); s.conns[c] = struct{}{}; s.mu.Unlock() }
func (s *server) remove(c *conn) { s.mu.Lock(); delete(s.conns, c); s.mu.Unlock() }
func (s *server) count() int     { s.mu.Lock(); defer s.mu.Unlock(); return len(s.conns) }
func (s *server) snapshot() []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, c)
	}
	return out
}

func writeFrame(ctx context.Context, c *websocket.Conn, f frame) error {
	b, _ := json.Marshal(f)
	return c.Write(ctx, websocket.MessageText, b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// pingInterval: absent -> 10s, "off" -> disabled, number -> that many seconds.
func pingInterval(v string) (time.Duration, bool) {
	switch v {
	case "off":
		return 0, false
	case "":
		return 10 * time.Second, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 10 * time.Second, true
	}
	return time.Duration(n) * time.Second, true
}

// resolveTaskID reads the task ARN from the ECS v4 metadata endpoint and
// returns its last path segment.
func resolveTaskID() string {
	base := os.Getenv("ECS_CONTAINER_METADATA_URI_V4")
	if base == "" {
		h, _ := os.Hostname()
		return "local-" + h
	}
	for i := 0; i < 30; i++ {
		resp, err := http.Get(base + "/task")
		if err == nil {
			var t struct {
				TaskARN string `json:"TaskARN"`
			}
			err = json.NewDecoder(resp.Body).Decode(&t)
			resp.Body.Close()
			if err == nil && t.TaskARN != "" {
				return t.TaskARN[strings.LastIndex(t.TaskARN, "/")+1:]
			}
		}
		time.Sleep(time.Second)
	}
	slog.Error("could not read task metadata")
	os.Exit(1)
	return ""
}

func randID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envDuration(k string, d time.Duration) time.Duration {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return time.Duration(n) * time.Second
	}
	return d
}
