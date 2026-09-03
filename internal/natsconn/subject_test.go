package natsconn

import "testing"

func TestIsSystemSubject(t *testing.T) {
	tests := []struct {
		subject string
		want    bool
	}{
		{"$SYS.REQ.SERVER.PING", true},
		{"$JS.EVENT.ADVISORY.API", true},
		{"$KV.config.db", true},
		{"_INBOX.abc123", true},
		{"orders.new", false},
		{"orders.$weird", false}, // only the first byte counts
		{"a", false},
		{"", false}, // must not panic on empty
	}
	for _, tt := range tests {
		if got := IsSystemSubject(tt.subject); got != tt.want {
			t.Errorf("IsSystemSubject(%q) = %v, want %v", tt.subject, got, tt.want)
		}
	}
}

func TestCanExcludeSystem(t *testing.T) {
	tests := []struct {
		subject string
		want    bool
	}{
		{">", true},
		{"*", true},
		{"*.foo", true},

		// Asking for system traffic deliberately: excluding it would filter
		// the subscription down to nothing, so the option must not apply.
		{"$JS.EVENT.>", false},
		{"$SYS.>", false},
		{"_INBOX.>", false},

		// No wildcard in the first token means no system subject can match.
		{"orders.>", false},
		{"orders.*", false},
		{"orders.new", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := CanExcludeSystem(tt.subject); got != tt.want {
			t.Errorf("CanExcludeSystem(%q) = %v, want %v", tt.subject, got, tt.want)
		}
	}
}
