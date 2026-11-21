package imagetest

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func startHost(t *testing.T) (*Host, *http.Client) {
	t.Helper()
	host, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	})
	// The certificate is made at start, so nothing trusts it.
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	return host, client
}

func get(t *testing.T, client *http.Client, url string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

func TestHostServesImagesAndRecords(t *testing.T) {
	host, client := startHost(t)
	base := "https://" + host.Addr()

	response, body := get(t, client, base+"/img/logo.png", map[string]string{
		"Referer": "http://localhost:8080/preview", "Cookie": "a=b", "Sec-Fetch-Dest": "image",
	})
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("/img/logo.png = %d %s, want 200 image/png", response.StatusCode, response.Header.Get("Content-Type"))
	}
	picture, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the image isn't a PNG: %v", err)
	}
	if size := picture.Bounds().Size(); size.X != 1 || size.Y != 1 {
		t.Errorf("the image is %v, want 1×1", size)
	}

	if response, _ := get(t, client, base+"/style.css", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("/style.css = %d, want 404", response.StatusCode)
	}

	want := []Request{
		{Method: "GET", Path: "/img/logo.png", Referer: "http://localhost:8080/preview", Cookie: true, SecFetchDest: "image"},
		{Method: "GET", Path: "/style.css"},
	}
	if got := host.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("recorded %+v, want %+v", got, want)
	}

	response, body = get(t, client, base+LogPath, nil)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("%s = %d %s, want 200 JSON", LogPath, response.StatusCode, response.Header.Get("Content-Type"))
	}
	var served []map[string]any
	if err := json.Unmarshal(body, &served); err != nil {
		t.Fatalf("the log isn't JSON: %v", err)
	}
	wantServed := []map[string]any{
		{"method": "GET", "path": "/img/logo.png", "referer": "http://localhost:8080/preview", "cookie": true, "sec_fetch_dest": "image"},
		{"method": "GET", "path": "/style.css", "referer": "", "cookie": false, "sec_fetch_dest": ""},
	}
	if !reflect.DeepEqual(served, wantServed) {
		t.Errorf("the log serves %v, want %v", served, wantServed)
	}
	if got := len(host.Requests()); got != 2 {
		t.Errorf("reading the log recorded it: %d requests, want 2", got)
	}
}

// A host that has seen nothing serves an empty list, so a reader can tell it
// from a broken log.
func TestHostLogStartsEmpty(t *testing.T) {
	host, client := startHost(t)
	response, body := get(t, client, "https://"+host.Addr()+LogPath, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s = %d, want 200", LogPath, response.StatusCode)
	}
	if got := string(bytes.TrimSpace(body)); got != "[]" {
		t.Errorf("the empty log serves %s, want []", got)
	}
}

// Browsers reach the host by either name, so the certificate names both.
func TestHostCertificate(t *testing.T) {
	host, _ := startHost(t)
	connection, err := tls.Dial("tcp", host.Addr(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	certificate := connection.ConnectionState().PeerCertificates[0]
	for _, name := range []string{"localhost", "127.0.0.1"} {
		if err := certificate.VerifyHostname(name); err != nil {
			t.Errorf("the certificate doesn't name %s: %v", name, err)
		}
	}
}
