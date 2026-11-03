// Command client runs the hypothesis tests and prints a PASS/FAIL report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type outputs struct {
	ALBDNS     string
	WSURL      string
	PushFunc   string
	Cluster    string
	Service    string
	Region     string
	ECRRepoURL string
}

func loadOutputs(path string) (outputs, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return outputs{}, err
	}
	var raw map[string]struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return outputs{}, err
	}
	return outputs{
		ALBDNS: raw["alb_dns_name"].Value, WSURL: raw["ws_api_url"].Value,
		PushFunc: raw["push_function_name"].Value, Cluster: raw["ecs_cluster_name"].Value,
		Service: raw["ecs_service_name"].Value, Region: raw["region"].Value,
	}, nil
}

// Result is one hypothesis verdict plus the evidence behind it.
type Result struct {
	ID, Title string
	Pass      bool
	Summary   string
	Details   []string
}

// Frame is the union of everything the servers send back.
type Frame struct {
	Type         string    `json:"type"`
	TaskID       string    `json:"task_id"`
	RemoteAddr   string    `json:"remote_addr"`
	ConnID       string    `json:"conn_id"`
	Counter      int64     `json:"counter"`
	Seq          int64     `json:"seq"`
	Message      string    `json:"message"`
	Ts           time.Time `json:"ts"`
	EnvID        string    `json:"env_id"`
	LogStream    string    `json:"log_stream"`
	RequestID    string    `json:"request_id"`
	ConnectionID string    `json:"connection_id"`
	DBName       string    `json:"db_name"`

	RecvAt time.Time `json:"-"`
}

// ---- evidence log (JSONL) ----

var (
	evMu   sync.Mutex
	evFile *os.File
)

func ev(kind string, fields map[string]any) {
	if evFile == nil {
		return
	}
	fields["kind"], fields["at"] = kind, time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(fields)
	evMu.Lock()
	evFile.Write(append(b, '\n'))
	evMu.Unlock()
}

// ---- websocket session ----

type sess struct {
	idx      int
	name     string
	c        *websocket.Conn
	openedAt time.Time

	mu       sync.Mutex
	all      []Frame
	in       chan Frame
	closed   chan struct{}
	closeErr error
	closedAt time.Time
}

func dial(ctx context.Context, idx int, name, url string) (*sess, error) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, url, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	s := &sess{idx: idx, name: name, c: c, openedAt: time.Now(), in: make(chan Frame, 4096), closed: make(chan struct{})}
	go s.readLoop()
	return s, nil
}

func (s *sess) readLoop() {
	for {
		_, data, err := s.c.Read(context.Background())
		if err != nil {
			s.mu.Lock()
			s.closeErr, s.closedAt = err, time.Now()
			s.mu.Unlock()
			ev("close", map[string]any{"conn": s.name, "code": int(websocket.CloseStatus(err)), "err": err.Error(), "after_s": time.Since(s.openedAt).Seconds()})
			close(s.closed)
			return
		}
		var f Frame
		if json.Unmarshal(data, &f) != nil {
			f = Frame{Type: "unparsed", Message: string(data)}
		}
		f.RecvAt = time.Now()
		ev("frame", map[string]any{"conn": s.name, "raw": string(data)})
		s.mu.Lock()
		s.all = append(s.all, f)
		s.mu.Unlock()
		select {
		case s.in <- f:
		default:
		}
	}
}

func (s *sess) send(v any) error {
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.c.Write(ctx, websocket.MessageText, b)
}

// waitType returns the next frame of the given type, or false on timeout/close.
func (s *sess) waitType(typ string, d time.Duration) (Frame, bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case f := <-s.in:
			if f.Type == typ {
				return f, true
			}
		case <-s.closed:
			select { // drain anything delivered just before close
			case f := <-s.in:
				if f.Type == typ {
					return f, true
				}
				continue
			default:
			}
			return Frame{}, false
		case <-t.C:
			return Frame{}, false
		}
	}
}

func (s *sess) frames(typ string) []Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Frame
	for _, f := range s.all {
		if f.Type == typ {
			out = append(out, f)
		}
	}
	return out
}

func (s *sess) closeInfo() (code int, at time.Time, ok bool) {
	select {
	case <-s.closed:
		s.mu.Lock()
		defer s.mu.Unlock()
		return int(websocket.CloseStatus(s.closeErr)), s.closedAt, true
	default:
		return 0, time.Time{}, false
	}
}

func (s *sess) close() { s.c.Close(websocket.StatusNormalClosure, "bye") }

// ---- helpers ----

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); fn(i) }()
	}
	wg.Wait()
}

func report(results []Result, evPath string) int {
	fmt.Println("\n================ REPORT ================")
	fails := 0
	for _, r := range results {
		tag := "PASS"
		if !r.Pass {
			tag = "FAIL"
			fails++
		}
		fmt.Printf("\n[%s] %s  %s\n       %s\n", tag, r.ID, r.Title, r.Summary)
		for _, d := range r.Details {
			fmt.Printf("         %s\n", d)
		}
	}
	fmt.Printf("\n%d/%d passed. Raw evidence: %s\n", len(results)-fails, len(results), evPath)
	if fails > 0 {
		return 1
	}
	return 0
}

func main() {
	stack := flag.String("stack", "all", "a, b or all")
	outPath := flag.String("outputs", "build/outputs.json", "tofu output -json file")
	evDir := flag.String("evidence-dir", "build", "where to write the evidence JSONL")
	b5 := flag.Bool("b5", false, "also run the slow B5 idle-timeout test")
	a1dur := flag.Duration("a1-duration", 3*time.Minute, "A1/A3/A4 duration")
	flag.Parse()

	o, err := loadOutputs(*outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read outputs (run make deploy first):", err)
		os.Exit(2)
	}
	os.MkdirAll(*evDir, 0o755)
	evPath := fmt.Sprintf("%s/evidence-%s.jsonl", *evDir, time.Now().Format("20060102-150405"))
	evFile, err = os.Create(evPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer evFile.Close()

	var results []Result
	if *stack == "a" || *stack == "all" {
		results = append(results, runStackA(o, *a1dur)...)
	}
	if *stack == "b" || *stack == "all" {
		results = append(results, runStackB(o, *b5)...)
	}
	os.Exit(report(results, evPath))
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s  %s\n", time.Now().Format("15:04:05"), strings.TrimSpace(fmt.Sprintf(format, a...)))
}
