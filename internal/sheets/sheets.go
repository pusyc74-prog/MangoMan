// Package sheets is Chargesheets: the user's spreadsheets (uploaded files or
// a Google Sheet link) turned into a dashboard they can question.
//
// Code does every count. engine.py (run with MangoMan's Python) reads and
// cleans the files and writes the columns the dashboard counts from in the
// browser, a profile of what it read, and a SQLite copy for questions. The
// free models only describe the data, write queries for questions, and
// write the words of the readout; every number they write is checked
// against what code computed (check.go).
package sheets

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed engine.py
var engine []byte

// MaxFile is the largest file a sheet takes.
const MaxFile = 50 << 20

// Readable are the file kinds the engine reads.
var Readable = map[string]bool{".xlsx": true, ".xlsm": true, ".csv": true, ".tsv": true, ".txt": true}

// Source is one file or Google Sheet behind a sheet.
type Source struct {
	File    string    `json:"file"`           // in the sheet's files folder
	Label   string    `json:"label"`          // shown to the user
	URL     string    `json:"url,omitempty"`  // a linked Google Sheet
	Hash    string    `json:"hash,omitempty"` // of the last download, to see changes
	Fetched time.Time `json:"fetched,omitempty"`
}

// Roles are what the columns of the main table are for (model's view, checked).
type Roles struct {
	Table    string   `json:"table,omitempty"`    // the main dataset's name
	Measures []string `json:"measures,omitempty"` // worth adding up
	Date     string   `json:"date,omitempty"`
	Dims     []string `json:"dims,omitempty"`   // worth splitting by
	Entity   string   `json:"entity,omitempty"` // customer, product, campaign
}

