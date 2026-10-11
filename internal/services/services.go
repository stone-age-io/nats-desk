// Package services finds the NATS micro services running in the app's own
// account.
//
// Discovery is the $SRV protocol every micro service answers: PING, INFO and
// STATS, each on $SRV.<VERB>, $SRV.<VERB>.<name> and $SRV.<VERB>.<name>.<id>.
// Those are ordinary subjects in the caller's account, not system subjects, so
// this needs no second connection and no system user. That is the difference
// from $SYS monitoring, and why this tab works for anyone who can connect.
//
// Nothing is stored here. A census is asked for, answered and handed back;
// the UI holds the result and the next census replaces it.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/jsm.go/serverdata"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

// censusTimeout is how long a census waits for its *first* reply. After that
// it stops as soon as replies stall, so a busy account answers in a fraction
// of this. A var so tests can shrink it.
var censusTimeout = 3 * time.Second

// directTimeout bounds a request addressed to one instance.
var directTimeout = 3 * time.Second

// maxInstances caps what one census returns, so a very large fleet degrades
// into a reported number rather than a table the browser cannot render. A var
// so tests can reach it.
var maxInstances = 5000

// tokenPattern is the name rule micro enforces on AddService. A service ID is
// a nuid, which also matches. Checked before either is put into a subject,
// because both arrive from a URL path and "x.>" is not a name.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9\-_]+$`)

// Instance is one running copy of a service.
type Instance struct {
	Name     string            `json:"name"`
	ID       string            `json:"id"`
	Version  string            `json:"version"`
	Metadata map[string]string `json:"metadata,omitempty"`

	// Info and Stats are the service's own replies, passed through as sent.
	// They are the protocol's documented shapes, and a service may add to them
	// (STATS carries a custom `data` per endpoint), so re-modelling them here
	// would only mean the UI showing less than the service said.
	Info json.RawMessage `json:"info"`

	// Stats is absent, never zeroed, when this instance did not answer the
	// STATS half of the census. "No requests yet" and "we did not hear" are
	// different facts.
	Stats json.RawMessage `json:"stats,omitempty"`
}

// Census is everything that answered.
type Census struct {
	At        time.Time  `json:"at"`
	Instances []Instance `json:"instances"`

	// Skipped counts replies that were not micro service responses. Something
	// else may answer on $SRV.INFO; it is counted rather than shown, and never
	// silently dropped.
	Skipped int `json:"skipped,omitempty"`

	// Truncated counts instances that answered beyond maxInstances.
	Truncated int `json:"truncated,omitempty"`

	// StatsError is set when INFO answered and STATS did not. A partial census
	// is more useful than an error.
	StatsError string `json:"stats_error,omitempty"`
}

// Discover runs a census: INFO and STATS to every service at once.
//
// INFO says what each instance is and what it serves; STATS says how it is
// doing. They are asked concurrently and joined by instance ID. PING is never
// sent - everything it carries is in INFO.
func Discover(nc *nats.Conn) (Census, error) {
	infoSubj, _ := micro.ControlSubject(micro.InfoVerb, "", "")
	statsSubj, _ := micro.ControlSubject(micro.StatsVerb, "", "")

	var (
		wg                sync.WaitGroup
		infos, stats      [][]byte
		infoErr, statsErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); infos, infoErr = gather(nc, infoSubj) }()
	go func() { defer wg.Done(); stats, statsErr = gather(nc, statsSubj) }()
	wg.Wait()

	switch {
	case errors.Is(infoErr, nats.ErrNoResponders):
		// The server said at once that nobody subscribes to $SRV.INFO. That
		// is a real, complete answer: no services are running here.
		infos = nil
	case infoErr != nil:
		return Census{}, fmt.Errorf("service discovery failed: %w", infoErr)
	case len(infos) == 0:
		// Not the same as no responders. With nobody subscribed the server
		// replies "no responders" immediately; silence until the timeout
		// means the request or its replies were refused on the way.
		return Census{}, fmt.Errorf("nothing answered %s within %s. If services are running, "+
			"this user may not be allowed to publish to $SRV.> or to receive replies", infoSubj, censusTimeout)
	}

	c := Census{At: time.Now(), Instances: []Instance{}}
	seen := map[string]bool{}
	byID := map[string]int{}

	for _, raw := range infos {
		id, ok := identity(raw, micro.InfoResponseType)
		if !ok {
			c.Skipped++
			continue
		}
		if seen[id.ID] {
			continue
		}
		seen[id.ID] = true
		if len(c.Instances) >= maxInstances {
			c.Truncated++
			continue
		}
		byID[id.ID] = len(c.Instances)
		c.Instances = append(c.Instances, Instance{
			Name:     id.Name,
			ID:       id.ID,
			Version:  id.Version,
			Metadata: id.Metadata,
			Info:     json.RawMessage(raw),
		})
	}

	for _, raw := range stats {
		id, ok := identity(raw, micro.StatsResponseType)
		if !ok {
			c.Skipped++
			continue
		}
		if i, ok := byID[id.ID]; ok {
			c.Instances[i].Stats = json.RawMessage(raw)
		}
	}

	if len(c.Instances) > 0 {
		switch {
		case statsErr != nil && !errors.Is(statsErr, nats.ErrNoResponders):
			c.StatsError = statsErr.Error()
		case len(stats) == 0:
			c.StatsError = fmt.Sprintf("nothing answered %s - this user may not be allowed to publish to it", statsSubj)
		}
	}

	sortInstances(c.Instances)
	return c, nil
}

