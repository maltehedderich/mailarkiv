package web

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"maps"
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

// nextSync is when a source whose sync ended at testNow syncs again.
const nextSync = `"2026-11-06T15:55:52.123Z"`

// started is a harness with a key and a started collector that trusts
// double, signed in as an administrator.
func started(t *testing.T, double *imaptest.Server) (*harness, func(*http.Request)) {
	t.Helper()
	h := newHarnessWith(t, Routes(), harnessOptions{key: true, roots: trusting(t, double), start: true})
	h.createAccount(t, "admin@example.test")
	return h, h.signIn(t, "admin@example.test")
}

// addOverHTTP adds the double's mailbox with body and waits until the
// source is waiting after a successful sync.
func (h *harness) addOverHTTP(t *testing.T, session func(*http.Request), body map[string]any) int64 {
	t.Helper()
	response, answer := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, body)), session)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, answer)
	}
	id := createdID(t, answer)
	h.waitUntilWaiting(t, session, id)
	return id
}

// waitUntilWaiting waits until the source is waiting after a successful
// sync.
func (h *harness) waitUntilWaiting(t *testing.T, session func(*http.Request), id int64) {
	t.Helper()
	path := "/api/sources/" + strconv.FormatInt(id, 10)
	waitFor(t, "a successful sync", func() bool {
		response, body := h.do(t, http.MethodGet, path, session)
		return response.StatusCode == http.StatusOK && strings.Contains(body, `"state":"waiting"`) &&
			!strings.Contains(body, `"last_success":null`)
	})
}

// detailJSON turns a list entry into the source's page with folders.
func detailJSON(entry string, folders ...string) string {
	objects := make([]string, len(folders))
	for i, folder := range folders {
		objects[i] = fmt.Sprintf(`{"name":%q,"unavailable_since":null,"renumbered_at":null}`, folder)
	}
	return strings.Replace(entry, `"folders":["INBOX","Archiv"]`,
		`"folders":[`+strings.Join(objects, ",")+`],`+
			`"messages":{"waiting_for_retry":{"total":0,"items":[]},"not_archived":{"total":0,"items":[]}}`, 1)
}

