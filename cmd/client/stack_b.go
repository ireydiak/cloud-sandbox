package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func runStackB(o outputs, withB5 bool) []Result {
	out := testB123(o)
	out = append(out, testB4(o))
	if withB5 {
		out = append(out, testB5(o))
	}
	return out
}

func dialB(o outputs, idx int, name string) (*sess, error) {
	var err error
	for i := 0; i < 3; i++ {
		var s *sess
		if s, err = dial(ctxBG, idx, name, o.WSURL+"?name="+name); err == nil {
			return s, nil
		}
		time.Sleep(time.Second)
	}
	return nil, err
}

// ---- B1 / B2 / B3 ----

func testB123(o outputs) []Result {
	const n = 30
	logf("B1-B3: %d sockets, bursts of 2 concurrent messages in groups of 4 connections", n)
	ss := make([]*sess, n)
	parallel(n, func(i int) {
		s, err := dialB(o, i, fmt.Sprintf("b-%02d", i))
		if err != nil {
			logf("dial b-%02d: %v", i, err)
			return
		}
		ss[i] = s
	})

	// Phase 1: concurrent bursts, with a handler delay to force overlap.
	// Account concurrency can be as low as 10, so keep in-flight messages
	// under that: groups of 4 connections x 2 messages = 8 at a time.
	const group, passes = 4, 3
	for p := 0; p < passes; p++ {
		for g := 0; g < n; g += group {
			parallel(group, func(k int) {
				i := g + k
				if i >= n || ss[i] == nil {
					return
				}
				for m := 0; m < 2; m++ {
					ss[i].send(map[string]any{"seq": int64(p*2 + m), "sleep_ms": 400})
				}
			})
			time.Sleep(700 * time.Millisecond)
		}
	}
	time.Sleep(5 * time.Second)

	b1 := Result{ID: "B1", Title: "Messages on one connection can hit different execution environments"}
	b2 := Result{ID: "B2", Title: "connectionId is stable for the life of the connection"}
	b3 := Result{ID: "B3", Title: "State must live outside Lambda"}

	envs, streams := map[string]bool{}, map[string]bool{}
	multiEnv, replies, unparsed := 0, 0, 0
	b2ok, dbWrong, live, noReply := true, 0, 0, 0
	for _, s := range ss {
		if s == nil {
			continue
		}
		live++
		fr := s.frames("reply")
		if len(fr) == 0 {
			noReply++
			continue
		}
		unparsed += len(s.frames("unparsed")) + len(s.frames(""))
		replies += len(fr)
		e, c := map[string]bool{}, map[string]bool{}
		for _, f := range fr {
			e[f.EnvID], c[f.ConnectionID] = true, true
			envs[f.EnvID], streams[f.LogStream] = true, true
			if f.DBName != s.name {
				dbWrong++
			}
		}
		if len(e) > 1 {
			multiEnv++
		}
		if len(c) != 1 {
			b2ok = false
		}
		b1.Details = append(b1.Details, fmt.Sprintf("%s: %d replies, %d environments", s.name, len(fr), len(e)))
		b2.Details = append(b2.Details, fmt.Sprintf("%s: connection ids %v", s.name, keys(c)))
	}
	b1.Pass = multiEnv > 0
	b1.Summary = fmt.Sprintf("%d/%d connections saw >1 environment; %d environments, %d log streams overall; %d replies, %d error/unparsed frames (throttling shows up here)",
		multiEnv, live, len(envs), len(streams), replies, unparsed)
	b2.Pass = b2ok && live-noReply > 0
	b2.Summary = fmt.Sprintf("connectionId constant on all %d connections that got replies: %v (%d connections got no reply, excluded)", live-noReply, b2ok, noReply)

	// Phase 2: strictly sequential messages on many connections at once, so
	// every counter step on a connection is directly comparable.
	for _, s := range ss {
		if s != nil {
			for len(s.in) > 0 {
				<-s.in
			}
		}
	}
	anomalies, steps := 0, 0
	var sample []string
	var aMu = make(chan struct{}, 1)
	sem := make(chan struct{}, 6) // stay under the Lambda concurrency limit
	parallel(n, func(i int) {
		s := ss[i]
		if s == nil {
			return
		}
		sem <- struct{}{}
		defer func() { <-sem }()
		var prev int64
		for k := 0; k < 10; k++ {
			s.send(map[string]any{"seq": 1000 + k})
			f, ok := s.waitType("reply", 10*time.Second)
			if !ok {
				continue
			}
			if f.DBName != s.name {
				aMu <- struct{}{}
				dbWrong++
				<-aMu
			}
			if prev != 0 {
				aMu <- struct{}{}
				steps++
				if f.Counter != prev+1 {
					anomalies++
					if len(sample) < 8 {
						sample = append(sample, fmt.Sprintf("%s: counter %d -> %d (env %s)", s.name, prev, f.Counter, f.EnvID))
					}
				}
				<-aMu
			}
			prev = f.Counter
		}
	})
	b3.Details = sample
	b3.Pass = dbWrong == 0 && anomalies > 0
	b3.Summary = fmt.Sprintf("DynamoDB name wrong on %d replies; in-memory counter was not +1 on %d of %d sequential steps",
		dbWrong, anomalies, steps)
	if dbWrong == 0 && anomalies == 0 {
		b3.Summary += " (counter looked consistent: probably only one environment was ever used; not evidence either way)"
	}
	for _, s := range ss {
		if s != nil {
			s.close()
		}
	}
	return []Result{b1, b2, b3}
}

