package ws

import (
	"encoding/json"
	"testing"
)

// batchFrame splices pre-marshalled frames together by hand rather than
// re-encoding them, which is fast but means a mistake produces invalid JSON
// that only shows up as a silently ignored frame in the browser.
func TestBatchFrameIsValidJSON(t *testing.T) {
	batch := [][]byte{
		[]byte(`{"type":"msg","subject":"a.b","data":"aGk="}`),
		[]byte(`{"type":"msg","subject":"c.d","data":""}`),
	}

	var out struct {
		Type   string            `json:"type"`
		Frames []json.RawMessage `json:"frames"`
	}
	if err := json.Unmarshal(batchFrame(batch), &out); err != nil {
		t.Fatalf("batchFrame produced invalid JSON: %v", err)
	}
	if out.Type != "batch" {
		t.Errorf("type = %q, want %q", out.Type, "batch")
	}
	if len(out.Frames) != len(batch) {
		t.Fatalf("got %d frames, want %d", len(out.Frames), len(batch))
	}
	for i := range batch {
		if string(out.Frames[i]) != string(batch[i]) {
			t.Errorf("frame %d = %s, want %s", i, out.Frames[i], batch[i])
		}
	}
}

func TestBatchFrameSingle(t *testing.T) {
	b := batchFrame([][]byte{[]byte(`{"type":"msg"}`)})
	if !json.Valid(b) {
		t.Fatalf("invalid JSON for a single-frame batch: %s", b)
	}
}

// The cap is what stops a firehose reaching the renderer. Dropping must take
// from the front so the newest messages survive - a tail viewer wants the live
// edge - and must count what it dropped, because a silent gap in a message log
// is a lie.
func TestQueueDropsOldestAndCounts(t *testing.T) {
	c := &client{}
	total := maxBatch + 50
	for i := 0; i < total; i++ {
		c.queue([]byte{byte(i)})
	}

	batch, dropped := c.take()
	if len(batch) != maxBatch {
		t.Errorf("kept %d frames, want %d", len(batch), maxBatch)
	}
	if dropped != uint64(total-maxBatch) {
		t.Errorf("dropped = %d, want %d", dropped, total-maxBatch)
	}
	// The survivors must be the newest, so the first kept frame is the one
	// queued at index total-maxBatch.
	if got, want := batch[0][0], byte(total-maxBatch); got != want {
		t.Errorf("oldest kept frame = %d, want %d (newest should survive)", got, want)
	}

	// take() must reset, or the next window would re-report the same drops.
	batch, dropped = c.take()
	if batch != nil || dropped != 0 {
		t.Errorf("take() after drain = (%v, %d), want (nil, 0)", batch, dropped)
	}
}
