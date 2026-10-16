package sheets

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the shop's time zone, on computers without a zone database
)

// Shopify: an app the store owner makes in Shopify's Dev Dashboard gives a
// Client ID and secret. They buy a token that lasts 24 hours (client
// credentials grant); orders come as one bulk export.

// shopifyBase is the store's address; tests point it elsewhere.
var shopifyBase = func(shop string) string { return "https://" + shop }

const shopifyVersion = "2026-10"

var shopRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.myshopify\.com$`)

// ShopDomain reads "my-store", "my-store.myshopify.com" or an admin link
// as the store's myshopify.com address, or "".
func ShopDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if rest, ok := strings.CutPrefix(s, "admin.shopify.com/store/"); ok {
		s = rest
	}
	s, _, _ = strings.Cut(s, "/")
	if s != "" && !strings.Contains(s, ".") {
		s += ".myshopify.com"
	}
	if !shopRe.MatchString(s) {
		return ""
	}
	return s
}

type shopToken struct {
	token string
	until time.Time
}

// shopifyToken buys a token, or reuses one bought earlier.
func (st *Store) shopifyToken(ctx context.Context, shop, id, secret string) (string, error) {
	key := shop + " " + id + " " + digest([]byte(secret))
	st.mu.Lock()
	t, ok := st.tokens[key]
	st.mu.Unlock()
	if ok && time.Now().Before(t.until) {
		return t.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secret}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, shopifyBase(shop)+"/admin/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := send(req, "Shopify")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if resp.StatusCode == http.StatusOK && json.Unmarshal(body, &r) == nil && r.Token != "" {
		if r.Expires <= 0 {
			r.Expires = 86399
		}
		st.mu.Lock()
		if st.tokens == nil {
			st.tokens = map[string]shopToken{}
		}
		st.tokens[key] = shopToken{r.Token, time.Now().Add(time.Duration(r.Expires)*time.Second - 10*time.Minute)}
		st.mu.Unlock()
		return r.Token, nil
	}
	low := strings.ToLower(string(body))
	switch {
	case strings.Contains(low, "shop_not_permitted") || strings.Contains(low, "cannot be performed on this shop"):
		return "", errors.New("Shopify refused: the app must be made in the Dev Dashboard of the organization that owns this store, and installed on it")
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("there is no Shopify store at %s: check the address", shop)
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		return "", errors.New("Shopify did not accept the Client ID and secret: copy them again from the app's Settings in the Dev Dashboard, and check the app is installed on this store")
	}
	return "", fmt.Errorf("Shopify answered HTTP %d; try again in a minute", resp.StatusCode)
}

var errShopifyToken = errors.New("token expired")

// shopifyQuery runs one Admin API call and decodes its data into out.
func shopifyQuery(ctx context.Context, shop, token, query string, vars map[string]any, out any) error {
	b, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, shopifyBase(shop)+"/admin/api/"+shopifyVersion+"/graphql.json", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", token)
	resp, err := send(req, "Shopify")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errShopifyToken
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Shopify answered HTTP %d; try again in a minute", resp.StatusCode)
	}
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return errors.New("Shopify's answer could not be read; try again")
	}
	if len(r.Errors) > 0 {
		if r.Errors[0].Extensions.Code == "ACCESS_DENIED" || strings.Contains(r.Errors[0].Message, "Access denied") {
			return errShopifyAccess
		}
		return fmt.Errorf("Shopify said: %s", r.Errors[0].Message)
	}
	return json.Unmarshal(r.Data, out)
}

var errShopifyAccess = errors.New("Shopify did not let the app read orders. In the Dev Dashboard, give the app's version the read_orders scope, " +
	"release it and approve it on the store; if Shopify asks, request access to protected customer data (level 1)")

// The orders and their lines, newest data Shopify lets the app read (the
// last 60 days unless the app may read all orders).
const shopifyOrders = `{ orders { edges { node { id name createdAt test cancelledAt displayFinancialStatus displayFulfillmentStatus sourceName ` +
	`customer { id } totalPriceSet { shopMoney { amount currencyCode } } totalDiscountsSet { shopMoney { amount } } ` +
	`totalShippingPriceSet { shopMoney { amount } } totalTaxSet { shopMoney { amount } } totalRefundedSet { shopMoney { amount } } ` +
	`lineItems { edges { node { id title sku vendor quantity discountedTotalSet { shopMoney { amount } } } } } } } } }`

type money struct {
	ShopMoney struct {
		Amount       string `json:"amount"`
		CurrencyCode string `json:"currencyCode"`
	} `json:"shopMoney"`
}

// bulkLine is one line of the export: an order, or a line of one.
type bulkLine struct {
	ID          string    `json:"id"`
	Parent      string    `json:"__parentId"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	Test        bool      `json:"test"`
	CancelledAt *string   `json:"cancelledAt"`
	Financial   string    `json:"displayFinancialStatus"`
	Fulfilment  string    `json:"displayFulfillmentStatus"`
	Source      string    `json:"sourceName"`
	Customer    *struct {
		ID string `json:"id"`
	} `json:"customer"`
	Total     *money `json:"totalPriceSet"`
	Discounts *money `json:"totalDiscountsSet"`
	Shipping  *money `json:"totalShippingPriceSet"`
	Tax       *money `json:"totalTaxSet"`
	Refunded  *money `json:"totalRefundedSet"`
	Title     string `json:"title"`
	SKU       string `json:"sku"`
	Vendor    string `json:"vendor"`
	Quantity  int    `json:"quantity"`
	Sales     *money `json:"discountedTotalSet"`
}

