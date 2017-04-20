package fileserver

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// Client is a client for interacting with a fileserver
type Client struct {
	http.Client
}

// NewClient creates a new client to interact with a fileserver.
// Set useHttp1 to true for this client to use HTTP1 instead of HTTP2, which is the default.
// cert and key are the filenames of the certificate and key files the client will use
// to identify itself with the server. ca is the file name of the certificate authorities' certificates the
// client will accept and use to authenticate the server
func NewClient(useHttp1 bool, cert, key, ca string) (*Client, error) {
	// Prepare client TLS configuration
	bothZero := len(cert) == 0 && len(key) == 0
	bothNonZero := len(cert) != 0 && len(key) != 0
	if !bothZero && !bothNonZero {
		return nil, fmt.Errorf("both cert and key files must be provided or both be zero length")
	}
	var clientCert tls.Certificate
	hasClientCert := false
	if len(cert) > 0 {
		absCert, err := filepath.Abs(cert)
		if err != nil {
			return nil, fmt.Errorf("invalid certificate file name '%s' [%s]", cert, err)
		}
		absKey, err := filepath.Abs(key)
		if err != nil {
			return nil, fmt.Errorf("invalid key file name '%s' [%s]", key, err)
		}
		clientCert, err = tls.LoadX509KeyPair(absCert, absKey)
		if err != nil {
			return nil, fmt.Errorf("error loading server certificate via tls.LoadX509KeyPair: %s", err)
		}
		hasClientCert = true
	}

	// Create a pool of CA certificates this client will use for checking the server's certificate
	absCa, err := filepath.Abs(ca)
	if err != nil {
		return nil, fmt.Errorf("invalid certificate authorities file name '%s' [%s]", ca, err)
	}
	caCerts, err := ioutil.ReadFile(absCa)
	if err != nil {
		return nil, fmt.Errorf("error loading certificate authorities file %s: %s", absCa, err)
	}
	serverCApool := x509.NewCertPool()
	if !serverCApool.AppendCertsFromPEM(caCerts) {
		return nil, fmt.Errorf("error adding certificate authorities certificates to the pool: %s", err)
	}

	// Build transport
	config := &tls.Config{
		RootCAs: serverCApool,
	}
	if hasClientCert {
		config.Certificates = []tls.Certificate{clientCert}
	}
	tr := &http.Transport{
		TLSClientConfig:     config,
		MaxIdleConnsPerHost: 100, // TODO: what would be a sensible value?
	}
	if !useHttp1 {
		http2.ConfigureTransport(tr) // Required: see issue https://github.com/golang/go/issues/17051
	}
	return &Client{http.Client{Transport: tr}}, nil
}

type DownloadReport struct {

	// Start and end times of the download operation
	Start time.Time
	End   time.Time

	// Time elapsed since the HTTP GET request is emitted until the client is ready for
	// receiving the first byte of the requested file
	TimeToFirstByte time.Duration

	// Checksum of the downloaded file, if the client has requested the server to compute it.
	// The string has the form:
	//    sha256:ABCDE14566
	Checksum string

	// Error, may be nil
	Err error
}