// insertKeylessSource saves a source row straight to the catalog, for a
// harness whose collector has no key and so can't add one.
func (h *harness) insertKeylessSource(t *testing.T) int64 {
	t.Helper()
	inserted, err := h.db.Writer.Exec(`
INSERT INTO imap_source (name, host, port, security, user_name, user_key, sealed_password, created_at)
VALUES ('Anna', 'imap.example.test', 993, 'tls', ?, ?, X'00', ?)`, mailboxUser, mailboxUser, testNow.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	id, err := inserted.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer.Exec("INSERT INTO imap_folder (source_id, name, chosen) VALUES (?, 'INBOX', 1)", id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Contract: POST /api/sources/check signs in and lists the folders, INBOX
// first. POST /api/sources/{id}/check does the same with the stored values
// for each field it leaves out.
func TestCheckRoutes(t *testing.T) {
	double := newMailbox(t)
	if err := double.AddFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	if _, err := double.AddMessage("INBOX", mail("counted"), testNow); err != nil {
		t.Fatal(err)
	}
	h, session := started(t, double)
	mailbox := sourceBody(t, double)
	delete(mailbox, "name")
	delete(mailbox, "folders")
	folders := `{"folders":[{"name":"INBOX","messages":1},{"name":"Archiv","messages":0}]}`

	t.Run("new details", func(t *testing.T) {
		response, body := h.send(t, http.MethodPost, "/api/sources/check", bytes.NewReader(encode(t, mailbox)), session)
		assertJSONResponse(t, response, body, http.StatusOK, folders)
	})

	t.Run("input rules", func(t *testing.T) {
		response, body := h.send(t, http.MethodPost, "/api/sources/check", strings.NewReader(`{"port":"993"}`), session)
		assertJSONResponse(t, response, body, http.StatusBadRequest,
			`{"error":"invalid_fields","fields":["host","port","security","user_name","password"]}`)

		withName := encode(t, sourceBody(t, double))
		response, body = h.send(t, http.MethodPost, "/api/sources/check", bytes.NewReader(withName), session)
		assertJSONResponse(t, response, body, http.StatusBadRequest, `{"error":"invalid_fields","fields":[]}`)
	})

	t.Run("a failed sign-in", func(t *testing.T) {
		wrong := maps.Clone(mailbox)
		wrong["password"] = "MARKER-PW-wrong"
		response, body := h.send(t, http.MethodPost, "/api/sources/check", bytes.NewReader(encode(t, wrong)), session)
		assertJSONResponse(t, response, body, http.StatusUnprocessableEntity,
			`{"error":"check_failed","category":"sign_in_rejected","reply":"[AUTHENTICATIONFAILED] Authentication failed"}`)
	})

	id := h.addOverHTTP(t, session, sourceBody(t, double))
	path := "/api/sources/" + strconv.FormatInt(id, 10) + "/check"

	t.Run("a stored source", func(t *testing.T) {
		for _, request := range []string{`{}`, `{"password":""}`, `{"security":"tls","password":"MARKER-PW-4f1c"}`} {
			response, body := h.send(t, http.MethodPost, path, strings.NewReader(request), session)
			assertJSONResponse(t, response, body, http.StatusOK, folders)
		}
	})

	t.Run("a stored source with a wrong password", func(t *testing.T) {
		response, body := h.send(t, http.MethodPost, path, strings.NewReader(`{"password":"MARKER-PW-wrong"}`), session)
		assertJSONResponse(t, response, body, http.StatusUnprocessableEntity,
			`{"error":"check_failed","category":"sign_in_rejected","reply":"[AUTHENTICATIONFAILED] Authentication failed"}`)
	})

	t.Run("a stored source and the input rules", func(t *testing.T) {
		for request, want := range map[string]string{
			`{"port":0,"password":7}`:      `{"error":"invalid_fields","fields":["port","password"]}`,
			`{"host":"imap.example.test"}`: `{"error":"invalid_fields","fields":[]}`,
			`not JSON`:                     `{"error":"invalid_fields","fields":[]}`,
		} {
			response, body := h.send(t, http.MethodPost, path, strings.NewReader(request), session)
			assertJSONResponse(t, response, body, http.StatusBadRequest, want)
		}
	})

	t.Run("an unknown source", func(t *testing.T) {
		for _, id := range []string{"999", "abc"} {
			response, body := h.send(t, http.MethodPost, "/api/sources/"+id+"/check", strings.NewReader(`{}`), session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
		}
	})
}

// Contract: a second source for the same host and user name, in any case,
// answers 409 source_exists with the existing source's ID, from the check
// and from POST /api/sources, before connecting.
func TestDuplicateSourceRefused(t *testing.T) {
	double := newMailbox(t)
	h, session := started(t, double)
	id := h.addOverHTTP(t, session, sourceBody(t, double))
	logins := double.Logins()
	want := fmt.Sprintf(`{"error":"source_exists","source_id":%d}`, id)

	duplicate := sourceBody(t, double)
	duplicate["user_name"] = strings.ToUpper(mailboxUser)
	response, body := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, duplicate)), session)
	assertJSONResponse(t, response, body, http.StatusConflict, want)

	delete(duplicate, "name")
	delete(duplicate, "folders")
	response, body = h.send(t, http.MethodPost, "/api/sources/check", bytes.NewReader(encode(t, duplicate)), session)
	assertJSONResponse(t, response, body, http.StatusConflict, want)

	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands for the duplicates, want 0", n)
	}
	if n := h.sourceIDs(t); n != 1 {
		t.Errorf("%d sources, want 1", n)
	}
}