// shopify reads the store's orders as two tables: orders, and products sold.
func (st *Store) shopify(ctx context.Context, shop, id, secret string) ([]pulled, error) {
	if id == "" || secret == "" {
		return nil, errors.New("paste both the Client ID and the Client secret")
	}
	var token string
	// call runs a query, buying a new token once when the old one ran out.
	call := func(q string, vars map[string]any, out any) error {
		for try := 0; ; try++ {
			var err error
			if token == "" {
				if token, err = st.shopifyToken(ctx, shop, id, secret); err != nil {
					return err
				}
			}
			err = shopifyQuery(ctx, shop, token, q, vars, out)
			if err != errShopifyToken || try > 0 {
				if err == errShopifyToken {
					err = errors.New("Shopify did not accept the app's token: check the app is still installed on the store")
				}
				return err
			}
			st.mu.Lock()
			delete(st.tokens, shop+" "+id+" "+digest([]byte(secret)))
			st.mu.Unlock()
			token = ""
		}
	}
	var info struct {
		Shop struct {
			Zone string `json:"ianaTimezone"`
		} `json:"shop"`
	}
	if err := call(`{ shop { ianaTimezone } }`, nil, &info); err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation(info.Shop.Zone)
	if err != nil {
		zone = time.UTC
	}
	var run struct {
		Run struct {
			Op struct {
				ID string `json:"id"`
			} `json:"bulkOperation"`
			UserErrors []struct {
				Message string `json:"message"`
			} `json:"userErrors"`
		} `json:"bulkOperationRunQuery"`
	}
	if err := call(`mutation run($q: String!) { bulkOperationRunQuery(query: $q) { bulkOperation { id status } userErrors { field message } } }`,
		map[string]any{"q": shopifyOrders}, &run); err != nil {
		return nil, err
	}
	if len(run.Run.UserErrors) > 0 {
		return nil, fmt.Errorf("Shopify said: %s", run.Run.UserErrors[0].Message)
	}
	if run.Run.Op.ID == "" {
		return nil, errors.New("Shopify did not start the export; try again in a minute")
	}
	var done struct {
		Op struct {
			Status    string  `json:"status"`
			ErrorCode string  `json:"errorCode"`
			URL       *string `json:"url"`
		} `json:"bulkOperation"`
	}
	for wait := time.Second; ; wait = min(wait*2, 10*time.Second) {
		select {
		case <-ctx.Done():
			return nil, errors.New("Shopify's export took too long; try again later")
		case <-time.After(wait):
		}
		if err := call(`query op($id: ID!) { bulkOperation(id: $id) { status errorCode url } }`, map[string]any{"id": run.Run.Op.ID}, &done); err != nil {
			return nil, err
		}
		switch done.Op.Status {
		case "COMPLETED":
			return shopifyTables(ctx, shop, done.Op.URL, zone)
		case "CREATED", "RUNNING":
		default:
			if done.Op.ErrorCode == "ACCESS_DENIED" {
				return nil, errShopifyAccess
			}
			return nil, fmt.Errorf("Shopify's export stopped (%s); try again in a minute", strings.ToLower(done.Op.Status))
		}
	}
}

