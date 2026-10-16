package sheets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/keys"
)

// memKeys is a key store in memory.
type memKeys struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKeys) Name() string { return "memory" }
func (k *memKeys) Get(p string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := k.m[p]; ok {
		return v, nil
	}
	return "", keys.ErrNotFound
}
func (k *memKeys) Set(p, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[p] = v
	return nil
}
func (k *memKeys) Delete(p string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, p)
	return nil
}

func TestAccountNames(t *testing.T) {
	for in, want := range map[string]string{
		"Kesar-Store": "kesar-store.myshopify.com",
		"https://kesar.myshopify.com/admin/orders": "kesar.myshopify.com",
		"admin.shopify.com/store/kesar/orders":     "kesar.myshopify.com",
		"kesar.example.com":                        "",
		"":                                         "",
	} {
		if got := ShopDomain(in); got != want {
			t.Errorf("ShopDomain(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"act_1234567": "1234567", "1234567": "1234567", "act_12": "", "abc": ""} {
		if got := MetaAccount(in); got != want {
			t.Errorf("MetaAccount(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeShopify answers like Shopify: the token, the shop, a bulk export of
// the given JSON lines.
func fakeShopify(t *testing.T, lines *string) (*httptest.Server, *int) {
	tokens := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "sec" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_client"}`)
				return
			}
			tokens++
			fmt.Fprintf(w, `{"access_token":"tok%d","scope":"read_orders","expires_in":86399}`, tokens)
		case "/admin/api/" + shopifyVersion + "/graphql.json":
			if r.Header.Get("X-Shopify-Access-Token") != fmt.Sprintf("tok%d", tokens) {
				w.WriteHeader(401)
				return
			}
			var in struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			switch {
			case strings.Contains(in.Query, "ianaTimezone"):
				fmt.Fprint(w, `{"data":{"shop":{"ianaTimezone":"Asia/Kolkata"}}}`)
			case strings.Contains(in.Query, "bulkOperationRunQuery"):
				if q, _ := in.Variables["q"].(string); !strings.Contains(q, "lineItems") {
					t.Errorf("bulk query: %q", q)
				}
				fmt.Fprint(w, `{"data":{"bulkOperationRunQuery":{"bulkOperation":{"id":"gid://shopify/BulkOperation/1","status":"CREATED"},"userErrors":[]}}}`)
			default:
				fmt.Fprintf(w, `{"data":{"bulkOperation":{"status":"COMPLETED","errorCode":null,"url":%q}}}`, srv.URL+"/export.jsonl")
			}
		case "/export.jsonl":
			fmt.Fprint(w, *lines)
		default:
			http.NotFound(w, r)
		}
	}))
	old := shopifyBase
	shopifyBase = func(string) string { return srv.URL }
	t.Cleanup(func() { srv.Close(); shopifyBase = old })
	return srv, &tokens
}

const shopLines = `{"id":"gid://shopify/Order/1","name":"#1001","createdAt":"2026-10-01T20:00:00Z","test":false,"cancelledAt":null,"displayFinancialStatus":"PAID","displayFulfillmentStatus":"FULFILLED","sourceName":"web","customer":{"id":"gid://shopify/Customer/77"},"totalPriceSet":{"shopMoney":{"amount":"1250.00","currencyCode":"INR"}},"totalDiscountsSet":{"shopMoney":{"amount":"50.00"}},"totalShippingPriceSet":{"shopMoney":{"amount":"0.0"}},"totalTaxSet":{"shopMoney":{"amount":"190.68"}},"totalRefundedSet":{"shopMoney":{"amount":"0.0"}}}
{"id":"gid://shopify/LineItem/11","title":"Kesar Oil","sku":"KO-1","vendor":"Kesar","quantity":2,"discountedTotalSet":{"shopMoney":{"amount":"1000.00"}},"__parentId":"gid://shopify/Order/1"}
{"id":"gid://shopify/LineItem/12","title":"Soap","sku":"S-1","vendor":"Kesar","quantity":1,"discountedTotalSet":{"shopMoney":{"amount":"250.00"}},"__parentId":"gid://shopify/Order/1"}
{"id":"gid://shopify/Order/2","name":"#1002","createdAt":"2026-10-03T05:00:00Z","test":false,"cancelledAt":"2026-10-04T05:00:00Z","displayFinancialStatus":"PARTIALLY_REFUNDED","displayFulfillmentStatus":"UNFULFILLED","sourceName":"pos","customer":null,"totalPriceSet":{"shopMoney":{"amount":"500.00","currencyCode":"INR"}},"totalDiscountsSet":{"shopMoney":{"amount":"0.0"}},"totalShippingPriceSet":{"shopMoney":{"amount":"40.0"}},"totalTaxSet":{"shopMoney":{"amount":"0.0"}},"totalRefundedSet":{"shopMoney":{"amount":"100.0"}}}
{"id":"gid://shopify/LineItem/21","title":"Soap","sku":"S-1","vendor":"Kesar","quantity":2,"discountedTotalSet":{"shopMoney":{"amount":"500.00"}},"__parentId":"gid://shopify/Order/2"}
{"id":"gid://shopify/Order/3","name":"#1003","createdAt":"2026-10-03T06:00:00Z","test":true,"totalPriceSet":{"shopMoney":{"amount":"9.00","currencyCode":"INR"}}}
`

func TestShopifyConnection(t *testing.T) {
	lines := shopLines
	_, tokens := fakeShopify(t, &lines)
	ks := &memKeys{m: map[string]string{}}
	st := &Store{Dir: t.TempDir(), Keys: ks}
	sh, _ := st.New("")
	if _, err := st.Connect(context.Background(), sh.ID, "shopify", "kesar", "cid\nwrong"); err == nil || !strings.Contains(err.Error(), "Client ID and secret") {
		t.Fatalf("wrong secret: %v", err)
	}
	sh, err := st.Connect(context.Background(), sh.ID, "shopify", "kesar", "cid\nsec")
	if err != nil {
		t.Fatal(err)
	}
	if len(sh.Sources) != 2 || sh.Sources[0].Label != "Shopify orders" || sh.Title != "Shopify orders" || !sh.Live() {
		t.Fatalf("%+v", sh)
	}
	if ks.m[secretName(sh.ID, "shopify", "kesar.myshopify.com")] != "cid\nsec" {
		t.Fatalf("secret not kept: %v", ks.m)
	}
	b, _ := os.ReadFile(filepath.Join(st.Dir, sh.ID, "files", sh.Sources[0].File))
	want := "Order,Date,Payment status,Fulfilment,Channel,Customer ID,Cancelled,Sales after refunds (INR),Order total (INR),Discounts (INR),Shipping (INR),Tax (INR),Refunded (INR)\n" +
		"#1002,2026-10-03,Partially refunded,Unfulfilled,Point of sale,,yes,400.00,500.00,0.0,40.0,0.0,100.0\n" +
		"#1001,2026-10-02,Paid,Fulfilled,Online store,77,no,1250.00,1250.00,50.00,0.0,190.68,0.0\n" // 8 pm UTC is the next day in India; test orders left out
	if string(b) != want {
		t.Fatalf("orders:\n%s", b)
	}
	b, _ = os.ReadFile(filepath.Join(st.Dir, sh.ID, "files", sh.Sources[1].File))
	// The cancelled order sold nothing.
	if string(b) != "Order number,Date,Product,SKU,Vendor,Quantity,Sales (INR)\n#1001,2026-10-02,Kesar Oil,KO-1,Kesar,2,1000.00\n#1001,2026-10-02,Soap,S-1,Kesar,1,250.00\n" {
		t.Fatalf("products:\n%s", b)
	}
	if _, err := st.Connect(context.Background(), sh.ID, "shopify", "kesar.myshopify.com", "cid\nsec"); err == nil {
		t.Error("the same store connected twice")
	}
	// An automatic refresh leaves a fresh connection alone; a forced one reads it.
	if _, changed, err := st.Refresh(context.Background(), sh.ID, false); err != nil || changed {
		t.Fatalf("auto refresh: %v %v", changed, err)
	}
	if _, changed, err := st.Refresh(context.Background(), sh.ID, true); err != nil || changed || *tokens != 1 {
		t.Fatalf("same data: changed %v, %d tokens, %v", changed, *tokens, err)
	}
	needPython(t, false)
	if _, err := st.Load(context.Background(), sh.ID); err != nil {
		t.Fatal(err)
	}
	lines = strings.Replace(shopLines, `"amount":"500.00","currencyCode"`, `"amount":"550.00","currencyCode"`, 1)
	sh, changed, err := st.Refresh(context.Background(), sh.ID, true)
	if err != nil || !changed || sh.Changed != "Same number of rows; some values changed." {
		t.Fatalf("changed data: %v %q %v", changed, sh.Changed, err)
	}
	// A connection that fails on an automatic refresh waits its 30 minutes and lets the rest update.
	ks.m[secretName(sh.ID, "shopify", "kesar.myshopify.com")] = "cid\nwrong"
	st.mu.Lock()
	st.tokens = nil
	st.mu.Unlock()
	if _, _, err := st.Refresh(context.Background(), sh.ID, true); err == nil {
		t.Fatal("a forced refresh hid the failure")
	}
	_, _ = st.update(sh.ID, func(sh *Sheet) {
		for i := range sh.Sources {
			sh.Sources[i].Fetched = time.Time{}
		}
	})
	if _, _, err := st.Refresh(context.Background(), sh.ID, false); err == nil {
		t.Fatal("the failure was not reported")
	}
	if _, _, err := st.Refresh(context.Background(), sh.ID, false); err != nil {
		t.Fatalf("a failed connection was asked again at once: %v", err)
	}
	ks.m[secretName(sh.ID, "shopify", "kesar.myshopify.com")] = "cid\nsec"
	prof, _ := st.Profile(sh.ID)
	if len(prof.Datasets) != 2 || len(prof.Links) != 0 {
		t.Fatalf("orders and products must stay two tables: %+v", prof.Links)
	}
	// The key goes with the last table of the store.
	st.RemoveSource(sh.ID, sh.Sources[0].File)
	if len(ks.m) != 1 {
		t.Fatal("key dropped while a table still uses it")
	}
	st.RemoveSource(sh.ID, sh.Sources[1].File)
	if len(ks.m) != 0 {
		t.Fatal("key kept after the store was removed")
	}
}

func TestMetaConnection(t *testing.T) {
	pages := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("access_token") != "good" {
			fmt.Fprint(w, `{"error":{"message":"Invalid OAuth access token.","type":"OAuthException","code":190}}`)
			return
		}
		switch r.URL.Path {
		case "/v26.0/act_1234567":
			fmt.Fprint(w, `{"name":"Kesar","currency":"INR","id":"act_1234567"}`)
		case "/v26.0/act_1234567/insights":
			var span struct{ Until string }
			_ = json.Unmarshal([]byte(q.Get("time_range")), &span)
			if q.Get("after") == "" && (q.Get("level") != "campaign" || q.Get("time_increment") != "1") {
				t.Errorf("insights query: %v", q)
			}
			switch {
			case q.Get("after") == "p2":
				pages++
				fmt.Fprint(w, `{"data":[{"date_start":"2026-10-02","campaign_name":"Brand","spend":"300.5","impressions":"900","clicks":"30"}],"paging":{"next":"https://evil.example/x"}}`)
			case span.Until == time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"): // the latest window, to yesterday
				pages++
				// Purchases twice, as omni and as website: only one may count.
				fmt.Fprintf(w, `{"data":[{"date_start":"2026-10-01","campaign_name":"Brand","spend":"1200.75","impressions":"5000","clicks":"120",`+
					`"actions":[{"action_type":"link_click","value":"120"},{"action_type":"offsite_conversion.fb_pixel_purchase","value":"3"},{"action_type":"omni_purchase","value":"4"}],`+
					`"action_values":[{"action_type":"offsite_conversion.fb_pixel_purchase","value":"2400"},{"action_type":"omni_purchase","value":"3100"}]}],`+
					`"paging":{"next":%q}}`, srv.URL+r.URL.Path+"?access_token=good&after=p2")
			default:
				fmt.Fprint(w, `{"data":[]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	old := metaBase
	metaBase = srv.URL + "/v26.0"
	t.Cleanup(func() { srv.Close(); metaBase = old })
	if _, err := metaAds(context.Background(), "1234567", "bad"); err == nil || !strings.Contains(err.Error(), "access token") {
		t.Fatalf("bad token: %v", err)
	}
	tables, err := metaAds(context.Background(), "1234567", "good")
	if err != nil {
		t.Fatal(err)
	}
	want := "Date,Campaign,Spend (INR),Impressions,Clicks,Purchases,Purchase value (INR)\n" +
		"2026-10-01,Brand,1200.75,5000,120,4,3100\n2026-10-02,Brand,300.5,900,30,,\n"
	if len(tables) != 1 || tables[0].label != "Meta Ads: Kesar" || string(tables[0].b) != want || pages != 2 {
		t.Fatalf("%d pages, %+v\n%s", pages, tables, tables[0].b)
	}
}
