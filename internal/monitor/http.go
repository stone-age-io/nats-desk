package monitor

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// The :8222 endpoints have no authentication mechanism whatsoever, and
// configuring https_port forces the server to ClientAuth = NoClientCert, so
// client certificates cannot gate them either. Treat a reachable monitoring
// port as readable by anyone who can route to it - and never expose what it
// returns onward.
const httpTimeout = 5 * time.Second

// HTTPOptions configures the :8222 source. Its TLS settings are its own: the
// monitoring port frequently has a different certificate from the client port,
// and often a private CA.
type HTTPOptions struct {
	CA       string `json:"ca"`       // PEM, pasted rather than a path
	Insecure bool   `json:"insecure"` // skip verification, opt-in and explicit
}

type httpSource struct {
	bases  []string
	client *http.Client
	opts   HTTPOptions
}

// HTTPResult is one server's answer. A failure is a value, not an error,
// because "three of four nodes answered" is the interesting case and losing
// the three to report the one would be the wrong trade.
type HTTPResult struct {
	Base   string          `json:"base"`
	Status int             `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	Error  string          `json:"error,omitempty"`
}

var ErrNoHTTPSource = errors.New("no monitoring URLs configured")

// SetHTTP replaces the monitoring endpoints. Each base is the root of a
// server's monitoring port - "http://localhost:8222", not ".../varz".
func (m *Monitor) SetHTTP(bases []string, opts HTTPOptions) error {
	clean := make([]string, 0, len(bases))
	for _, b := range bases {
		b = strings.TrimSpace(strings.TrimRight(b, "/"))
		if b == "" {
			continue
		}
		u, err := url.Parse(b)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("%q is not an http:// or https:// URL", b)
		}
		clean = append(clean, b)
	}
	if len(clean) == 0 {
		return errors.New("at least one monitoring URL is required")
	}

	client, err := httpClient(opts)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.http = &httpSource{bases: clean, client: client, opts: opts}
	m.mu.Unlock()

	m.sink.MonitorStatus(m.Status())
	return nil
}

func (m *Monitor) ClearHTTP() {
	m.mu.Lock()
	m.http = nil
	m.mu.Unlock()
	m.sink.MonitorStatus(m.Status())
}

func httpClient(opts HTTPOptions) (*http.Client, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: opts.Insecure}

	if ca := strings.TrimSpace(opts.CA); ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("the CA certificate is not valid PEM")
		}
		tlsCfg.RootCAs = pool
	}

	return &http.Client{
		Timeout:   httpTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

// FetchHTTP asks every configured server for one endpoint, in parallel.
func (m *Monitor) FetchHTTP(endpoint string, query url.Values) ([]HTTPResult, error) {
	m.mu.RLock()
	src := m.http
	m.mu.RUnlock()
	if src == nil {
		return nil, ErrNoHTTPSource
	}

	out := make([]HTTPResult, len(src.bases))
	var wg sync.WaitGroup
	for i, base := range src.bases {
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			out[i] = src.get(base, endpoint, query)
		}(i, base)
	}
	wg.Wait()

	// A /varz answer is also a grid row, so the HTTP source populates the same
	// table the $SYS source does.
	if endpoint == "varz" {
		seen := map[string]bool{}
		for _, r := range out {
			if r.Status != http.StatusOK || len(r.Body) == 0 {
				continue
			}
			var v server.Varz
			if err := json.Unmarshal(r.Body, &v); err != nil {
				continue
			}
			m.grid.applyVarz(&v, "http")
			seen[v.ID] = true
		}
		if len(seen) > 0 {
			m.grid.prune(seen, "http")
			m.sink.MonitorServers(m.grid.rowsSorted())
		}
	}

	return out, nil
}

func (src *httpSource) get(base, endpoint string, query url.Values) HTTPResult {
	res := HTTPResult{Base: base}

	u := base + "/" + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	resp, err := src.client.Get(u)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()

	res.Status = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		res.Error = err.Error()
		return res
	}

	// A non-200 from /healthz is the answer, not a transport failure: an
	// unhealthy server reports 503 with a JSON body saying why. Passing the
	// body through either way is what lets the UI show the reason.
	if json.Valid(body) {
		res.Body = json.RawMessage(body)
	} else {
		res.Error = strings.TrimSpace(string(body))
		if res.Error == "" {
			res.Error = fmt.Sprintf("HTTP %d with an empty body", resp.StatusCode)
		}
	}
	return res
}