// shopifyTables reads the finished export (JSON lines) into the two tables.
func shopifyTables(ctx context.Context, shop string, link *string, zone *time.Location) ([]pulled, error) {
	var lines []bulkLine
	if link != nil && *link != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *link, nil)
		if err != nil {
			return nil, errors.New("Shopify's export link could not be read")
		}
		resp, err := send(req, "Shopify")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Shopify's export answered HTTP %d; try again", resp.StatusCode)
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			var l bulkLine
			if json.Unmarshal(sc.Bytes(), &l) == nil {
				lines = append(lines, l)
			}
		}
		if sc.Err() != nil {
			return nil, errors.New("the download from Shopify broke off: try again")
		}
	}
	cur := ""
	orders := map[string]*bulkLine{}
	for i := range lines {
		if l := &lines[i]; l.Parent == "" && !l.Test {
			orders[l.ID] = l
			if cur == "" && l.Total != nil {
				cur = l.Total.ShopMoney.CurrencyCode
			}
		}
	}
	unit := ""
	if cur != "" {
		unit = " (" + cur + ")"
	}
	day := func(t time.Time) string { return t.In(zone).Format("2006-01-02") }
	amount := func(m *money) string {
		if m == nil {
			return ""
		}
		return m.ShopMoney.Amount
	}
	head := []string{"Order", "Date", "Payment status", "Fulfilment", "Channel", "Customer ID", "Cancelled",
		"Sales after refunds" + unit, "Order total" + unit, "Discounts" + unit, "Shipping" + unit, "Tax" + unit, "Refunded" + unit}
	// Sales after refunds: the order's total less what was paid back, so a
	// cancelled and refunded order counts nothing.
	net := func(o *bulkLine) string {
		a, errA := strconv.ParseFloat(amount(o.Total), 64)
		b, errB := strconv.ParseFloat(amount(o.Refunded), 64)
		if errA != nil {
			return ""
		}
		if errB != nil {
			b = 0
		}
		return strconv.FormatFloat(a-b, 'f', 2, 64)
	}
	var rows, items [][]string
	for _, o := range orders {
		cust := ""
		if o.Customer != nil {
			cust = o.Customer.ID[strings.LastIndexByte(o.Customer.ID, '/')+1:]
		}
		rows = append(rows, []string{o.Name, day(o.CreatedAt), words(o.Financial), words(o.Fulfilment), channel(o.Source), cust,
			map[bool]string{true: "yes", false: "no"}[o.CancelledAt != nil], net(o), amount(o.Total), amount(o.Discounts), amount(o.Shipping), amount(o.Tax), amount(o.Refunded)})
	}
	for _, l := range lines {
		if o := orders[l.Parent]; o != nil && o.CancelledAt == nil { // cancelled orders sold nothing
			items = append(items, []string{o.Name, day(o.CreatedAt), l.Title, l.SKU, l.Vendor, fmt.Sprint(l.Quantity), amount(l.Sales)})
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("this store has no orders the app can read yet (Shopify shows the last 60 days unless the app may read all orders)")
	}
	base := "shopify-" + strings.TrimSuffix(shop, ".myshopify.com")
	return []pulled{
		{base + "-orders.csv", "Shopify orders", csvFile(newest(head, rows))},
		{base + "-products.csv", "Shopify products sold", csvFile(newest([]string{"Order number", "Date", "Product", "SKU", "Vendor", "Quantity", "Sales" + unit}, items))},
	}, nil
}

// newest keeps the header and the most recent rows the engine takes (column 2 is the date).
func newest(head []string, rows [][]string) [][]string {
	ordered(rows, 1, true)
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	return append([][]string{head}, rows...)
}

// ordered sorts rows by one column, then by all of them: the same data
// always makes the same file, so a refresh sees no change where there is none.
func ordered(rows [][]string, col int, desc bool) {
	sort.Slice(rows, func(i, j int) bool {
		if a, b := rows[i][col], rows[j][col]; a != b {
			return (a > b) == desc
		}
		return strings.Join(rows[i], "\x00") < strings.Join(rows[j], "\x00")
	})
}

// maxRows is the engine's limit per table (MAX_ROWS in engine.py).
const maxRows = 300_000

// channel names Shopify's two own sales channels; others (apps) as written.
func channel(s string) string {
	if n, ok := map[string]string{"web": "Online store", "pos": "Point of sale"}[s]; ok {
		return n
	}
	return words(s)
}

// words turns PARTIALLY_REFUNDED into "Partially refunded".
func words(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "_", " "))
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
