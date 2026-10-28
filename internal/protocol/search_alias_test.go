package protocol

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSyncMailboxMessagesSearchesAliasOutsideRecentWindow(t *testing.T) {
	const alias = "alias@icloud.com"
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/mailws2/v1/geqs/query"):
			_, _ = w.Write([]byte(`{"domainObjects":[{"identifier":"inbox","name":"INBOX","messageCount":1}]}`))
		case strings.Contains(r.URL.Path, "/mailws2/v1/thread/search"):
			searches = append(searches, string(body))
			threadID := "thread-other"
			if strings.Contains(string(body), alias) {
				threadID = "thread-alias"
			}
			_, _ = w.Write([]byte(`{"threadList":[{"threadId":"` + threadID + `","subject":"mail","preview":"x"}]}`))
		case strings.Contains(r.URL.Path, "/mailws2/v1/thread/get"):
			if strings.Contains(string(body), "thread-alias") {
				_, _ = w.Write([]byte(`{"messageMetadataList":[{"uid":"9","messageId":"<otp@example.com>","subject":"OpenAI","preview":"code 123456","to":["` + alias + `"],"date":"2026-10-19T12:00:00Z"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"messageMetadataList":[{"uid":"1","messageId":"<other@example.com>","subject":"hello","preview":"nope","to":["other@icloud.com"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewICloudClient()
	session := ICloudSession{
		DSID:               "dsid",
		MailGatewayBaseURL: server.URL,
		Cookies:            []SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "token", Domain: "icloud.com", Path: "/"}},
	}
	items, err := client.SyncMailboxMessages(t.Context(), session, Mailbox{ID: "box", Email: alias}, time.Time{}, "OpenAI", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || ExtractOTP(items[0].Body) != "123456" {
		t.Fatalf("items = %#v", items)
	}
	if len(searches) == 0 || !strings.Contains(strings.Join(searches, "\n"), `"query":"`+alias+`"`) {
		t.Fatalf("search bodies = %s", strings.Join(searches, "\n"))
	}
	var sawFifty bool
	for _, raw := range searches {
		var body map[string]any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatal(err)
		}
		if body["maxResults"] == float64(50) {
			sawFifty = true
		}
	}
	if !sawFifty {
		t.Fatalf("single mailbox search was clamped below 50: %s", strings.Join(searches, "\n"))
	}
}

func TestSyncMailboxMessagesFallsBackWhenAliasQueryRejected(t *testing.T) {
	const alias = "alias@icloud.com"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/mailws2/v1/geqs/query"):
			_, _ = w.Write([]byte(`{"domainObjects":[{"identifier":"inbox","name":"INBOX","messageCount":1}]}`))
		case strings.Contains(r.URL.Path, "/mailws2/v1/thread/search"):
			if strings.Contains(string(body), `"query"`) {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"threadList":[{"threadId":"thread-recent","subject":"OpenAI","preview":"code"}]}`))
		case strings.Contains(r.URL.Path, "/mailws2/v1/thread/get"):
			_, _ = w.Write([]byte(`{"messageMetadataList":[{"uid":"3","messageId":"<otp@example.com>","subject":"OpenAI","preview":"code 654321","to":["` + alias + `"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewICloudClient()
	session := ICloudSession{
		DSID:               "dsid",
		MailGatewayBaseURL: server.URL,
		Cookies:            []SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "token", Domain: "icloud.com", Path: "/"}},
	}
	items, err := client.SyncMailboxMessages(t.Context(), session, Mailbox{ID: "box", Email: alias}, time.Time{}, "OpenAI", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || ExtractOTP(items[0].Body) != "654321" {
		t.Fatalf("items = %#v", items)
	}
}