// Sheet is one Chargesheet.
type Sheet struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	About     string    `json:"about,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Built     time.Time `json:"built,omitempty"`
	Sources   []Source  `json:"sources"`
	Rows      int       `json:"rows,omitempty"`
	Questions []string  `json:"questions,omitempty"`
	Roles     Roles     `json:"roles"`
	Error     string    `json:"error,omitempty"`
	Changed   string    `json:"changed,omitempty"` // what the last refresh changed
}

// Live reports whether the sheet has a linked Google Sheet.
func (s Sheet) Live() bool {
	for _, src := range s.Sources {
		if src.URL != "" {
			return true
		}
	}
	return false
}

// Store keeps every sheet in its own folder under Dir.
type Store struct {
	Dir    string
	Python string                                                       // default python3
	Model  func(prompt string, asJSON bool) (string, error)             // a free model; nil = no words from models
	Fetch  func(ctx context.Context, id string) ([]byte, string, error) // a Google Sheet as .xlsx; nil = docs.google.com

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var idRe = regexp.MustCompile(`^[a-f0-9]{12}$`)

func (st *Store) dir(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", errors.New("no such sheet")
	}
	d := filepath.Join(st.Dir, id)
	if _, err := os.Stat(filepath.Join(d, "sheet.json")); err != nil {
		return "", errors.New("no such sheet")
	}
	return d, nil
}

// lock keeps one load or refresh per sheet at a time.
func (st *Store) lock(id string) *sync.Mutex {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.locks == nil {
		st.locks = map[string]*sync.Mutex{}
	}
	if st.locks[id] == nil {
		st.locks[id] = &sync.Mutex{}
	}
	return st.locks[id]
}

// New makes an empty sheet.
func (st *Store) New(title string) (Sheet, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return Sheet{}, err
	}
	now := time.Now().UTC()
	sh := Sheet{ID: hex.EncodeToString(b), Title: strings.TrimSpace(title), Created: now, Updated: now, Sources: []Source{}}
	if sh.Title == "" {
		sh.Title = "New Chargesheet"
	}
	if err := os.MkdirAll(filepath.Join(st.Dir, sh.ID, "files"), 0o700); err != nil {
		return Sheet{}, err
	}
	return sh, st.save(sh)
}

func (st *Store) save(sh Sheet) error {
	b, _ := json.MarshalIndent(sh, "", "  ")
	p := filepath.Join(st.Dir, sh.ID, "sheet.json")
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// Get reads one sheet.
func (st *Store) Get(id string) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	var sh Sheet
	b, err := os.ReadFile(filepath.Join(d, "sheet.json"))
	if err == nil {
		err = json.Unmarshal(b, &sh)
	}
	return sh, err
}

// List is every sheet, newest change first.
func (st *Store) List() []Sheet {
	ents, _ := os.ReadDir(st.Dir)
	out := []Sheet{}
	for _, e := range ents {
		if sh, err := st.Get(e.Name()); err == nil {
			out = append(out, sh)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Delete removes a sheet and its copies of the user's files.
func (st *Store) Delete(id string) error {
	d, err := st.dir(id)
	if err != nil {
		return err
	}
	l := st.lock(id)
	l.Lock()
	defer l.Unlock()
	return os.RemoveAll(d)
}

// Rename sets the title.
func (st *Store) Rename(id, title string) (Sheet, error) {
	return st.update(id, func(sh *Sheet) { sh.Title = strings.TrimSpace(title) })
}

func (st *Store) update(id string, f func(*Sheet)) (Sheet, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sh, err := st.Get(id)
	if err != nil {
		return sh, err
	}
	f(&sh)
	if sh.Title == "" {
		sh.Title = "New Chargesheet"
	}
	sh.Updated = time.Now().UTC()
	return sh, st.save(sh)
}

var notWord = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

// AddFile copies one of the user's files into the sheet.
func (st *Store) AddFile(id, name string, r io.Reader) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".xls" {
		return Sheet{}, errors.New("this is an old Excel file (.xls): open it in Excel or Google Sheets and save it as .xlsx")
	}
	if !Readable[ext] || strings.HasPrefix(name, ".") {
		return Sheet{}, errors.New("Chargesheets reads Excel (.xlsx) and CSV files")
	}
	safe := strings.Trim(notWord.ReplaceAllString(name, "-"), "-")
	b, err := io.ReadAll(io.LimitReader(r, MaxFile+1))
	if err != nil {
		return Sheet{}, err
	}
	if len(b) > MaxFile {
		return Sheet{}, errors.New("files can be at most 50 MB")
	}
	sh, err := st.Get(id)
	if err != nil {
		return sh, err
	}
	file := unique(safe, sh.Sources)
	if err := os.WriteFile(filepath.Join(d, "files", file), b, 0o600); err != nil {
		return Sheet{}, err
	}
	return st.update(id, func(sh *Sheet) {
		sh.Sources = append(sh.Sources, Source{File: file, Label: strings.TrimSuffix(name, filepath.Ext(name))})
		if len(sh.Sources) == 1 && sh.Title == "New Chargesheet" {
			sh.Title = niceTitle(strings.TrimSuffix(name, filepath.Ext(name)))
		}
	})
}

// niceTitle makes "kesar_sales-2026" read "Kesar sales 2026".
func niceTitle(s string) string {
	s = strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(s)), " ")
	if s == "" {
		return "New Chargesheet"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func unique(name string, have []Source) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	out := name
	for i := 2; ; i++ {
		taken := false
		for _, s := range have {
			taken = taken || strings.EqualFold(s.File, out)
		}
		if !taken {
			return out
		}
		out = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
}

// RemoveSource takes a file or link out of the sheet.
func (st *Store) RemoveSource(id, file string) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	sh, err := st.update(id, func(sh *Sheet) {
		keep := sh.Sources[:0]
		for _, s := range sh.Sources {
			if s.File != file {
				keep = append(keep, s)
			}
		}
		sh.Sources = keep
	})
	if err == nil && filepath.Base(file) == file {
		os.Remove(filepath.Join(d, "files", file))
	}
	return sh, err
}

// python runs the engine.
func (st *Store) python(ctx context.Context, d string, stdin []byte, args ...string) ([]byte, error) {
	script := filepath.Join(st.Dir, "engine.py")
	if old, err := os.ReadFile(script); err != nil || !bytes.Equal(old, engine) {
		if err := os.WriteFile(script, engine, 0o600); err != nil {
			return nil, err
		}
	}
	py := st.Python
	if py == "" {
		py = "python3"
	}
	cmd := exec.CommandContext(ctx, py, append([]string{"-I", script}, append(args, d)...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var msg struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(lastLine(out), &msg) == nil && msg.Error != "" {
		return out, errors.New(msg.Error)
	}
	if err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return nil, errors.New("Chargesheets needs MangoMan's Python: open the setup page and click Get ready")
		}
		if ctx.Err() != nil {
			return nil, errors.New("reading took too long; try a smaller file")
		}
		return nil, fmt.Errorf("could not read the data: %s", tail(stderr.String()))
	}
	return out, nil
}

func lastLine(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return b[i+1:]
	}
	return b
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return s
}

// Load reads the sheet's files again and asks a model to describe the
// result. A failed description keeps code's own reading of the roles.
func (st *Store) Load(ctx context.Context, id string) (Sheet, error) {
	l := st.lock(id)
	l.Lock()
	defer l.Unlock()
	return st.load(ctx, id)
}

// load is Load with the sheet's lock already held.
func (st *Store) load(ctx context.Context, id string) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	sh, err := st.Get(id)
	if err != nil {
		return sh, err
	}
	if len(sh.Sources) == 0 {
		return sh, errors.New("add a file or a Google Sheet link first")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := st.python(ctx, d, nil, "load"); err != nil {
		_, _ = st.update(id, func(sh *Sheet) { sh.Error = err.Error() })
		return sh, err
	}
	prof, err := ReadProfile(d)
	if err != nil {
		return sh, err
	}
	// The model's description is waited for a little; a slow one lands
	// later, while the dashboard already shows code's own reading.
	type desc struct {
		roles     Roles
		about     string
		questions []string
	}
	got := make(chan desc, 1)
	go func() {
		r, a, q := st.describe(prof)
		got <- desc{r, a, q}
	}()
	built := time.Now().UTC()
	apply := func(d desc) func(*Sheet) {
		return func(sh *Sheet) {
			sh.Roles, sh.Questions = d.roles, d.questions
			if d.about != "" {
				sh.About = d.about
			}
			sh.Rows = 0
			for _, ds := range prof.Datasets {
				if ds.Name == d.roles.Table {
					sh.Rows = ds.Rows
				}
			}
		}
	}
	var now desc
	select {
	case now = <-got:
	case <-time.After(describeWait):
		ds, _ := mainDataset(prof)
		now = desc{roles: guessRoles(ds)}
		go func() {
			late := <-got
			st.mu.Lock()
			current, err := st.Get(id)
			st.mu.Unlock()
			if err == nil && current.Built.Equal(built) {
				_, _ = st.update(id, apply(late))
			}
		}()
	}
	return st.update(id, func(sh *Sheet) {
		sh.Error, sh.Built = "", built
		apply(now)(sh)
	})
}

// describeWait is how long a load waits for the model's description.
var describeWait = 25 * time.Second

// Profile is what the engine read (profile.json).
type Profile struct {
	Built    string    `json:"built"`
	Datasets []Dataset `json:"datasets"`
	Links    []Link    `json:"links"`
}

// Dataset is one table after cleaning.
type Dataset struct {
	Name       string   `json:"name"`
	Table      string   `json:"table"`
	Rows       int      `json:"rows"`
	Origins    []string `json:"origins"`
	Notes      []string `json:"notes"`
	Currency   string   `json:"currency,omitempty"`
	JoinedInto string   `json:"joined_into,omitempty"`
	Columns    []Column `json:"columns"`
}

// Column is one column's reading.
type Column struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Unit       string   `json:"unit,omitempty"`
	Unreadable int      `json:"unreadable"`
	Merged     int      `json:"merged"`
	Blanks     int      `json:"blanks"`
	Distinct   int      `json:"distinct"`
	Min        any      `json:"min,omitempty"`
	Max        any      `json:"max,omitempty"`
	Sum        *float64 `json:"sum,omitempty"`
	Top        [][]any  `json:"top,omitempty"`
	Examples   []string `json:"examples,omitempty"`
	Mean       *float64 `json:"mean,omitempty"`
}

// Link is a lookup table joined into a bigger one.
type Link struct {
	ID      string  `json:"id"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	By      string  `json:"by"`
	Key     string  `json:"key"`
	Matched float64 `json:"matched"`
	Rows    int     `json:"rows"`
	On      bool    `json:"on"`
}

