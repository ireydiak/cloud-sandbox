package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func runStackA(o outputs, a1dur time.Duration) []Result {
	var out []Result
	out = append(out, testA2(o))
	out = append(out, testA134(o, a1dur)...)
	out = append(out, testA5(o))
	out = append(out, testA6(o))
	out = append(out, testA7(o, "off"))
	out = append(out, testA7(o, "on"))
	return out
}

func wsURL(o outputs, query string) string { return "ws://" + o.ALBDNS + "/ws?" + query }

func dialRetry(o outputs, idx int, name, query string) (*sess, error) {
	var err error
	for i := 0; i < 5; i++ {
		var s *sess
		if s, err = dial(ctxBG, idx, name, wsURL(o, query)); err == nil {
			return s, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, err
}

func awsCLI(o outputs, args ...string) ([]byte, error) {
	args = append(args, "--region", o.Region)
	b, err := exec.Command("aws", args...).CombinedOutput()
	if err != nil {
		return b, fmt.Errorf("aws %s: %w: %s", strings.Join(args, " "), err, b)
	}
	return b, nil
}

// ---- A2 ----

func testA2(o outputs) Result {
	r := Result{ID: "A2", Title: "Plain HTTP requests are routed independently"}
	logf("A2: 100 x GET /whoami with fresh connections")
	counts := map[string]int{}
	var mu sync.Mutex
	errs := 0
	parallel(10, func(w int) {
		for i := 0; i < 10; i++ {
			tr := &http.Transport{DisableKeepAlives: true}
			c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
			resp, err := c.Get("http://" + o.ALBDNS + "/whoami")
			mu.Lock()
			if err != nil {
				errs++
			} else {
				var v struct {
					TaskID     string `json:"task_id"`
					RemoteAddr string `json:"remote_addr"`
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if json.Unmarshal(b, &v) == nil && v.TaskID != "" {
					counts[v.TaskID]++
					ev("whoami", map[string]any{"task_id": v.TaskID, "remote_addr": v.RemoteAddr})
				} else {
					errs++
				}
			}
			mu.Unlock()
			tr.CloseIdleConnections()
		}
	})
	r.Pass = len(counts) > 1
	r.Summary = fmt.Sprintf("%d distinct task IDs over 100 requests (%d errors)", len(counts), errs)
	for _, k := range keys(counts) {
		r.Details = append(r.Details, fmt.Sprintf("task %s: %d requests", k, counts[k]))
	}
	return r
}

// ---- A1 / A3 / A4: one shared run ----

func testA134(o outputs, dur time.Duration) []Result {
	const n = 20
	logf("A1/A3/A4: %d sockets, 1 msg/2s for %s", n, dur)
	ss := make([]*sess, n)
	parallel(n, func(i int) {
		s, err := dialRetry(o, i, fmt.Sprintf("a1-%02d", i), "ping=10")
		if err != nil {
			logf("dial a1-%02d: %v", i, err)
			return
		}
		ss[i] = s
	})
	deadline := time.Now().Add(dur)
	parallel(n, func(i int) {
		s := ss[i]
		if s == nil {
			return
		}
		for seq := int64(1); time.Now().Before(deadline); seq++ {
			if s.send(map[string]any{"seq": seq}) != nil {
				return
			}
			select {
			case <-time.After(2 * time.Second):
			case <-s.closed:
				return
			}
		}
	})
	time.Sleep(2 * time.Second)

	a1 := Result{ID: "A1", Title: "A socket is pinned to one task for its lifetime (no stickiness)"}
	a3 := Result{ID: "A3", Title: "Task sees one stable backend connection per socket"}
	a4 := Result{ID: "A4", Title: "In-memory per-connection state survives across messages"}
	a1ok, a3ok, a4ok := true, true, true
	allTasks := map[string]int{}
	pairs := map[string]bool{}
	var a1bad, a3bad, a4bad []string
	live := 0
	for _, s := range ss {
		if s == nil {
			continue
		}
		fr := s.frames("reply")
		if len(fr) == 0 {
			a1ok, a3ok, a4ok = false, false, false
			a1bad = append(a1bad, s.name+": no replies")
			continue
		}
		live++
		tasks, addrs := map[string]bool{}, map[string]bool{}
		resets := 0
		for i, f := range fr {
			tasks[f.TaskID], addrs[f.RemoteAddr] = true, true
			if f.Counter != int64(i+1) {
				resets++
			}
		}
		allTasks[fr[0].TaskID]++
		pairs[fr[0].TaskID+"|"+fr[0].RemoteAddr] = true
		if len(tasks) != 1 {
			a1ok = false
			a1bad = append(a1bad, fmt.Sprintf("%s saw tasks %v", s.name, keys(tasks)))
		}
		if len(addrs) != 1 {
			a3ok = false
			a3bad = append(a3bad, fmt.Sprintf("%s saw remote addrs %v", s.name, keys(addrs)))
		}
		if resets > 0 {
			a4ok = false
			a4bad = append(a4bad, fmt.Sprintf("%s: %d counter anomalies", s.name, resets))
		}
		a4.Details = append(a4.Details, fmt.Sprintf("%s: %d msgs, counter %d..%d", s.name, len(fr), fr[0].Counter, fr[len(fr)-1].Counter))
		a3.Details = append(a3.Details, fmt.Sprintf("%s: task %s via %s", s.name, fr[0].TaskID, fr[0].RemoteAddr))
		a1.Details = append(a1.Details, fmt.Sprintf("%s: task %s (%d msgs)", s.name, fr[0].TaskID, len(fr)))
		if _, _, closed := s.closeInfo(); closed {
			a1ok = false
			a1bad = append(a1bad, s.name+": connection closed early")
		}
	}
	for _, s := range ss {
		if s != nil {
			s.close()
		}
	}
	a1.Pass = a1ok && live == n && len(allTasks) > 1
	a1.Summary = fmt.Sprintf("%d/%d sockets live; spread across %d tasks %v; violations: %v", live, n, len(allTasks), allTasks, a1bad)
	a3.Pass = a3ok && live == n && len(pairs) == n
	a3.Summary = fmt.Sprintf("%d distinct (task, backend addr) pairs for %d sockets; violations: %v", len(pairs), live, a3bad)
	a4.Pass = a4ok && live == n
	a4.Summary = fmt.Sprintf("counter incremented by exactly 1 on %d/%d sockets; violations: %v", live-len(a4bad), live, a4bad)
	return []Result{a1, a3, a4}
}

// ---- A5 ----

func testA5(o outputs) Result {
	r := Result{ID: "A5", Title: "A task cannot reach sockets held by other tasks (no backplane)"}
	const n = 20
	logf("A5: %d sockets, one POST /broadcast", n)
	ss := make([]*sess, n)
	parallel(n, func(i int) {
		s, err := dialRetry(o, i, fmt.Sprintf("a5-%02d", i), "ping=10")
		if err != nil {
			return
		}
		s.send(map[string]any{"seq": 1})
		s.waitType("reply", 10*time.Second)
		ss[i] = s
	})
	resp, err := http.Post("http://"+o.ALBDNS+"/broadcast", "text/plain", strings.NewReader("hello-a5"))
	if err != nil {
		r.Summary = "broadcast request failed: " + err.Error()
		return r
	}
	var br struct {
		TaskID string `json:"task_id"`
		Sent   int    `json:"sent"`
	}
	json.NewDecoder(resp.Body).Decode(&br)
	resp.Body.Close()
	time.Sleep(3 * time.Second)

	onTask, gotOnTask, offTask, gotOffTask := 0, 0, 0, 0
	for _, s := range ss {
		if s == nil {
			continue
		}
		reps := s.frames("reply")
		if len(reps) == 0 {
			continue
		}
		got := len(s.frames("broadcast")) > 0
		on := reps[0].TaskID == br.TaskID
		r.Details = append(r.Details, fmt.Sprintf("%s: task %s received=%v", s.name, reps[0].TaskID, got))
		switch {
		case on:
			onTask++
			if got {
				gotOnTask++
			}
		default:
			offTask++
			if got {
				gotOffTask++
			}
		}
		s.close()
	}
	r.Pass = onTask > 0 && offTask > 0 && gotOnTask == onTask && gotOffTask == 0
	r.Summary = fmt.Sprintf("request handled by task %s (reported sent=%d); on that task %d/%d clients received it; on other tasks %d/%d received it",
		br.TaskID, br.Sent, gotOnTask, onTask, gotOffTask, offTask)
	if onTask == 0 || offTask == 0 {
		r.Summary += " (inconclusive: clients were not spread across tasks)"
	}
	return r
}

// ---- A6 ----

func testA6(o outputs) Result {
	r := Result{ID: "A6", Title: "ALB idle timeout (20s) closes silent connections"}
	logf("A6: silent socket vs pinged socket")
	silent, err1 := dialRetry(o, 0, "a6-silent", "ping=off")
	pinged, err2 := dialRetry(o, 1, "a6-pinged", "ping=10")
	if err1 != nil || err2 != nil {
		r.Summary = fmt.Sprintf("dial failed: %v %v", err1, err2)
		return r
	}
	var silentAfter time.Duration
	select {
	case <-silent.closed:
		silentAfter = silent.closedAt.Sub(silent.openedAt)
	case <-time.After(60 * time.Second):
	}
	// Wait out the full 60s on the pinged socket, then probe it.
	if rest := 60*time.Second - time.Since(pinged.openedAt); rest > 0 {
		time.Sleep(rest)
	}
	alive := false
	if pinged.send(map[string]any{"seq": 1}) == nil {
		_, alive = pinged.waitType("reply", 5*time.Second)
	}
	code, _, _ := silent.closeInfo()
	silentOK := silentAfter >= 15*time.Second && silentAfter <= 25*time.Second
	r.Pass = silentOK && alive
	r.Summary = fmt.Sprintf("silent socket closed after %.1fs (close code %d, want 15-25s); pinged socket alive at 60s: %v",
		silentAfter.Seconds(), code, alive)
	pinged.close()
	return r
}

// ---- A7 ----

func testA7(o outputs, mode string) Result {
	r := Result{ID: "A7-" + mode, Title: fmt.Sprintf("Draining keeps sockets until deregistration delay (graceful close %s)", mode)}
	const n = 12
	logf("A7 (%s): opening %d sockets, then force-new-deployment", mode, n)
	ss := make([]*sess, n)
	parallel(n, func(i int) {
		s, err := dialRetry(o, i, fmt.Sprintf("a7%s-%02d", mode, i), "graceful="+mode)
		if err != nil {
			return
		}
		ss[i] = s
		go func() { // keep traffic flowing so the ALB idle timeout is irrelevant
			for seq := int64(1); ; seq++ {
				if s.send(map[string]any{"seq": seq}) != nil {
					return
				}
				select {
				case <-time.After(2 * time.Second):
				case <-s.closed:
					return
				}
			}
		}()
	})
	oldTasks := map[string]bool{}
	live := []*sess{}
	for _, s := range ss {
		if s == nil {
			continue
		}
		if f, ok := s.waitType("reply", 10*time.Second); ok {
			oldTasks[f.TaskID] = true
			live = append(live, s)
		}
	}
	if len(live) == 0 {
		r.Summary = "no live sockets"
		return r
	}

	t0 := time.Now()
	if b, err := awsCLI(o, "ecs", "update-service", "--cluster", o.Cluster, "--service", o.Service, "--force-new-deployment", "--query", "service.deployments[0].id", "--output", "text"); err != nil {
		r.Summary = "update-service failed: " + string(b)
		return r
	}
	ev("deployment", map[string]any{"mode": mode, "triggered_at": t0.UTC()})

	deadline := time.Now().Add(6 * time.Minute)
	for _, s := range live {
		select {
		case <-s.closed:
		case <-time.After(time.Until(deadline)):
		}
	}

	allClosed, graceful1001, notClean := true, 0, 0
	for _, s := range live {
		code, at, closed := s.closeInfo()
		reps := s.frames("reply")
		task := reps[len(reps)-1].TaskID
		if !closed {
			allClosed = false
			r.Details = append(r.Details, fmt.Sprintf("%s (task %s): still open after 6 min", s.name, task))
			continue
		}
		line := fmt.Sprintf("%s (task %s): close code %d, %.1fs after deployment trigger", s.name, task, code, at.Sub(t0).Seconds())
		if st := s.frames("sigterm"); len(st) > 0 {
			line += fmt.Sprintf(", %.1fs after SIGTERM notice", at.Sub(st[0].RecvAt).Seconds())
		} else {
			line += ", no SIGTERM notice received before close"
		}
		if code == 1001 {
			graceful1001++
		} else {
			notClean++
			line += fmt.Sprintf(" (not a 1001; err: %v)", s.closeErr)
		}
		r.Details = append(r.Details, line)
	}

	logf("A7 (%s): waiting for service to stabilise", mode)
	awsCLI(o, "ecs", "wait", "services-stable", "--cluster", o.Cluster, "--services", o.Service)

	// Reconnecting clients must land on tasks that did not exist before.
	newOK, reconnected := 0, 0
	for i := range live {
		s, err := dialRetry(o, i, fmt.Sprintf("a7%s-re%02d", mode, i), "ping=10")
		if err != nil {
			continue
		}
		s.send(map[string]any{"seq": 1})
		if f, ok := s.waitType("reply", 10*time.Second); ok {
			reconnected++
			if !oldTasks[f.TaskID] {
				newOK++
			}
			r.Details = append(r.Details, fmt.Sprintf("reconnect %d -> task %s (new: %v)", i, f.TaskID, !oldTasks[f.TaskID]))
		}
		s.close()
	}

	var closeOK bool
	want := "ALB cut (any code but 1001)"
	if mode == "on" {
		closeOK, want = graceful1001 == len(live), "server close 1001"
	} else {
		closeOK = notClean == len(live)
	}
	r.Pass = allClosed && closeOK && reconnected == len(live) && newOK == reconnected
	r.Summary = fmt.Sprintf("%d sockets over old tasks %v; expected %s: %d x 1001, %d other; reconnects on new tasks: %d/%d",
		len(live), keys(oldTasks), want, graceful1001, notClean, newOK, reconnected)
	return r
}

var ctxBG = context.Background()
