// Package imagetest is an HTTPS image host that stands in for the internet's
// image hosts in the browser tests. It serves a 1×1 PNG at any /img/ path and
// records every request, so a test can see what a preview asked for and what
// each request carried. It imports no module.
package imagetest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LogPath serves the record of requests as JSON. Requests to it aren't
// recorded.
const LogPath = "/__log"

// ImagePrefix is where the host serves images.
const ImagePrefix = "/img/"

// Pixel is the 1×1 transparent PNG the host serves.
var Pixel = pixel()

func pixel() []byte {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		panic(err)
	}
	return out.Bytes()
}

// Request is what the host noted about one request.
type Request struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Referer is "" when the request carried none.
	Referer string `json:"referer"`
	Cookie  bool   `json:"cookie"`
	// SecFetchDest is "" when the request carried none.
	SecFetchDest string `json:"sec_fetch_dest"`
}

// Host is a running image host.
type Host struct {
	server   *http.Server
	listener net.Listener

	mu       sync.Mutex
	requests []Request
}

// Start serves HTTPS on 127.0.0.1, on a port the system picks, with a
// certificate made now for localhost and 127.0.0.1. Browsers don't trust the
// certificate, so tests ignore HTTPS errors.
func Start() (*Host, error) {
	certificate, err := selfSigned(time.Now())
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	h := &Host{listener: listener}
	h.server = &http.Server{
		Handler:           http.HandlerFunc(h.serve),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
	}
	go func() { _ = h.server.ServeTLS(listener, "", "") }()
	return h, nil
}

// Addr is the host and port, such as 127.0.0.1:41234.
func (h *Host) Addr() string { return h.listener.Addr().String() }

// Requests returns what the host has recorded, in the order requests came.
// It's never nil, so the log serves an empty list as [].
func (h *Host) Requests() []Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Request{}, h.requests...)
}

// Close stops the host.
func (h *Host) Close() error {
	if err := h.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (h *Host) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == LogPath {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.Requests())
		return
	}
	h.mu.Lock()
	h.requests = append(h.requests, Request{
		Method:       r.Method,
		Path:         r.URL.Path,
		Referer:      r.Header.Get("Referer"),
		Cookie:       r.Header.Get("Cookie") != "",
		SecFetchDest: r.Header.Get("Sec-Fetch-Dest"),
	})
	h.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, ImagePrefix) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(Pixel)
}

// selfSigned makes a certificate for localhost and 127.0.0.1 that is valid
// from an hour before now for a day.
func selfSigned(now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("make a key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("make a serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "imagetest"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("make a certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
