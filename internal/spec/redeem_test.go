package spec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTicket = "tkt_" + "example0123456789abcdef"

// fakeServer answers every POST with status and body and records the
// requests.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
}

type recorded struct {
	Path, ContentType, Accept, UserAgent string
	Body                                 map[string]any
}

func newFakeServer(t *testing.T, status int, body string, headers map[string]string) *fakeServer {
	t.Helper()
	server := &fakeServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(data, &decoded)
		server.mu.Lock()
		server.requests = append(server.requests, recorded{r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Accept"), r.Header.Get("User-Agent"), decoded})
		server.mu.Unlock()
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func closedPortURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	listener.Close()
	return url
}

func redeemCode(t *testing.T, serverURL string) string {
	t.Helper()
	_, err := Redeem(context.Background(), serverURL, 7, testTicket)
	var redeemErr *RedeemError
	if !errors.As(err, &redeemErr) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testTicket) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error text = %q", err.Error())
	}
	return redeemErr.Code
}

func TestRedeemReturnsSpecOfServer(t *testing.T) {
	body, _ := json.Marshal(makeSpec(t, server, false, nil))
	fake := newFakeServer(t, http.StatusOK, string(body), nil)
	data, err := Redeem(context.Background(), fake.URL, 7, testTicket)
	if err != nil {
		t.Fatal(err)
	}
	if data["mcp_token"] != testToken || data["run_id"] != json.Number("7") {
		t.Fatalf("data = %v", data)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("requests = %v", fake.requests)
	}
	request := fake.requests[0]
	if request.Path != RedeemPath || request.ContentType != "application/json" || request.Accept != "application/json" || !strings.HasPrefix(request.UserAgent, "leadin-helper/") {
		t.Fatalf("request = %+v", request)
	}
	if len(request.Body) != 2 || request.Body["run_id"] != 7.0 || request.Body["ticket"] != testTicket {
		t.Fatalf("body = %v", request.Body)
	}
}

func TestRedeem404IsTicketRejected(t *testing.T) {
	fake := newFakeServer(t, http.StatusNotFound, `{"code": "invalid_ticket"}`, nil)
	if code := redeemCode(t, fake.URL); code != "ticket_rejected" {
		t.Fatalf("code = %s", code)
	}
}

func TestRedeemOtherStatusIsServerError(t *testing.T) {
	for _, status := range []int{500, 400, 403, 429} {
		fake := newFakeServer(t, status, `{}`, nil)
		if code := redeemCode(t, fake.URL); code != "server_error" {
			t.Errorf("%d: code = %s", status, code)
		}
	}
}

func TestRedeemDoesNotFollowRedirects(t *testing.T) {
	target := newFakeServer(t, http.StatusOK, `{}`, nil)
	fake := newFakeServer(t, http.StatusTemporaryRedirect, `{}`, map[string]string{"Location": target.URL + "/x"})
	if code := redeemCode(t, fake.URL); code != "server_error" {
		t.Fatalf("code = %s", code)
	}
	if len(fake.requests) != 1 || len(target.requests) != 0 {
		t.Fatalf("requests: %d, redirect target %d", len(fake.requests), len(target.requests))
	}
}

func TestRedeemInvalidJSONIsServerError(t *testing.T) {
	for _, body := range []string{"<html>", "[1, 2]", "null", "\xff\xfe", `{"a": 1} {"b": 2}`, `{"a": "` + strings.Repeat("x", MaxRedeemBody) + `"}`} {
		fake := newFakeServer(t, http.StatusOK, body, nil)
		if code := redeemCode(t, fake.URL); code != "server_error" {
			t.Errorf("%.20q: code = %s", body, code)
		}
	}
}

func TestRedeemWithoutServerIsServerUnreachable(t *testing.T) {
	if code := redeemCode(t, closedPortURL(t)); code != "server_unreachable" {
		t.Fatalf("code = %s", code)
	}
}

func TestRedeemWithUntrustedCertificateIsServerUnreachable(t *testing.T) {
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer fake.Close()
	if code := redeemCode(t, fake.URL); code != "server_unreachable" {
		t.Fatalf("code = %s", code)
	}
}

func TestRedeemTimeoutIsServerUnreachable(t *testing.T) {
	release := make(chan struct{})
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer fake.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := Redeem(ctx, fake.URL, 7, testTicket)
	var redeemErr *RedeemError
	if !errors.As(err, &redeemErr) || redeemErr.Code != "server_unreachable" {
		t.Fatalf("err = %v", err)
	}
}

func TestIsValidServerURL(t *testing.T) {
	for _, url := range []string{"https://leadin.example", "https://leadin.example:8443", "http://localhost:8000", "http://127.0.0.1:8000", "http://[::1]:8000", "https://LeadIn.Example"} {
		if !IsValidServerURL(url) {
			t.Errorf("%q rejected", url)
		}
	}
	for _, url := range []string{
		"http://leadin.example", "https://leadin.example/", "https://leadin.example/api", "https://leadin.example?x=1",
		"https://leadin.example?", "https://leadin.example#x", "https://leadin.example#", "ftp://localhost",
		"https://user:pw@leadin.example", "localhost:8000", "", "http://localhost.evil.example", "https://leadin.example:99999",
		"https://leadin.example:abc", "https:leadin.example", "http://127.0.0.1:8000/%2e",
	} {
		if IsValidServerURL(url) {
			t.Errorf("%q accepted", url)
		}
	}
}
