package web

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

const (
	mailboxUser     = "anna@example.test"
	mailboxPassword = "MARKER-PW-4f1c"
)

func newMailbox(t *testing.T) *imaptest.Server {
	t.Helper()
	double, err := imaptest.New(imaptest.Options{Security: imaptest.ImplicitTLS, User: mailboxUser, Password: mailboxPassword})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := double.Close(); err != nil {
			t.Errorf("close the double: %v", err)
		}
	})
	return double
}

func trusting(t *testing.T, double *imaptest.Server) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(double.CAPEM()) {
		t.Fatal("the double's CA isn't PEM")
	}
	return roots
}

// sourceBody is a request body that keeps every input rule, for the double's
// mailbox.
func sourceBody(t *testing.T, double *imaptest.Server) map[string]any {
	t.Helper()
	host, port, err := net.SplitHostPort(double.Addr())
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"name":      "Anna",
		"host":      host,
		"port":      number,
		"security":  "tls",
		"user_name": mailboxUser,
		"password":  mailboxPassword,
		"folders":   []string{"INBOX", "Archiv"},
	}
}

func encode(t *testing.T, body map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func (h *harness) sourceIDs(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.db.Reader.QueryRow("SELECT COUNT(*) FROM imap_source").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// entryJSON is a source's list entry as the contract writes it, while
// syncing or waiting. progress, lastSuccess, and nextSyncAt are JSON.
func entryJSON(id int64, port int, state, progress, lastSuccess, nextSyncAt string, collected int) string {
	return fmt.Sprintf(`{"id":%d,"name":"Anna","host":"127.0.0.1","port":%d,"security":"tls","user_name":%q,`+
		`"state":%q,"folders":["INBOX","Archiv"],"progress":%s,"last_success":%s,"collected":%d,`+
		`"next_sync_at":%s,"paused_at":null,"failure":null,`+
		`"problems":{"unavailable_folders":0,"waiting_for_retry":0,"not_archived":0}}`,
		id, port, mailboxUser, state, progress, lastSuccess, collected, nextSyncAt)
}

// Contract: POST /api/sources saves a source whose sign-in worked, and starts
// its first sync. GET /api/sources and GET /api/sources/{id} read it.
func TestAddSource(t *testing.T) {
	double := newMailbox(t)
	if err := double.AddFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	inbox := mail("collected from INBOX")
	archived := mail("collected from Archiv")
	received := time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC)
	if _, err := double.AddMessage("INBOX", inbox, received); err != nil {
		t.Fatal(err)
	}
	if _, err := double.AddMessage("Archiv", archived, received.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWith(t, Routes(), harnessOptions{key: true, roots: trusting(t, double), start: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	body := sourceBody(t, double)
	port := body["port"].(int)

	response, answer := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, body)), session)

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, answer)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(answer), &created); err != nil {
		t.Fatal(err)
	}
	assertJSONResponse(t, response, answer, http.StatusCreated,
		entryJSON(created.ID, port, "syncing", `{"archived":0,"found":0}`, "null", "null", 0))
	if strings.Contains(answer, mailboxPassword) {
		t.Error("the answer holds the password")
	}

	path := "/api/sources/" + strconv.FormatInt(created.ID, 10)
	waitFor(t, "the first sync", func() bool {
		response, body := h.do(t, http.MethodGet, path, session)
		return response.StatusCode == http.StatusOK && !strings.Contains(body, `"last_success":null`)
	})
	finished := fmt.Sprintf(`{"at":%q,"archived":2}`, testTime)
	// The next sync is due 5 minutes after the first ended.
	nextSync := `"2026-11-06T15:55:52.123Z"`

	t.Run("the list", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, "/api/sources", session)
		assertJSONResponse(t, response, body, http.StatusOK,
			`{"collection":"available","sources":[`+entryJSON(created.ID, port, "waiting", "null", finished, nextSync, 2)+`]}`)
	})

	t.Run("one source", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, session)
		want := strings.Replace(entryJSON(created.ID, port, "waiting", "null", finished, nextSync, 2),
			`"folders":["INBOX","Archiv"]`,
			`"folders":[{"name":"INBOX","unavailable_since":null,"renumbered_at":null},{"name":"Archiv","unavailable_since":null,"renumbered_at":null}],`+
				`"messages":{"waiting_for_retry":{"total":0,"items":[]},"not_archived":{"total":0,"items":[]}}`, 1)
		assertJSONResponse(t, response, body, http.StatusOK, want)
	})

	t.Run("an unknown ID", func(t *testing.T) {
		for _, id := range []string{"abc", "999", "-1", "1.5"} {
			response, body := h.do(t, http.MethodGet, "/api/sources/"+id, session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
		}
	})

	t.Run("collected messages name the source and folder", func(t *testing.T) {
		var inboxID int64
		if err := h.db.Reader.QueryRow(
			"SELECT id FROM imap_folder WHERE source_id = ? AND name = 'INBOX'", created.ID).Scan(&inboxID); err != nil {
			t.Fatal(err)
		}
		// The double always reports INTERNALDATE, so archive records one
		// without it directly.
		undated := mail("collected without a date")
		result, err := h.archive.StoreCollected(context.Background(), bytes.NewReader(undated),
			archive.CollectedMeta{SourceID: created.ID, FolderID: inboxID})
		if err != nil {
			t.Fatal(err)
		}
		undatedID := result.(archive.Stored).Message.ID

		source := fmt.Sprintf(`{"id":%d,"name":"Anna","host":"127.0.0.1","user_name":%q,"removed":false}`, created.ID, mailboxUser)
		collected := func(folder string, receivedAt string) string {
			return fmt.Sprintf(`{"source_type":"imap_collection","observed_at":%q,"source":%s,"folder":%q,"received_at":%s}`,
				testTime, source, folder, receivedAt)
		}
		response, body := h.do(t, http.MethodGet, "/api/messages", session)
		var listed struct {
			Items []struct {
				ID          int64  `json:"id"`
				ContentHash string `json:"content_hash"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed.Items) != 3 {
			t.Fatalf("GET /api/messages = %s, want three messages", body)
		}
		ids := map[string]int64{}
		for _, item := range listed.Items {
			ids[item.ContentHash] = item.ID
		}
		item := func(content []byte, latest string) string {
			return fmt.Sprintf(`{"id":%d,"content_hash":%q,"size":%d,"observation_count":1,"latest":%s}`,
				ids[hashOf(content)], hashOf(content), len(content), latest)
		}
		// Every entry has the same time, so the most recent is the one
		// recorded last. The sync took INBOX before Archiv.
		assertJSONResponse(t, response, body, http.StatusOK, `{"items":[`+
			item(undated, collected("INBOX", "null"))+","+
			item(archived, collected("Archiv", fmt.Sprintf("%q", formatTime(received.Add(time.Hour)))))+","+
			item(inbox, collected("INBOX", fmt.Sprintf("%q", formatTime(received))))+
			`],"page":1,"page_size":50,"total":3}`)

		// Importing the undated bytes adds an import entry to their history.
		response, body = h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(undated), both(session, withFileName("undated.eml")))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("import = %d %s, want 200", response.StatusCode, body)
		}
		response, body = h.do(t, http.MethodGet, "/api/messages/"+strconv.FormatInt(undatedID, 10), session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(
			`{"id":%d,"content_hash":%q,"size":%d,"observations":[`+
				`{"source_type":"manual_import","observed_at":%q,"account_email":"admin@example.test","file_name":"undated.eml"},%s]}`,
			undatedID, hashOf(undated), len(undated), testTime, collected("INBOX", "null")))
	})
}

// Contract: a body that isn't JSON, has unknown fields, or breaks an input
// rule answers 400 invalid_fields, naming each field that broke a rule. A
// collector with no key answers a valid body with 409 no_key, so the
// accepted cases don't reach a mail server.
func TestAddSourceInputRules(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	valid := func() map[string]any {
		return map[string]any{
			"name":      "Anna",
			"host":      "imap.example.test",
			"port":      993,
			"security":  "tls",
			"user_name": mailboxUser,
			"password":  mailboxPassword,
			"folders":   []string{"INBOX"},
		}
	}
	names := func(n int) []string {
		folders := make([]string, n)
		for i := range folders {
			folders[i] = "Folder " + strconv.Itoa(i)
		}
		return folders
	}
	longHost := strings.Repeat("a.", 126) + "ab"
	host253 := strings.Repeat("a.", 125) + "abc"

	tests := []struct {
		name   string
		field  string
		value  any
		broken []string // nil means accepted.
	}{
		{"empty name", "name", "", []string{"name"}},
		{"name of spaces", "name", "   ", []string{"name"}},
		{"name of 101 code points", "name", strings.Repeat("ä", 101), []string{"name"}},
		{"name of 100 code points", "name", strings.Repeat("ä", 100), nil},
		{"name that isn't a string", "name", 7, []string{"name"}},
		{"missing name", "name", nil, []string{"name"}},
		{"host with a space", "host", "imap example.test", []string{"host"}},
		{"host of 254 bytes", "host", longHost, []string{"host"}},
		{"host in brackets", "host", "[::1]", []string{"host"}},
		{"host with a non-ASCII name", "host", "bücher.example", []string{"host"}},
		{"empty host", "host", "", []string{"host"}},
		{"IPv6 host", "host", "::1", nil},
		{"host in xn-- form", "host", "xn--bcher-kva.example", nil},
		{"IPv4 host", "host", "127.0.0.1", nil},
		{"host of 253 bytes", "host", host253, nil},
		{"port 0", "port", 0, []string{"port"}},
		{"port 65536", "port", 65536, []string{"port"}},
		{"port as a string", "port", "993", []string{"port"}},
		{"port with a fraction", "port", 993.5, []string{"port"}},
		{"port 1", "port", 1, nil},
		{"port 65535", "port", 65535, nil},
		{"security ssl", "security", "ssl", []string{"security"}},
		{"security in capitals", "security", "TLS", []string{"security"}},
		{"security starttls", "security", "starttls", nil},
		{"user name with a control character", "user_name", "anna\u0001@example.test", []string{"user_name"}},
		{"user name of 1,025 bytes", "user_name", strings.Repeat("u", 1025), []string{"user_name"}},
		{"empty user name", "user_name", "", []string{"user_name"}},
		{"user name of 1,024 bytes", "user_name", strings.Repeat("u", 1024), nil},
		{"empty password", "password", "", []string{"password"}},
		{"password of 1,025 bytes", "password", strings.Repeat("p", 1025), []string{"password"}},
		{"password of 1,024 bytes", "password", strings.Repeat("p", 1024), nil},
		{"no folders", "folders", []string{}, []string{"folders"}},
		{"1,001 folders", "folders", names(1001), []string{"folders"}},
		{"a repeated folder", "folders", []string{"INBOX", "Archiv", "INBOX"}, []string{"folders"}},
		{"an empty folder name", "folders", []string{"INBOX", ""}, []string{"folders"}},
		{"a folder name of 1,001 bytes", "folders", []string{strings.Repeat("f", 1001)}, []string{"folders"}},
		{"1,000 folders", "folders", names(1000), nil},
		{"a folder name of 1,000 bytes", "folders", []string{strings.Repeat("f", 1000)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := valid()
			if tt.value == nil {
				delete(body, tt.field)
			} else {
				body[tt.field] = tt.value
			}

			response, answer := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, body)), session)

			if tt.broken == nil {
				assertJSONResponse(t, response, answer, http.StatusConflict, `{"error":"no_key"}`)
				return
			}
			fields, err := json.Marshal(tt.broken)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONResponse(t, response, answer, http.StatusBadRequest, `{"error":"invalid_fields","fields":`+string(fields)+`}`)
		})
	}

	t.Run("several broken rules name each field", func(t *testing.T) {
		body := valid()
		body["name"] = ""
		body["port"] = 0
		body["password"] = ""
		body["folders"] = []string{}

		response, answer := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, body)), session)

		assertJSONResponse(t, response, answer, http.StatusBadRequest,
			`{"error":"invalid_fields","fields":["name","port","password","folders"]}`)
	})

	t.Run("bytes that aren't UTF-8", func(t *testing.T) {
		encoded := encode(t, valid())
		encoded = bytes.Replace(encoded, []byte(`"Anna"`), []byte("\"An\xffna\""), 1)

		response, answer := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encoded), session)

		assertJSONResponse(t, response, answer, http.StatusBadRequest, `{"error":"invalid_fields","fields":["name"]}`)
	})

	for _, tt := range []struct {
		name string
		body []byte
	}{
		{"not JSON", []byte("name=Anna")},
		{"an array", []byte(`[]`)},
		{"an unknown field", bytes.Replace(encode(t, valid()), []byte(`{`), []byte(`{"color":"blue",`), 1)},
		{"two values", append(encode(t, valid()), []byte(` {}`)...)},
		{"over 1 MiB", append(encode(t, valid())[:1], append(bytes.Repeat([]byte(" "), 1<<20), encode(t, valid())[1:]...)...)},
		{"empty", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, answer := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(tt.body), session)
			assertJSONResponse(t, response, answer, http.StatusBadRequest, `{"error":"invalid_fields","fields":[]}`)
		})
	}

	if n := h.sourceIDs(t); n != 0 {
		t.Errorf("%d sources were saved, want none", n)
	}
}

// Contract: with no key, a request that would store a password answers 409
// no_key and saves nothing, and the list says collection needs the key.
func TestAddSourceWithoutKey(t *testing.T) {
	double := newMailbox(t)
	h := newHarnessWith(t, Routes(), harnessOptions{roots: trusting(t, double), start: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	response, body := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, sourceBody(t, double))), session)

	assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"no_key"}`)
	if n := h.sourceIDs(t); n != 0 {
		t.Errorf("%d sources were saved, want none", n)
	}
	for _, command := range double.Commands() {
		t.Errorf("the double saw %s", command.Name)
	}
	response, body = h.do(t, http.MethodGet, "/api/sources", session)
	assertJSONResponse(t, response, body, http.StatusOK, `{"collection":"no_key","sources":[]}`)
}

// Contract: without a collector, as serve runs without --preview-collection,
// the source paths are unknown: 404 with a session, 401 without.
func TestSourcesWithoutCollector(t *testing.T) {
	h := newHarnessWith(t, Routes(), harnessOptions{noCollector: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	for _, route := range Routes() {
		if !route.Collection {
			continue
		}
		path := concretePath(route.Path, 1)
		t.Run(route.pattern(), func(t *testing.T) {
			response, body := h.send(t, route.Method, path, strings.NewReader(`{}`), session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)

			response, body = h.send(t, route.Method, path, strings.NewReader(`{}`), nil)
			assertJSONResponse(t, response, body, http.StatusUnauthorized, noSession)
		})
	}
}

// Contract: POST /api/sources whose sign-in fails answers 422 check_failed
// with the category and the server's reply, which is absent when it sent
// none, and saves nothing.
func TestAddSourceCheckFailed(t *testing.T) {
	double := newMailbox(t)
	// The harness trusts only double's CA, so another double's certificate
	// fails.
	untrusted := newMailbox(t)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closedPort, err := net.SplitHostPort(closed.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	h := newHarnessWith(t, Routes(), harnessOptions{key: true, roots: trusting(t, double), start: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	login := func(answer error) imaptest.Hook {
		return func(command imaptest.Command) error {
			if command.Name == "LOGIN" {
				return answer
			}
			return nil
		}
	}
	cases := []struct {
		name string
		body func() map[string]any
		want string
	}{
		{"server_not_reachable", func() map[string]any {
			body := sourceBody(t, double)
			body["port"], _ = strconv.Atoi(closedPort)
			return body
		}, `{"error":"check_failed","category":"server_not_reachable"}`},
		{"secure_connection_failed", func() map[string]any {
			return sourceBody(t, untrusted)
		}, `{"error":"check_failed","category":"secure_connection_failed"}`},
		{"sign_in_rejected", func() map[string]any {
			double.SetHook(login(imaptest.No(imap.ResponseCodeAuthenticationFailed, "Invalid credentials")))
			return sourceBody(t, double)
		}, `{"error":"check_failed","category":"sign_in_rejected","reply":"[AUTHENTICATIONFAILED] Invalid credentials"}`},
		{"server_error", func() map[string]any {
			double.SetHook(login(imaptest.Bad("", "Command syntax error")))
			return sourceBody(t, double)
		}, `{"error":"check_failed","category":"server_error","reply":"Command syntax error"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, tt.body())), session)
			assertJSONResponse(t, response, body, http.StatusUnprocessableEntity, tt.want)
			if n := h.sourceIDs(t); n != 0 {
				t.Errorf("%d sources were saved, want none", n)
			}
		})
	}

	t.Run("timed_out", func(t *testing.T) {
		stalled := newMailbox(t)
		stalled.SetHook(login(imaptest.ErrStall))
		h := newHarnessWith(t, Routes(), harnessOptions{key: true, roots: trusting(t, stalled), start: true, checkLimit: 2 * time.Second})
		h.createAccount(t, "admin@example.test")
		session := h.signIn(t, "admin@example.test")

		response, body := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, sourceBody(t, stalled))), session)

		assertJSONResponse(t, response, body, http.StatusUnprocessableEntity, `{"error":"check_failed","category":"timed_out"}`)
		if n := h.sourceIDs(t); n != 0 {
			t.Errorf("%d sources were saved, want none", n)
		}
	})
}
