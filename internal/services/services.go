// Package services finds the NATS micro services running in the app's own
// account, and keeps the last census of them.
//
// Discovery is the $SRV protocol every micro service answers: PING, INFO and
// STATS, each on $SRV.<VERB>, $SRV.<VERB>.<name> and $SRV.<VERB>.<name>.<id>.
// Those are ordinary subjects in the caller's account, not system subjects, so
// this needs no second connection and no system user. That is the difference
// from $SYS monitoring, and why this tab works for anyone who can connect.
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

// maxInstances caps what one census keeps, so a very large fleet degrades into
// a reported number rather than a table the browser cannot render. A var so
// tests can reach it.
var maxInstances = 5000

// tokenPattern is the name rule micro enforces on AddService. A service ID is
// a nuid, which also matches. Checked before either is put into a subject,
// because both arrive from a URL path and "x.>" is not a name.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9\-_]+$`)

// DataConn is the app's own NATS connection. Only the connection is needed, so
// only the connection is asked for.
type DataConn interface {
	Conn() (*nats.Conn, error)
}

// Instance is one running copy of a service, as the last census saw it.
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

	// Gone marks an instance the latest census did not hear from. It stays for
	// one census and then goes, which is the compromise between two failures:
	// a list that silently loses a line tells you nothing went away, and one
	// that keeps every line forever fills up with restarts, because micro mints
	// a new ID for every process.
	Gone bool `json:"gone,omitempty"`
}

// Snapshot is the last census.
type Snapshot struct {
	// At is nil until a census has run on the current connection.
	At        *time.Time `json:"at,omitempty"`
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

// Catalog holds the last census, and which connection it came from.
type Catalog struct {
	data DataConn

	mu   sync.Mutex
	conn *nats.Conn
	snap Snapshot
}

func New(data DataConn) *Catalog {
	return &Catalog{data: data, snap: Snapshot{Instances: []Instance{}}}
}

// Snapshot returns the last census without asking anyone anything.
//
// A census belongs to the connection it ran on. Connecting elsewhere - another
// server, another account, another user - makes it a list of services
// nats-desk can no longer see, so it is dropped the first time anyone looks.
// A reconnect keeps the same *nats.Conn, and the census with it.
func (c *Catalog) Snapshot() Snapshot {
	nc, err := c.data.Conn()

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || nc != c.conn {
		c.conn = nil
		c.snap = Snapshot{Instances: []Instance{}}
	}
	return c.copyLocked()
}

// copyLocked returns the snapshot with its own instance slice, because
// InstanceStats updates entries in place while a caller may be encoding them.
func (c *Catalog) copyLocked() Snapshot {
	out := c.snap
	out.Instances = append(make([]Instance, 0, len(c.snap.Instances)), c.snap.Instances...)
	return out
}

// Refresh runs a census: INFO and STATS to every service at once.
//
// INFO says what each instance is and what it serves; STATS says how it is
// doing. They are asked concurrently and joined by instance ID. PING is never
// sent - everything it carries is in INFO.
func (c *Catalog) Refresh() (Snapshot, error) {
	nc, err := c.data.Conn()
	if err != nil {
		return Snapshot{}, err
	}

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
		return Snapshot{}, fmt.Errorf("service discovery failed: %w", infoErr)
	case len(infos) == 0:
		// Not the same as no responders. With nobody subscribed the server
		// replies "no responders" immediately; silence until the timeout
		// means the request or its replies were refused on the way.
		return Snapshot{}, fmt.Errorf("nothing answered %s within %s. If services are running, "+
			"this user may not be allowed to publish to $SRV.> or to receive replies", infoSubj, censusTimeout)
	}

	snap := Snapshot{Instances: []Instance{}}
	answered := map[string]bool{}
	byID := map[string]*Instance{}
	fresh := make([]Instance, 0, len(infos))

	for _, raw := range infos {
		id, ok := identity(raw, micro.InfoResponseType)
		if !ok {
			snap.Skipped++
			continue
		}
		if answered[id.ID] {
			continue
		}
		answered[id.ID] = true
		if len(fresh) >= maxInstances {
			snap.Truncated++
			continue
		}
		fresh = append(fresh, Instance{
			Name:     id.Name,
			ID:       id.ID,
			Version:  id.Version,
			Metadata: id.Metadata,
			Info:     json.RawMessage(raw),
		})
	}
	for i := range fresh {
		byID[fresh[i].ID] = &fresh[i]
	}

	for _, raw := range stats {
		id, ok := identity(raw, micro.StatsResponseType)
		if !ok {
			snap.Skipped++
			continue
		}
		if in := byID[id.ID]; in != nil {
			in.Stats = json.RawMessage(raw)
		}
	}

	if len(fresh) > 0 {
		switch {
		case statsErr != nil && !errors.Is(statsErr, nats.ErrNoResponders):
			snap.StatsError = statsErr.Error()
		case len(stats) == 0:
			snap.StatsError = fmt.Sprintf("nothing answered %s - this user may not be allowed to publish to it", statsSubj)
		}
	}

	now := time.Now()
	snap.At = &now

	c.mu.Lock()
	defer c.mu.Unlock()
	var prev []Instance
	if c.conn == nc {
		prev = c.snap.Instances
	}
	snap.Instances = merge(prev, fresh, answered)
	sortInstances(snap.Instances)
	c.conn = nc
	c.snap = snap
	return c.copyLocked(), nil
}