// DownloadFile emits a HTTP request against the specified server to download a file given its file identifier ans size (in bytes).
// chkMode and chkAlgo specify if the checksum is to be computed by the server, the client, both or none and what algorithm should
// be used to compute that checksum
func (c *Client) DownloadFile(serverAddr string, fileID string, size int, chkMode ChecksumMode, chkAlgo ChecksumAlgorithm, dst io.Writer) (report DownloadReport) {
	// Verify checksum
	algorithm := getChecksumName(chkAlgo)
	if chkMode != ChecksumNone && algorithm == "" {
		report.Err = fmt.Errorf("invalid requested checksum algorithm %v", chkAlgo)
		return
	}
	doRequestChecksum := chkMode == ChecksumServerOnly || chkMode == ChecksumClientAndServer

	u := &url.URL{
		Scheme: "https",
		Host:   serverAddr,
		Path:   "/file",
	}
	q := u.Query()
	q.Set("id", fileID)
	q.Set("size", fmt.Sprintf("%d", size))
	if doRequestChecksum {
		q.Set("checksum", algorithm)
	}
	u.RawQuery = q.Encode()
	req := &http.Request{
		Method: http.MethodGet,
		URL:    u,
	}
	report.Start = time.Now()
	resp, err := c.Do(req)
	report.TimeToFirstByte = time.Since(report.Start)
	if err != nil {
		report.Err = err
		return
	}

	// Consume remaining response body
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	// In case of HTTP status not OK, consume response body which may contain the error message
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		report.Err = fmt.Errorf("error downloading file: %q", string(body))
		return
	}

	// We are ready to receive the file contents. Do we need to compute the checksum
	// of the response's body?
	src := io.Reader(resp.Body)
	var chksumer hash.Hash
	if chkMode == ChecksumClientOnly {
		// Compute the checksum. Don't check against server's (if any)
		chksumer, _ = getChecksumByKey(chkAlgo)
		src = io.TeeReader(src, chksumer)
	} else if chkMode == ChecksumClientAndServer {
		// Compute the checksum and check against server's
		serverAlgo := strings.ToLower(resp.Header.Get("X-Checksum-Algorithm"))
		if algorithm != serverAlgo {
			report.Err = fmt.Errorf("unexpected server algorithm %q", serverAlgo)
			return
		}
		chksumer, _ = getChecksumByKey(chkAlgo)
		src = io.TeeReader(src, chksumer)
	}

	// Receive the response body
	received, err := io.Copy(dst, src)
	report.End = time.Now()
	clientCheckSum := ""
	if chksumer != nil {
		clientCheckSum = strings.ToLower(hex.EncodeToString(chksumer.Sum(nil)))
	}

	// Check that the value of 'X-Content-Length' trailer and actual length of response body match
	clength := resp.Trailer.Get("X-Content-Length")
	if len(clength) == 0 {
		report.Err = fmt.Errorf("missing 'X-Content-Length' trailer")
		return
	}
	bodyLength, _ := strconv.ParseUint(clength, 10, 64)
	if int64(bodyLength) != received {
		report.Err = fmt.Errorf("response body length %d does not match 'X-Content-Length' value %d", bodyLength, received)
		return
	}

	// Check the received checksum and the computed one actually match
	serverChecksum := ""
	if chkMode == ChecksumClientAndServer {
		serverChecksum = strings.ToLower(resp.Trailer.Get("X-Checksum-Value"))
		if len(serverChecksum) == 0 {
			report.Err = fmt.Errorf("missing 'X-Checksum-Value' trailer")
			return
		}
		if clientCheckSum != serverChecksum {
			report.Err = fmt.Errorf("computed checksum (%s) and received checksum (%s) do not match", clientCheckSum, serverChecksum)
			return
		}
	}

	// Add the checksum to the download report
	if chkMode != ChecksumNone {
		if serverChecksum != "" {
			report.Checksum = fmt.Sprintf("%s:%s", algorithm, serverChecksum)
		} else {
			report.Checksum = fmt.Sprintf("%s:%s", algorithm, clientCheckSum)
		}
	}
	return
}

// CloseIdleConnections closes idle TCP connections in use by this client
func (c *Client) CloseIdleConnections() {
	c.Client.Transport.(*http.Transport).CloseIdleConnections()
}

type ChecksumAlgorithm uint

const (
	NONE ChecksumAlgorithm = iota
	SHA256
	SHA512
)

type ChecksumMode int

const (
	// Don't compute data checksum
	ChecksumNone ChecksumMode = iota

	// Compute data checksum only at the client while receiving
	// the data
	ChecksumClientOnly

	// Compute data checksum at the server while sending the data
	ChecksumServerOnly

	// Compute both at the client and at the server
	ChecksumClientAndServer
)

type checksumSpec struct {
	name     string
	hashFunc func() hash.Hash
}

var (
	// Map of supported checksum algorithms
	checksumMap = map[ChecksumAlgorithm]checksumSpec{
		SHA256: {"sha256", sha256.New},
		SHA512: {"sha512", sha512.New},
	}
)

// getChecksumByName returns a hash function associated to the given name, if any.
// An error is returned if there is no function associated to that name
func getChecksumByName(name string) (hash.Hash, error) {
	name = strings.ToLower(name)
	for _, v := range checksumMap {
		if v.name == name {
			return v.hashFunc(), nil
		}
	}
	return nil, fmt.Errorf("unkown checksum algorithm %q", name)
}

// getChecksumByKey returns a hash function associated to the given algorithm key, if any.
// An error is returned if there is no function associated to that key
func getChecksumByKey(key ChecksumAlgorithm) (hash.Hash, error) {
	if v, ok := checksumMap[key]; ok {
		return v.hashFunc(), nil
	}
	return nil, fmt.Errorf("invalid checksum algorithm %q", key)
}

// getChecksumName returns the name of the checksum algorithm
func getChecksumName(algo ChecksumAlgorithm) string {
	if s, ok := checksumMap[algo]; ok {
		return s.name
	}
	return ""
}

// isChecksumAvailable returns true if the provided name is associated to a checksum
// algorithm
func isChecksumAvailable(name string) bool {
	_, err := getChecksumByName(name)
	return err == nil
}