// gather is one scatter-gather on the data connection: wait up to
// censusTimeout for the first reply, then stop once replies stall. How many
// services will answer is not knowable in advance, so there is no count to
// wait for.
//
// The same jsm.go helper the account view uses. It reports "no responders" as
// an error and a silent timeout as success with nothing, which is exactly the
// distinction Discover needs to tell an empty account from a refused request.
func gather(nc *nats.Conn, subject string) ([][]byte, error) {
	// DoReqAsync calls log.Debugf with no nil check, so a logger is required
	// even when nothing should be logged.
	return serverdata.DoReq(context.Background(), nil, subject, 0, nc, censusTimeout, api.NewDiscardLogger())
}

// identity reads the identity every $SRV reply starts with, and reports false
// for anything that is not the expected response type.
func identity(raw []byte, wantType string) (micro.ServiceIdentity, bool) {
	var r struct {
		micro.ServiceIdentity
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Type != wantType || r.ID == "" || r.Name == "" {
		return micro.ServiceIdentity{}, false
	}
	return r.ServiceIdentity, true
}

// sortInstances orders by service name, then metadata, then ID, so a row does
// not jump about between censuses. Metadata comes before ID because an ID is
// random per process, while metadata is usually what an operator thinks of an
// instance as - a host, a site, a device code.
func sortInstances(list []Instance) {
	key := func(m map[string]string) string {
		parts := make([]string, 0, len(m))
		for k, v := range m {
			parts = append(parts, k+"="+v)
		}
		sort.Strings(parts)
		return strings.Join(parts, "\x00")
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if ka, kb := key(a.Metadata), key(b.Metadata); ka != kb {
			return ka < kb
		}
		return a.ID < b.ID
	})
}

// ErrStopped means the instance asked for has stopped: the server reported
// nobody listening on its stats subject.
var ErrStopped = errors.New("that instance is no longer answering - refresh the list")

// Stats asks one instance for its stats now.
func Stats(nc *nats.Conn, name, id string) (json.RawMessage, error) {
	if !tokenPattern.MatchString(name) || !tokenPattern.MatchString(id) {
		return nil, errors.New("invalid service name or instance id")
	}

	subj, _ := micro.ControlSubject(micro.StatsVerb, name, id)
	msg, err := nc.Request(subj, nil, directTimeout)
	switch {
	case errors.Is(err, nats.ErrNoResponders):
		return nil, ErrStopped
	case errors.Is(err, nats.ErrTimeout):
		// Unlike no responders, the instance may be alive and slow.
		return nil, fmt.Errorf("%s did not answer within %s", subj, directTimeout)
	case err != nil:
		return nil, fmt.Errorf("stats request failed: %w", err)
	}
	if got, ok := identity(msg.Data, micro.StatsResponseType); !ok || got.ID != id {
		return nil, fmt.Errorf("%s answered with something that is not its stats", subj)
	}
	return json.RawMessage(msg.Data), nil
}
