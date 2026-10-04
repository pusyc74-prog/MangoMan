package guardian

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Telegram talks to the owner's own bot (made in @BotFather) straight from
// this computer: no server in between. Only the owner's chat is obeyed.
type Telegram struct {
	Token string `json:"token"`
	Chat  string `json:"chat"`
	API   string `json:"-"` // default https://api.telegram.org
}

// LoadTelegram reads the bot settings saved in dir, or returns nil.
func LoadTelegram(dir string) *Telegram {
	b, err := os.ReadFile(filepath.Join(dir, "telegram.json"))
	if err != nil {
		return nil
	}
	var t Telegram
	if json.Unmarshal(b, &t) != nil || t.Token == "" || t.Chat == "" {
		return nil
	}
	return &t
}

// Save stores the bot settings in dir, readable only by this user.
func (t *Telegram) Save(dir string) error {
	b, _ := json.Marshal(t)
	return os.WriteFile(filepath.Join(dir, "telegram.json"), b, 0o600)
}

func (t *Telegram) call(ctx context.Context, method string, in, out any) error {
	api := t.API
	if api == "" {
		api = "https://api.telegram.org"
	}
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/bot"+t.Token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 70 * time.Second}).Do(req)
	if err != nil {
		return errors.New("Telegram not reachable") // the error would contain the token
	}
	defer resp.Body.Close()
	var r struct {
		OK     bool            `json:"ok"`
		Desc   string          `json:"description"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || !r.OK {
		return fmt.Errorf("Telegram %s: %s", method, r.Desc)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Send posts a message to the owner, with buttons as {label, data} pairs.
func (t *Telegram) Send(text string, buttons ...[2]string) error {
	msg := map[string]any{"chat_id": t.Chat, "text": cut(text, 4000)}
	if len(buttons) > 0 {
		var row []map[string]string
		for _, b := range buttons {
			row = append(row, map[string]string{"text": b[0], "callback_data": b[1]})
		}
		msg["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{row}}
	}
	return t.call(context.Background(), "sendMessage", msg, nil)
}

// Update is a message or a button press.
type Update struct {
	ID     int
	Chat   string
	Text   string // a message
	Data   string // a button's data
	Button string // the button press id, to acknowledge it
}

// Updates waits up to wait for new updates after offset.
func (t *Telegram) Updates(ctx context.Context, offset int, wait time.Duration) ([]Update, error) {
	var raw []struct {
		ID      int `json:"update_id"`
		Message *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			Text string `json:"text"`
		} `json:"message"`
		Callback *struct {
			ID      string `json:"id"`
			Data    string `json:"data"`
			Message struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
	}
	in := map[string]any{"offset": offset, "timeout": int(wait.Seconds()), "allowed_updates": []string{"message", "callback_query"}}
	if err := t.call(ctx, "getUpdates", in, &raw); err != nil {
		return nil, err
	}
	var out []Update
	for _, r := range raw {
		u := Update{ID: r.ID}
		switch {
		case r.Callback != nil:
			u.Chat, u.Data, u.Button = strconv.FormatInt(r.Callback.Message.Chat.ID, 10), r.Callback.Data, r.Callback.ID
		case r.Message != nil:
			u.Chat, u.Text = strconv.FormatInt(r.Message.Chat.ID, 10), r.Message.Text
		}
		out = append(out, u)
	}
	return out, nil
}

// Ack answers a button press so Telegram stops showing it as loading.
func (t *Telegram) Ack(id, text string) {
	_ = t.call(context.Background(), "answerCallbackQuery", map[string]string{"callback_query_id": id, "text": text}, nil)
}