// ReadProfile reads profile.json of a sheet folder.
func ReadProfile(d string) (Profile, error) {
	var p Profile
	b, err := os.ReadFile(filepath.Join(d, "profile.json"))
	if err != nil {
		return p, errors.New("this sheet has not been read yet")
	}
	return p, json.Unmarshal(b, &p)
}

// Profile of one sheet.
func (st *Store) Profile(id string) (Profile, error) {
	d, err := st.dir(id)
	if err != nil {
		return Profile{}, err
	}
	return ReadProfile(d)
}

// DataPath is the columns file the dashboard reads.
func (st *Store) DataPath(id string) (string, error) {
	d, err := st.dir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "data.json"), nil
}

// Choices are the user's corrections on the check screen.
type Choices struct {
	Kinds   map[string]string `json:"kinds,omitempty"`  // "<dataset>/<column>": kind
	Header  map[string]int    `json:"header,omitempty"` // origin: row of the column names
	NoLink  []string          `json:"nolink,omitempty"` // link ids turned off
	NoStack bool              `json:"nostack,omitempty"`
}

// Kinds the user can choose for a column.
var Kinds = map[string]bool{"number": true, "money": true, "percent": true, "date": true, "category": true,
	"text": true, "id": true, "email": true, "url": true, "yes_no": true}

// SetChoices saves the corrections and reads the files again.
func (st *Store) SetChoices(ctx context.Context, id string, c Choices) (Sheet, error) {
	d, err := st.dir(id)
	if err != nil {
		return Sheet{}, err
	}
	for k, v := range c.Kinds {
		if !Kinds[v] || !strings.Contains(k, "/") {
			return Sheet{}, fmt.Errorf("unknown kind %q", v)
		}
	}
	b, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(d, "choices.json"), b, 0o600); err != nil {
		return Sheet{}, err
	}
	return st.Load(ctx, id)
}

// GetChoices reads the saved corrections.
func (st *Store) GetChoices(id string) Choices {
	var c Choices
	if d, err := st.dir(id); err == nil {
		if b, err := os.ReadFile(filepath.Join(d, "choices.json")); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	return c
}

// Result is one query's rows.
type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	More    bool     `json:"more,omitempty"`
}

// Query runs one read-only SELECT on the sheet's data.
func (st *Store) Query(ctx context.Context, id, sql string) (Result, error) {
	d, err := st.dir(id)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"sql": sql})
	out, err := st.python(ctx, d, in, "sql")
	if err != nil {
		return Result{}, err
	}
	var r struct {
		Result
		Error string `json:"error"`
	}
	if err := json.Unmarshal(lastLine(out), &r); err != nil {
		return Result{}, err
	}
	if r.Error != "" {
		if r.Error == "interrupted" {
			r.Error = "the query took longer than 10 seconds"
		}
		return Result{}, errors.New(r.Error)
	}
	return r.Result, nil
}
