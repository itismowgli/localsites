package api

import (
	"strings"
	"testing"
	"time"
)

func TestApprovals(t *testing.T) {
	notify = func(string, string) {}
	var a approvals
	run := func(decide func(id string)) error {
		done := make(chan struct{})
		res := make(chan error, 1)
		go func() { res <- a.wait("share.start", "mcp", []byte(`{"folder":"shop","password":"s3cret"}`), done) }()
		var list []Approval
		for i := 0; i < 100 && len(list) == 0; i++ {
			time.Sleep(10 * time.Millisecond)
			list = a.list()
		}
		if len(list) != 1 {
			t.Fatal("request not listed")
		}
		if strings.Contains(list[0].Summary, "s3cret") || !strings.Contains(list[0].Summary, "shop") {
			t.Fatalf("summary must show the request but hide passwords: %s", list[0].Summary)
		}
		if decide == nil {
			close(done)
		} else {
			decide(list[0].ID)
		}
		return <-res
	}
	if err := run(func(id string) { a.decide(id, true) }); err != nil {
		t.Fatalf("approved: %v", err)
	}
	if err := run(func(id string) { a.decide(id, false) }); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("denied: %v", err)
	}
	if err := run(nil); err == nil {
		t.Fatal("cancelled request ran")
	}
	if len(a.list()) != 0 {
		t.Fatal("finished requests must leave the queue")
	}
	if a.decide("nope", true) == nil {
		t.Fatal("unknown id accepted")
	}
	if !isDestructive("stop") || !isDestructive("share.start") || isDestructive("sites.list") {
		t.Fatal("destructive lookup")
	}
}

// A request arriving and ending both tell waiting clients to refresh.
func TestApprovalsSignalChanges(t *testing.T) {
	notify = func(string, string) {}
	mu := make(chan struct{}, 10)
	a := approvals{onChange: func() { mu <- struct{}{} }}
	done := make(chan struct{})
	go func() { _ = a.wait("quit", "mcp", nil, done) }()
	<-mu
	close(done)
	select {
	case <-mu:
	case <-time.After(2 * time.Second):
		t.Fatal("ending a request didn't signal")
	}
}