// gather is one scatter-gather on the data connection: wait up to
// censusTimeout for the first reply, then stop once replies stall. How many
// services will answer is not knowable in advance, so there is no count to
// wait for.
//
// The same jsm.go helper the account view uses. It reports "no responders" as
// an error and a silent timeout as success with nothing, which is exactly the
// distinction Refresh needs to tell an empty account from a refused request.
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

// merge folds a census into the previous one. Anything the census did not hear
// from is kept once, marked gone, and dropped if it is still missing next time.
//
// answered is every ID that replied, including any past the cap. An instance
// that answered but was not kept is not gone - Truncated already accounts for
// it - so it must not be dressed up as one.
func merge(prev, fresh []Instance, answered map[string]bool) []Instance {
	out := fresh
	for _, old := range prev {
		if answered[old.ID] || old.Gone {
			continue
		}
		old.Gone = true
		out = append(out, old)
	}
	return out
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

// ErrUnknownInstance means the instance is not in the current census.
var ErrUnknownInstance = errors.New("that instance is not in the current census - refresh the list")

// InstanceStats asks one instance for its stats now, and folds the answer into
// the census.
//
// "No responders" for a request addressed to one ID means that process has
// stopped, so the instance is marked gone and returned without an error: gone
// is a state the UI shows, not a failure. A timeout is different - the
// instance may be alive and slow - and is reported as one.
func (c *Catalog) InstanceStats(name, id string) (Instance, error) {
	if !tokenPattern.MatchString(name) || !tokenPattern.MatchString(id) {
		return Instance{}, errors.New("invalid service name or instance id")
	}
	nc, err := c.data.Conn()
	if err != nil {
		return Instance{}, err
	}
	if _, ok := c.find(nc, id); !ok {
		return Instance{}, ErrUnknownInstance
	}

	subj, _ := micro.ControlSubject(micro.StatsVerb, name, id)
	msg, err := nc.Request(subj, nil, directTimeout)

	c.mu.Lock()
	defer c.mu.Unlock()

	in := c.indexLocked(nc, id)
	if in < 0 {
		// The census was replaced while we waited.
		return Instance{}, ErrUnknownInstance
	}
	switch {
	case errors.Is(err, nats.ErrNoResponders):
		c.snap.Instances[in].Gone = true
		return c.snap.Instances[in], nil
	case errors.Is(err, nats.ErrTimeout):
		return Instance{}, fmt.Errorf("%s did not answer within %s", subj, directTimeout)
	case err != nil:
		return Instance{}, fmt.Errorf("stats request failed: %w", err)
	}
	if got, ok := identity(msg.Data, micro.StatsResponseType); !ok || got.ID != id {
		return Instance{}, fmt.Errorf("%s answered with something that is not its stats", subj)
	}

	c.snap.Instances[in].Stats = json.RawMessage(msg.Data)
	c.snap.Instances[in].Gone = false
	return c.snap.Instances[in], nil
}

func (c *Catalog) find(nc *nats.Conn, id string) (Instance, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := c.indexLocked(nc, id); i >= 0 {
		return c.snap.Instances[i], true
	}
	return Instance{}, false
}

func (c *Catalog) indexLocked(nc *nats.Conn, id string) int {
	if c.conn != nc {
		return -1
	}
	for i := range c.snap.Instances {
		if c.snap.Instances[i].ID == id {
			return i
		}
	}
	return -1
}