// Contract: PATCH /api/sources/{id} changes the name, port, security,
// password, and folders, signing in first when port, security, or password
// change. It refuses host and user name, and a failed sign-in changes
// nothing.
func TestEditSourceRoute(t *testing.T) {
	double := newMailbox(t)
	if err := double.AddFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	h, session := started(t, double)
	body := sourceBody(t, double)
	port := body["port"].(int)
	id := h.addOverHTTP(t, session, body)
	path := "/api/sources/" + strconv.FormatInt(id, 10)
	finished := fmt.Sprintf(`{"at":%q,"archived":0}`, testTime)
	waiting := entryJSON(id, port, "waiting", "null", finished, nextSync, 0)
	renamed := strings.Replace(waiting, `"name":"Anna"`, `"name":"Anna privat"`, 1)

	patch := func(t *testing.T, request string) (*http.Response, string) {
		t.Helper()
		return h.send(t, http.MethodPatch, path, strings.NewReader(request), session)
	}

	t.Run("a rename", func(t *testing.T) {
		logins := double.Logins()
		response, answer := patch(t, `{"name":"Anna privat"}`)
		assertJSONResponse(t, response, answer, http.StatusOK, detailJSON(renamed, "INBOX", "Archiv"))
		if n := double.Logins() - logins; n != 0 {
			t.Errorf("%d LOGIN commands for a rename, want 0", n)
		}
	})

	t.Run("host and user name", func(t *testing.T) {
		response, answer := patch(t, `{"name":"Neu","host":"imap.example.test","user_name":"ben@example.test"}`)
		assertJSONResponse(t, response, answer, http.StatusBadRequest, `{"error":"invalid_fields","fields":["host","user_name"]}`)
	})

	t.Run("input rules", func(t *testing.T) {
		for request, want := range map[string]string{
			`{"name":""}`:                  `["name"]`,
			`{"port":"993"}`:               `["port"]`,
			`{"security":"ssl"}`:           `["security"]`,
			`{"password":7}`:               `["password"]`,
			`{"folders":[]}`:               `["folders"]`,
			`{"name":7,"folders":"INBOX"}`: `["name","folders"]`,
			`{"color":"blue"}`:             `[]`,
			`[]`:                           `[]`,
		} {
			response, answer := patch(t, request)
			assertJSONResponse(t, response, answer, http.StatusBadRequest, `{"error":"invalid_fields","fields":`+want+`}`)
		}
	})

	t.Run("a failed sign-in changes nothing", func(t *testing.T) {
		response, answer := patch(t, `{"name":"Neu","password":"MARKER-PW-wrong"}`)
		assertJSONResponse(t, response, answer, http.StatusUnprocessableEntity,
			`{"error":"check_failed","category":"sign_in_rejected","reply":"[AUTHENTICATIONFAILED] Authentication failed"}`)
		response, answer = h.do(t, http.MethodGet, path, session)
		assertJSONResponse(t, response, answer, http.StatusOK, detailJSON(renamed, "INBOX", "Archiv"))
	})

	t.Run("an empty password keeps the stored one", func(t *testing.T) {
		logins := double.Logins()
		response, answer := patch(t, `{"password":""}`)
		assertJSONResponse(t, response, answer, http.StatusOK, detailJSON(renamed, "INBOX", "Archiv"))
		if n := double.Logins() - logins; n != 0 {
			t.Errorf("%d LOGIN commands, want 0", n)
		}
	})

	t.Run("a removed folder", func(t *testing.T) {
		response, answer := patch(t, `{"folders":["INBOX"]}`)
		assertJSONResponse(t, response, answer, http.StatusOK, detailJSON(renamed, "INBOX"))
	})

	t.Run("a saved password syncs at once", func(t *testing.T) {
		logins := double.Logins()
		response, answer := patch(t, `{"password":"MARKER-PW-4f1c","folders":["INBOX","Archiv"]}`)
		if response.StatusCode != http.StatusOK || !strings.Contains(answer, `"state":"syncing"`) {
			t.Fatalf("PATCH = %d %s, want 200 and syncing", response.StatusCode, answer)
		}
		h.waitUntilWaiting(t, session, id)
		if n := double.Logins() - logins; n != 2 {
			t.Errorf("%d LOGIN commands, want 2: the check and the sync", n)
		}
	})

	t.Run("an unknown source", func(t *testing.T) {
		for _, id := range []string{"999", "abc"} {
			response, answer := h.send(t, http.MethodPatch, "/api/sources/"+id, strings.NewReader(`{"name":"Neu"}`), session)
			assertJSONResponse(t, response, answer, http.StatusNotFound, `{"error":"not_found"}`)
		}
	})
}

