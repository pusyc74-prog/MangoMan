package sheets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var sheetLink = regexp.MustCompile(`^https://docs\.google\.com/spreadsheets/(?:u/\d+/)?d/([a-zA-Z0-9_-]{20,})`)

// GoogleID is the id in a Google Sheets link, or "".
func GoogleID(link string) string {
	m := sheetLink.FindStringSubmatch(strings.TrimSpace(link))
	if m == nil {
		return ""
	}
	return m[1]
}

// errNotShared is shown when Google answers with its sign-in page.
var errNotShared = errors.New("Google did not share this sheet with MangoMan. In Google Sheets click Share, set General access to " +
	"\"Anyone with the link\" (Viewer), and paste the link again")

// download gets the whole sheet, every tab, as one Excel file. Only
// docs.google.com is ever called.
func download(ctx context.Context, id string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://docs.google.com/spreadsheets/d/"+url.PathEscape(id)+"/export?format=xlsx", nil)
	req.Header.Set("User-Agent", "MangoMan Chargesheets")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", errors.New("could not reach Google Sheets: check the internet connection and try again")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", errors.New("Google says this sheet does not exist: check the link")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, "", errNotShared
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("Google Sheets answered HTTP %d; try again in a minute", resp.StatusCode)
	case strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html"):
		return nil, "", errNotShared
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxFile+1))
	if err != nil {
		return nil, "", errors.New("the download from Google Sheets broke off: try again")
	}
	if len(b) > MaxFile {
		return nil, "", errors.New("this Google Sheet is over 50 MB")
	}
	name := ""
	if _, p, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = strings.TrimSuffix(p["filename"], ".xlsx")
	}
	return b, name, nil
}

func (st *Store) fetch(ctx context.Context, id string) ([]byte, string, error) {
	if st.Fetch != nil {
		return st.Fetch(ctx, id)
	}
	return download(ctx, id)
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// AddGoogle links a Google Sheet shared as "anyone with the link".
func (st *Store) AddGoogle(ctx context.Context, id, link string) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	gid := GoogleID(link)
	if gid == "" {
		return Sheet{}, errors.New("paste the link of a Google Sheet (it starts with https://docs.google.com/spreadsheets/d/)")
	}
	sh, err := st.Get(id)
	if err != nil {
		return sh, err
	}
	for _, s := range sh.Sources {
		if GoogleID(s.URL) == gid {
			return sh, errors.New("this Google Sheet is already linked")
		}
	}
	b, name, err := st.fetch(ctx, gid)
	if err != nil {
		return Sheet{}, err
	}
	file := unique("google-"+gid[:10]+".xlsx", sh.Sources)
	if err := os.WriteFile(filepath.Join(d, "files", file), b, 0o600); err != nil {
		return Sheet{}, err
	}
	if name == "" {
		name = "Google Sheet"
	}
	return st.update(id, func(sh *Sheet) {
		sh.Sources = append(sh.Sources, Source{File: file, Label: name, URL: "https://docs.google.com/spreadsheets/d/" + gid,
			Hash: digest(b), Fetched: time.Now().UTC()})
		if len(sh.Sources) == 1 && sh.Title == "New Chargesheet" {
			sh.Title = name
		}
	})
}

// Refresh downloads linked Google Sheets again and reads everything again
// when one changed. It says what changed in Sheet.Changed.
func (st *Store) Refresh(ctx context.Context, id string) (Sheet, bool, error) {
	l := st.lock(id)
	l.Lock()
	defer l.Unlock()
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, false, err
	}
	sh, err := st.Get(id)
	if err != nil {
		return sh, false, err
	}
	type got struct {
		file, hash string
		b          []byte
	}
	var fresh []got
	for _, s := range sh.Sources {
		if s.URL == "" {
			continue
		}
		b, _, err := st.fetch(ctx, GoogleID(s.URL))
		if err != nil {
			return sh, false, err
		}
		if h := digest(b); h != s.Hash {
			fresh = append(fresh, got{s.File, h, b})
		}
	}
	now := time.Now().UTC()
	if len(fresh) == 0 {
		sh, err = st.update(id, func(sh *Sheet) {
			for i := range sh.Sources {
				if sh.Sources[i].URL != "" {
					sh.Sources[i].Fetched = now
				}
			}
		})
		return sh, false, err
	}
	before := sh.Rows
	for _, f := range fresh {
		if err := os.WriteFile(filepath.Join(d, "files", f.file), f.b, 0o600); err != nil {
			return sh, false, err
		}
	}
	if _, err := st.update(id, func(sh *Sheet) {
		for i := range sh.Sources {
			for _, f := range fresh {
				if sh.Sources[i].File == f.file {
					sh.Sources[i].Hash, sh.Sources[i].Fetched = f.hash, now
				}
			}
		}
	}); err != nil {
		return sh, false, err
	}
	sh, err = st.load(ctx, id)
	if err != nil {
		return sh, true, err
	}
	change := "The sheet changed."
	switch diff := sh.Rows - before; {
	case diff > 0:
		change = fmt.Sprintf("%d new row%s since the last update.", diff, plural(diff))
	case diff < 0:
		change = fmt.Sprintf("%d row%s fewer than at the last update.", -diff, plural(-diff))
	case before > 0:
		change = "Same number of rows; some values changed."
	}
	sh, err = st.update(id, func(sh *Sheet) { sh.Changed = change })
	return sh, true, err
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
