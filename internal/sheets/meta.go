package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Meta Ads: a system user's token with the ads_read permission reads the
// ad account's daily results per campaign for the last year.

// metaBase is the Graph API; tests point it elsewhere.
var metaBase = "https://graph.facebook.com/v26.0"

var metaRe = regexp.MustCompile(`^(?:act_)?(\d{5,20})$`)

// MetaAccount reads "act_123..." or "123..." as the account number, or "".
func MetaAccount(s string) string {
	m := metaRe.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if m == nil {
		return ""
	}
	return m[1]
}

// metaGet reads one Graph API answer into out.
func metaGet(ctx context.Context, link string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return errors.New("Meta's link could not be read")
	}
	resp, err := send(req, "Meta")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var e struct {
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != nil {
		switch c := e.Error.Code; {
		case c == 190:
			return errors.New("Meta did not accept the access token: make a new one for the system user, with the ads_read permission")
		case c == 10 || c >= 200 && c < 300:
			return errors.New("this token cannot read that ad account: in Business settings, give the system user the ad account, then make the token again with ads_read")
		case c == 4 || c == 17 || c == 613 || c == 80000 || c == 80004:
			return errors.New("Meta's limit for reading this ad account was reached: try again in an hour")
		}
		return fmt.Errorf("Meta said: %s", e.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Meta answered HTTP %d; try again in a minute", resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

type metaStat struct {
	Type  string `json:"action_type"`
	Value string `json:"value"`
}

// purchases is one kind of purchase, never two added up: all purchases
// (omni) when Meta reports them, else website purchases.
func purchases(stats []metaStat, kind string) (string, string) {
	for _, want := range []string{"omni_purchase", "offsite_conversion.fb_pixel_purchase"} {
		if kind != "" && want != kind {
			continue
		}
		for _, s := range stats {
			if s.Type == want {
				return s.Value, want
			}
		}
	}
	return "", kind
}

// metaAds reads the account's campaigns, day by day, for the last 364 days.
func metaAds(ctx context.Context, account, token string) ([]pulled, error) {
	var acct struct {
		Name     string `json:"name"`
		Currency string `json:"currency"`
	}
	q := url.Values{"fields": {"name,currency"}, "access_token": {token}}
	if err := metaGet(ctx, metaBase+"/act_"+account+"?"+q.Encode(), &acct); err != nil {
		return nil, err
	}
	unit := ""
	if acct.Currency != "" {
		unit = " (" + acct.Currency + ")"
	}
	head := []string{"Date", "Campaign", "Spend" + unit, "Impressions", "Clicks", "Purchases", "Purchase value" + unit}
	var rows [][]string
	end := time.Now().UTC()
	// Four windows of 91 days: short requests, which Meta answers without a report job.
	for w := 3; w >= 0; w-- {
		until := end.AddDate(0, 0, -91*w)
		since := until.AddDate(0, 0, -90)
		span, _ := json.Marshal(map[string]string{"since": since.Format("2006-01-02"), "until": until.Format("2006-01-02")})
		q := url.Values{"level": {"campaign"}, "time_increment": {"1"}, "time_range": {string(span)}, "limit": {"500"},
			"fields": {"campaign_name,spend,impressions,clicks,actions,action_values"}, "access_token": {token}}
		link := metaBase + "/act_" + account + "/insights?" + q.Encode()
		for pages := 0; link != "" && pages < 200; pages++ {
			var page struct {
				Data []struct {
					Date        string     `json:"date_start"`
					Campaign    string     `json:"campaign_name"`
					Spend       string     `json:"spend"`
					Impressions string     `json:"impressions"`
					Clicks      string     `json:"clicks"`
					Actions     []metaStat `json:"actions"`
					Values      []metaStat `json:"action_values"`
				} `json:"data"`
				Paging struct {
					Next string `json:"next"`
				} `json:"paging"`
			}
			if err := metaGet(ctx, link, &page); err != nil {
				return nil, err
			}
			for _, r := range page.Data {
				n, kind := purchases(r.Actions, "")
				v, _ := purchases(r.Values, kind)
				rows = append(rows, []string{r.Date, r.Campaign, r.Spend, r.Impressions, r.Clicks, n, v})
			}
			// Only Meta's own next page is followed: the token rides along in it.
			link = ""
			if strings.HasPrefix(page.Paging.Next, metaOrigin()+"/") {
				link = page.Paging.Next
			}
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("this ad account has no results in the last year")
	}
	ordered(rows, 0, false)
	label := "Meta Ads"
	if acct.Name != "" {
		label = "Meta Ads: " + acct.Name
	}
	return []pulled{{"meta-" + account + ".csv", label, csvFile(append([][]string{head}, rows...))}}, nil
}

// metaOrigin is the scheme and host of metaBase.
func metaOrigin() string {
	u, _ := url.Parse(metaBase)
	return u.Scheme + "://" + u.Host
}