// ---- B4 ----

func invokePush(o outputs, connID, msg string) (resp struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	StatusCode int    `json:"status_code"`
}, err error) {
	payload, _ := json.Marshal(map[string]string{"connection_id": connID, "message": msg})
	outFile, _ := os.CreateTemp("", "push-*.json")
	outFile.Close()
	defer os.Remove(outFile.Name())
	if b, e := awsCLI(o, "lambda", "invoke", "--function-name", o.PushFunc,
		"--cli-binary-format", "raw-in-base64-out", "--payload", string(payload), outFile.Name()); e != nil {
		return resp, fmt.Errorf("%s", b)
	}
	b, _ := os.ReadFile(outFile.Name())
	ev("push_invoke", map[string]any{"connection_id": connID, "response": string(b)})
	return resp, json.Unmarshal(b, &resp)
}

func testB4(o outputs) Result {
	r := Result{ID: "B4", Title: "Any code can push to a connection via the Management API"}
	logf("B4: push via lambda invoke, then to a closed connection")
	s, err := dialB(o, 0, "push-test")
	if err != nil {
		r.Summary = "dial failed: " + err.Error()
		return r
	}
	s.send(map[string]any{"seq": 1})
	f, ok := s.waitType("reply", 15*time.Second)
	if !ok {
		r.Summary = "no reply to learn connectionId"
		return r
	}
	resp, err := invokePush(o, f.ConnectionID, "hello-from-push")
	if err != nil {
		r.Summary = "invoke failed: " + err.Error()
		return r
	}
	got, received := s.waitType("push", 10*time.Second)
	r.Details = append(r.Details, fmt.Sprintf("connection %s: lambda response %+v, client received push: %v (%q)", f.ConnectionID, resp, received, got.Message))

	s.close()
	time.Sleep(3 * time.Second)
	resp2, err := invokePush(o, f.ConnectionID, "after-close")
	if err != nil {
		r.Summary = "second invoke failed: " + err.Error()
		return r
	}
	r.Details = append(r.Details, fmt.Sprintf("after close: lambda response %+v", resp2))
	r.Pass = resp.OK && received && resp2.Error == "GoneException" && resp2.StatusCode == 410
	r.Summary = fmt.Sprintf("push delivered: %v; push to closed connection -> %s (%d)", received, resp2.Error, resp2.StatusCode)
	return r
}

// ---- B5 ----

func testB5(o outputs) Result {
	r := Result{ID: "B5", Title: "10-minute idle timeout closes a silent connection"}
	logf("B5: holding a silent connection for up to 12 minutes")
	s, err := dialB(o, 0, "idle-test")
	if err != nil {
		r.Summary = "dial failed: " + err.Error()
		return r
	}
	select {
	case <-s.closed:
	case <-time.After(12 * time.Minute):
	}
	code, at, closed := s.closeInfo()
	if !closed {
		r.Summary = "still open after 12 minutes"
		return r
	}
	d := at.Sub(s.openedAt)
	r.Pass = d >= 9*time.Minute && d <= 11*time.Minute
	r.Summary = fmt.Sprintf("closed after %s with code %d (want ~10m)", d.Round(time.Second), code)
	return r
}
