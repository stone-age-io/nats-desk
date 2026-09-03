package natsconn

import "strings"

// The same two rules the old browser client used, moved server-side so the
// filtering happens before a message is encoded and pushed to the UI rather
// than after.

// IsSystemSubject reports whether a subject belongs to the server or to
// client plumbing rather than to an application.
//
// NATS reserves a leading '$' for server-side subjects ($SYS, $JS, $KV, $OBJ,
// $SRV, $MQTT, ...) and a leading '_' for client plumbing (_INBOX replies).
// Nothing an application publishes should start with either, so the first
// byte is enough - and it stays correct as new $-prefixed subsystems appear,
// which a hard-coded prefix list would not.
func IsSystemSubject(subject string) bool {
	if subject == "" {
		return false
	}
	return subject[0] == '$' || subject[0] == '_'
}

// CanExcludeSystem reports whether excluding system subjects would change
// anything for this pattern.
//
// Core NATS has no "everything except" wildcard, so the exclusion has to be a
// client-side drop. It only means something when the pattern's first token is
// a wildcard: '>' and '*.foo' sweep up $/_ traffic the user never asked for,
// while '$JS.EVENT.>' asks for it deliberately and would otherwise be
// filtered down to nothing. No other pattern can match a system subject.
func CanExcludeSystem(subject string) bool {
	first, _, _ := strings.Cut(subject, ".")
	return first == ">" || first == "*"
}
