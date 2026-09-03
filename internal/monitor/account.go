package monitor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/jsm.go/serverdata"
)

// The account view is the free tier: no second connection, no system-account
// credentials, nothing to configure. The server auto-imports these three
// services into every account, scoped so an account only ever learns about
// itself - which is exactly what makes them safe to enable by default.
const (
	accStatzSubj = "$SYS.REQ.ACCOUNT.PING.STATZ"
	accConnzSubj = "$SYS.REQ.ACCOUNT.PING.CONNZ"
	userInfoSubj = "$SYS.REQ.USER.INFO"

	accTimeout = 3 * time.Second
)

// AccountView is what the data connection can tell you about your own account.
// Each part is reported separately, because a deployment may have turned any
// one of them off and a partial answer is more useful than an error.
type AccountView struct {
	User  json.RawMessage   `json:"user,omitempty"`
	Statz []json.RawMessage `json:"statz,omitempty"`
	Connz []json.RawMessage `json:"connz,omitempty"`

	UserError  string `json:"user_error,omitempty"`
	StatzError string `json:"statz_error,omitempty"`
	ConnzError string `json:"connz_error,omitempty"`
}

// Account queries the app's own connection. Unlike everything else here it
// needs no configuration at all.
func (m *Monitor) Account() (*AccountView, error) {
	nc, err := m.data.Conn()
	if err != nil {
		return nil, err
	}

	view := &AccountView{}
	log := api.NewDiscardLogger()

	// USER.INFO is answered by the one server we are attached to, so one
	// reply is the whole answer.
	if msg, err := nc.Request(userInfoSubj, nil, accTimeout); err != nil {
		view.UserError = err.Error()
	} else {
		view.User = json.RawMessage(msg.Data)
	}

	// STATZ and CONNZ are scatter-gather: in a cluster every server answers
	// about the part of the account it is holding.
	statz, err := serverdata.DoReq(context.Background(), nil, accStatzSubj, 0, nc, accTimeout, log)
	if err != nil {
		view.StatzError = err.Error()
	} else {
		view.Statz = raws(statz)
	}

	connz, err := serverdata.DoReq(context.Background(), nil, accConnzSubj, 0, nc, accTimeout, log)
	if err != nil {
		view.ConnzError = err.Error()
	} else {
		view.Connz = raws(connz)
	}

	return view, nil
}

func raws(in [][]byte) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(in))
	for _, b := range in {
		out = append(out, json.RawMessage(b))
	}
	return out
}
