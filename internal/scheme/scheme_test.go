package scheme

import "testing"

func TestAction(t *testing.T) {
	tests := []struct {
		arg     string
		want    string
		wantOK  bool
		comment string
	}{
		{"natsdesk://start", ActionStart, true, ""},
		{"natsdesk://open", ActionOpen, true, ""},

		// The form actually observed: Windows hands the shell command a URL
		// the browser has already normalised, trailing slash and all.
		{"natsdesk://start/", ActionStart, true, ""},
		{"natsdesk://open/", ActionOpen, true, ""},

		// Schemes are case insensitive, and a browser may well normalise the
		// case of either half on the way out.
		{"NATSDESK://Open", ActionOpen, true, ""},

		// A verb with no verb still means "make it run", and start is the
		// half of the pair that cannot surprise anyone.
		{"natsdesk://", ActionStart, true, ""},

		{"natsdesk://open?from=offline", ActionOpen, true, "query stripped"},
		{"natsdesk://open#x", ActionOpen, true, "fragment stripped"},

		// Everything a person types. None of these may be read as a verb, or
		// an ordinary launch would silently change behaviour.
		{"", "", false, ""},
		{"-port", "", false, ""},
		{"natsdesk", "", false, "scheme name alone is not a URL"},
		{"nats://localhost:4222", "", false, ""},
		{"natsdesk:/open", "", false, "one slash is not the scheme"},
		{"xnatsdesk://open", "", false, "prefix must not match mid-string"},
		{"http://natsdesk://open", "", false, ""},
	}

	for _, tt := range tests {
		got, ok := Action(tt.arg)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Action(%q) = (%q, %v), want (%q, %v) %s",
				tt.arg, got, ok, tt.want, tt.wantOK, tt.comment)
		}
	}
}
