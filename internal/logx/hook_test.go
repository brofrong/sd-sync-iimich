package logx

import "testing"

func TestOnErrorGetsErrorMessagesOnly(t *testing.T) {
	var got []string
	OnError(func(msg string) { got = append(got, msg) })
	t.Cleanup(func() { OnError(nil) })

	Info("started", "path", "/Users/me")
	Warn("slow")
	Error("sync failed", "err", "open /Users/me/DCIM: denied")

	if len(got) != 1 || got[0] != "sync failed" {
		t.Fatalf("hook got %q, want only the error's message", got)
	}
}