// Contract: pause, resume, and sync answer the source as in the list.
// Sync is refused while paused or syncing.
func TestPauseResumeSyncRoutes(t *testing.T) {
	double := newMailbox(t)
	if err := double.AddFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	h, session := started(t, double)
	body := sourceBody(t, double)
	port := body["port"].(int)
	id := h.addOverHTTP(t, session, body)
	path := "/api/sources/" + strconv.FormatInt(id, 10)
	finished := fmt.Sprintf(`{"at":%q,"archived":0}`, testTime)
	paused := strings.Replace(entryJSON(id, port, "paused", "null", finished, "null", 0),
		`"paused_at":null`, `"paused_at":`+quote(testTime), 1)
	post := func(t *testing.T, action string) (*http.Response, string) {
		t.Helper()
		return h.do(t, http.MethodPost, path+"/"+action, session)
	}

	response, answer := post(t, "pause")
	assertJSONResponse(t, response, answer, http.StatusOK, paused)
	response, answer = post(t, "pause")
	assertJSONResponse(t, response, answer, http.StatusOK, paused)
	response, answer = post(t, "sync")
	assertJSONResponse(t, response, answer, http.StatusConflict, `{"error":"paused"}`)

	response, answer = post(t, "resume")
	assertJSONResponse(t, response, answer, http.StatusOK,
		entryJSON(id, port, "syncing", `{"archived":0,"found":0}`, finished, "null", 0))
	h.waitUntilWaiting(t, session, id)
	response, answer = post(t, "resume")
	assertJSONResponse(t, response, answer, http.StatusOK, entryJSON(id, port, "waiting", "null", finished, nextSync, 0))

	uid, err := double.AddMessage("INBOX", mail("held"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	hold := double.HoldBody("INBOX", uid)
	response, answer = post(t, "sync")
	assertJSONResponse(t, response, answer, http.StatusOK,
		entryJSON(id, port, "syncing", `{"archived":0,"found":0}`, finished, "null", 0))
	<-hold.Reached()
	response, answer = post(t, "sync")
	assertJSONResponse(t, response, answer, http.StatusConflict, `{"error":"already_syncing"}`)
	hold.Release()
	h.waitUntilWaiting(t, session, id)

	for _, action := range []string{"pause", "resume", "sync"} {
		for _, id := range []string{"999", "abc"} {
			response, answer := h.do(t, http.MethodPost, "/api/sources/"+id+"/"+action, session)
			assertJSONResponse(t, response, answer, http.StatusNotFound, `{"error":"not_found"}`)
		}
	}
}

// Contract: without the key, sync and a PATCH that signs in answer 409
// no_key. A rename and a pause still work.
func TestManageWithoutKey(t *testing.T) {
	h := newHarnessWith(t, Routes(), harnessOptions{start: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	id := h.insertKeylessSource(t)
	path := "/api/sources/" + strconv.FormatInt(id, 10)

	response, body := h.do(t, http.MethodPost, path+"/sync", session)
	assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"no_key"}`)
	for _, request := range []string{`{"password":"MARKER-PW-4f1c"}`, `{"port":143}`, `{"security":"starttls"}`} {
		response, body = h.send(t, http.MethodPatch, path, strings.NewReader(request), session)
		assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"no_key"}`)
	}
	response, body = h.send(t, http.MethodPost, "/api/sources/check", strings.NewReader(
		`{"host":"imap.example.test","port":993,"security":"tls","user_name":"ben@example.test","password":"MARKER-PW-4f1c"}`), session)
	assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"no_key"}`)
	response, body = h.send(t, http.MethodPost, path+"/check", strings.NewReader(`{}`), session)
	assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"no_key"}`)

	response, body = h.send(t, http.MethodPatch, path, strings.NewReader(`{"name":"Anna privat"}`), session)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"Anna privat"`) {
		t.Errorf("rename without the key = %d %s, want 200", response.StatusCode, body)
	}
	response, body = h.do(t, http.MethodPost, path+"/pause", session)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `"state":"paused"`) {
		t.Errorf("pause without the key = %d %s, want 200 and paused", response.StatusCode, body)
	}
}

// Contract: DELETE /api/sources/{id} answers 204. The source is gone from
// the list and answers 404, and history entries still name it.
func TestRemoveSourceRoute(t *testing.T) {
	double := newMailbox(t)
	collected := mail("collected before removal")
	if _, err := double.AddMessage("INBOX", collected, testNow); err != nil {
		t.Fatal(err)
	}
	h, session := started(t, double)
	body := sourceBody(t, double)
	body["folders"] = []string{"INBOX"}
	id := h.addOverHTTP(t, session, body)
	path := "/api/sources/" + strconv.FormatInt(id, 10)

	response, answer := h.do(t, http.MethodDelete, path, session)
	if response.StatusCode != http.StatusNoContent || answer != "" {
		t.Fatalf("DELETE = %d %q, want 204 and no body", response.StatusCode, answer)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, path},
		{http.MethodDelete, path},
		{http.MethodPost, path + "/sync"},
		{http.MethodDelete, "/api/sources/abc"},
	} {
		response, answer := h.do(t, request.method, request.path, session)
		assertJSONResponse(t, response, answer, http.StatusNotFound, `{"error":"not_found"}`)
	}
	response, answer = h.do(t, http.MethodGet, "/api/sources", session)
	assertJSONResponse(t, response, answer, http.StatusOK, `{"collection":"available","sources":[]}`)

	response, answer = h.do(t, http.MethodGet, "/api/messages", session)
	source := fmt.Sprintf(`"source":{"id":%d,"name":"Anna","host":"127.0.0.1","user_name":%q,"removed":true}`, id, mailboxUser)
	if response.StatusCode != http.StatusOK || !strings.Contains(answer, source) {
		t.Errorf("GET /api/messages = %d %s, want the entry to name the removed source", response.StatusCode, answer)
	}

	// The same mailbox can be added again.
	response, answer = h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, body)), session)
	if response.StatusCode != http.StatusCreated {
		t.Errorf("POST /api/sources after removal = %d %s, want 201", response.StatusCode, answer)
	}
}
