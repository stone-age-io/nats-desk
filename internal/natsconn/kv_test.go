package natsconn

import (
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

// The UI keys off the wire values, and jetstream's own String() does not
// produce them - it returns "KeyValueDeleteOp" where the KV-Operation header
// says "DEL". Getting this wrong silently breaks the key list: deletes stop
// removing rows.
func TestKvOpName(t *testing.T) {
	tests := []struct {
		op   jetstream.KeyValueOp
		want string
	}{
		{jetstream.KeyValuePut, "PUT"},
		{jetstream.KeyValueDelete, "DEL"},
		{jetstream.KeyValuePurge, "PURGE"},
	}
	for _, tt := range tests {
		if got := kvOpName(tt.op); got != tt.want {
			t.Errorf("kvOpName(%v) = %q, want %q", tt.op, got, tt.want)
		}
	}
}
