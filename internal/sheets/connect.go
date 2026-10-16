package sheets

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Connections: a Shopify store or a Meta ad account read through its API.
// Each writes CSV files into the sheet's files folder, which the engine
// reads like any upload. Their secrets live in MangoMan's key store, never
// in the sheet's folder or in an answer to the page.

// connectorWait is how long an automatic refresh leaves a connection alone.
const connectorWait = 30 * time.Minute

// secretName is a connection's entry in the key store.
func secretName(sheet, kind, account string) string {
	return "chargesheets:" + sheet + ":" + kind + ":" + account
}

// pulled is one file a connection writes.
type pulled struct {
	file, label string
	b           []byte
}

// Connect checks the details by reading the data once, keeps the secret and
// adds the connection's tables to the sheet.
func (st *Store) Connect(ctx context.Context, id, kind, account, secret string) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	if st.Keys == nil {
		return Sheet{}, errors.New("connections need MangoMan's key store: restart MangoMan")
	}
	switch kind {
	case "shopify":
		account = ShopDomain(account)
		if account == "" {
			return Sheet{}, errors.New("type the store address, like my-store.myshopify.com")
		}
	case "meta":
		account = MetaAccount(account)
		if account == "" {
			return Sheet{}, errors.New("type the ad account ID: the number in Ads Manager, with or without act_")
		}
	default:
		return Sheet{}, fmt.Errorf("unknown connection %q", kind)
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return Sheet{}, errors.New("paste the key first")
	}
	l := st.lock(id)
	l.Lock()
	defer l.Unlock()
	sh, err := st.Get(id)
	if err != nil {
		return sh, err
	}
	for _, s := range sh.Sources {
		if s.Kind == kind && s.Account == account {
			return sh, errors.New("this is already connected")
		}
	}
	tables, err := st.pullConnection(ctx, kind, account, secret)
	if err != nil {
		return Sheet{}, err
	}
	name := secretName(id, kind, account)
	if err := st.Keys.Set(name, secret); err != nil {
		return Sheet{}, fmt.Errorf("could not keep the key safely: %v", err)
	}
	now := time.Now().UTC()
	for _, t := range tables {
		if err := os.WriteFile(filepath.Join(d, "files", t.file), t.b, 0o600); err != nil {
			_ = st.Keys.Delete(name)
			return Sheet{}, err
		}
	}
	return st.update(id, func(sh *Sheet) {
		for _, t := range tables {
			sh.Sources = append(sh.Sources, Source{File: t.file, Label: t.label, Kind: kind, Account: account, Hash: digest(t.b), Fetched: now})
		}
		if sh.Title == "New Chargesheet" {
			sh.Title = tables[0].label
		}
	})
}

// pullConnection reads a connection's tables with the given secret.
func (st *Store) pullConnection(ctx context.Context, kind, account, secret string) ([]pulled, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if kind == "shopify" {
		id, sec, _ := strings.Cut(secret, "\n")
		return st.shopify(ctx, account, strings.TrimSpace(id), strings.TrimSpace(sec))
	}
	return metaAds(ctx, account, secret)
}

// pull reads one source again: a Google Sheet, or every table of its connection.
func (st *Store) pull(ctx context.Context, sheet string, s Source) ([]pulled, error) {
	if s.Kind == "" {
		b, _, err := st.fetch(ctx, GoogleID(s.URL))
		return []pulled{{file: s.File, b: b}}, err
	}
	if st.Keys == nil {
		return nil, errors.New("connections need MangoMan's key store: restart MangoMan")
	}
	secret, err := st.Keys.Get(secretName(sheet, s.Kind, s.Account))
	if err != nil {
		return nil, fmt.Errorf("the key for %s is gone: remove it and connect again", s.Account)
	}
	return st.pullConnection(ctx, s.Kind, s.Account, secret)
}

// forgetSecrets drops the keys of connections no source uses any more.
func (st *Store) forgetSecrets(sheet string, before, after []Source) {
	if st.Keys == nil {
		return
	}
	for _, s := range before {
		if s.Kind == "" {
			continue
		}
		used := false
		for _, a := range after {
			used = used || (a.Kind == s.Kind && a.Account == s.Account)
		}
		if !used {
			_ = st.Keys.Delete(secretName(sheet, s.Kind, s.Account))
		}
	}
}

// csvFile writes rows as CSV.
func csvFile(rows [][]string) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.WriteAll(rows)
	return []byte(b.String())
}

// send makes a request with a plain error when the service cannot be reached.
func send(req *http.Request, service string) (*http.Response, error) {
	req.Header.Set("User-Agent", "MangoMan Chargesheets")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, fmt.Errorf("%s took too long to answer; try again", service)
		}
		return nil, fmt.Errorf("could not reach %s: check the internet connection and try again", service)
	}
	return resp, nil
}
