package fileserver

import (
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"testing"
)

const (
	serverAddr = "localhost:5678"
)

var (
	fs *Server
)

// 	TODO:
//	*	Programmatically generate client and server certificates

func setupServer(addr, cert, key, ca string, t *testing.T) *Server {
	if fs != nil {
		return fs
	}
	var err error
	if fs, err = NewServer(serverAddr, cert, key, ca); err != nil {
		t.Fatalf("failed creating a new Fileserver: %s", err)
	}
	go fs.Serve()
	return fs
}

type TestServerCase struct {
	method   string
	path     string
	expected int
}

func certPath(name string) string {
	return path.Join("..", "certs", name)
}

func TestServer(t *testing.T) {
	fsrv := setupServer(serverAddr, certPath("localhost.pem"), certPath("localhost.key"), certPath("ca.pem"), t)

	// Setup a client
	client, err := NewClient(false, certPath("chasqui_client.pem"), certPath("chasqui_client.key"), certPath("ca.pem"))
	if err != nil {
		t.Fatalf("failed creating new client %s", err)
	}

	// Send HTTP requests
	tests := []TestServerCase{
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodHead, "/", http.StatusNotFound},
		{http.MethodPost, "/", http.StatusNotFound},
		{http.MethodPut, "/", http.StatusNotFound},
		{http.MethodDelete, "/", http.StatusNotFound},
		{http.MethodOptions, "/", http.StatusNotFound},
		{http.MethodTrace, "/", http.StatusNotFound},

		{http.MethodHead, "/file", http.StatusMethodNotAllowed}, // TODO: HEAD should be supported
		{http.MethodPost, "/file", http.StatusMethodNotAllowed},
		{http.MethodPut, "/file", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/file", http.StatusMethodNotAllowed},
		{http.MethodOptions, "/file", http.StatusMethodNotAllowed},
		{http.MethodTrace, "/file", http.StatusMethodNotAllowed},

		{http.MethodGet, "/file", http.StatusBadRequest},
		{http.MethodGet, "/file?id=myfileid&size=", http.StatusBadRequest},
		{http.MethodGet, "/file?id=myfileid&size=1234&size=7890", http.StatusBadRequest},
		{http.MethodGet, "/file?id=myfileid&size=1234&checksum=xxxx", http.StatusBadRequest},
		{http.MethodGet, "/file?id=myfileid&size=1234&checksum=xxxx&checksum=yyyy", http.StatusBadRequest},
	}

	urlPrefix := "https://" + fsrv.addr
	for i, c := range tests {
		u := urlPrefix + c.path
		req, err := http.NewRequest(c.method, u, nil)
		if err != nil {
			t.Fatalf("failed creating new %s request to URL %s: %s [test #%d]", c.method, u, err, i)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s failed: %s [test #%d]", c.method, u, err, i)
		}

		// Check HTTP status code
		if resp.StatusCode != c.expected {
			t.Fatalf("%s %s expecting HTTP status %d got %d [test #%d]", c.method, u, c.expected, resp.StatusCode, i)
		}

		// Consume the response body
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}
}

type DownloadTestCase struct {
	fileID     string
	size       int
	mode       ChecksumMode
	algorithm  ChecksumAlgorithm
	shouldFail bool
}

var tests = []DownloadTestCase{
	// Check invalid checksum algorithm
	{"test1", 1000, ChecksumNone, ChecksumAlgorithm(345), false},
	{"test2", 1000, ChecksumClientOnly, ChecksumAlgorithm(345), true},
	{"test3", 1000, ChecksumServerOnly, ChecksumAlgorithm(345), true},
	{"test4", 1000, ChecksumClientAndServer, ChecksumAlgorithm(345), true},

	// Check valid checksum algorithm
	{"test5", 945710, ChecksumNone, ChecksumAlgorithm(SHA256), false},
	{"test6", 945710, ChecksumClientOnly, ChecksumAlgorithm(SHA256), false},
	{"test7", 945710, ChecksumServerOnly, ChecksumAlgorithm(SHA256), false},
	{"test8", 945710, ChecksumClientAndServer, ChecksumAlgorithm(SHA256), false},

	{"test9", 945710, ChecksumNone, ChecksumAlgorithm(SHA512), false},
	{"test10", 945710, ChecksumClientOnly, ChecksumAlgorithm(SHA512), false},
	{"test11", 945710, ChecksumServerOnly, ChecksumAlgorithm(SHA512), false},
	{"test12", 945710, ChecksumClientAndServer, ChecksumAlgorithm(SHA512), false},
}

func TestAnonymousDownload(t *testing.T) {
	// Setup server
	fsrv := setupServer(serverAddr, certPath("localhost.pem"), certPath("localhost.key"), certPath("ca.pem"), t)

	// Create anonymous client
	client, err := NewClient(false, "", "", certPath("ca.pem"))
	if err != nil {
		t.Fatalf("failed creating new client %s", err)
	}

	doDownloads(client, fsrv.addr, tests, t)
}

func TestIdentifiedDownload(t *testing.T) {
	// Setup server
	fsrv := setupServer(serverAddr, certPath("localhost.pem"), certPath("localhost.key"), certPath("ca.pem"), t)

	// Create client
	client, err := NewClient(false, certPath("chasqui_client.pem"), certPath("chasqui_client.key"), certPath("ca.pem"))
	if err != nil {
		t.Fatalf("failed creating new client %s", err)
	}

	doDownloads(client, fsrv.addr, tests, t)
}

func doDownloads(client *Client, addr string, tests []DownloadTestCase, t *testing.T) {
	for _, c := range tests {
		report := client.DownloadFile(addr, c.fileID, c.size, c.mode, c.algorithm, ioutil.Discard)
		if c.shouldFail && report.Err == nil {
			t.Fatalf("error expected error downloading file %q", c.fileID)
		} else if !c.shouldFail && report.Err != nil {
			t.Fatalf("unexpected error downloading file %q: %s", c.fileID, report.Err)
		}
	}
}
