package fileserver

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Server represents a file server which responds to HTTP GET requests for files
type Server struct {
	// Network address this server must listen to in the form "host:port"
	addr string

	// TLS configuration for this server
	tlsConfig *tls.Config
}

const (
	_        = iota
	KB int64 = 1 << (10 * iota)
	MB
	GB
	TB
)

const (
	// Size of the buffer used to send the file contents to the client
	bufferSize = 1 * MB
)

var (
	contentsBuffer []byte
)

// init initializes the memory buffer used to send file contents
func init() {
	rand.Seed(time.Now().UnixNano())
	var b [bufferSize / 8]int64
	for i := range b {
		b[i] = rand.Int63()
	}
	bfr := new(bytes.Buffer)
	binary.Write(bfr, binary.LittleEndian, b)
	contentsBuffer = bfr.Bytes()
}

// NewServer creates a new file server. The server will listen for HTTPS
// requests on the addr address and will present to its client
// the X509 certificate and key located in files cert and key
// respectively.
// The server will only accept connections from clients with
// certificates issued by any of the certificate authorities in the
// ca file.
func NewServer(addr, cert, key, ca string) (*Server, error) {
	// Load this server's certificate
	absCert, err := filepath.Abs(cert)
	if err != nil {
		return nil, fmt.Errorf("invalid certificate file name '%s' [%s]", cert, err)
	}
	absKey, err := filepath.Abs(key)
	if err != nil {
		return nil, fmt.Errorf("invalid key file name '%s' [%s]", key, err)
	}
	serverCert, err := tls.LoadX509KeyPair(absCert, absKey)
	if err != nil {
		return nil, fmt.Errorf("error loading server certificate via tls.LoadX509KeyPair: %s", err)
	}

	// Build pool of certificates of the certificate authorities this server accepts clients from
	absCa, err := filepath.Abs(ca)
	if err != nil {
		return nil, fmt.Errorf("invalid certificate authorities file name '%s' [%s]", ca, err)
	}
	caCerts, err := ioutil.ReadFile(absCa)
	if err != nil {
		return nil, fmt.Errorf("error loading certificate authorities file %s: %s", absCa, err)
	}
	clientCAPool := x509.NewCertPool()
	if !clientCAPool.AppendCertsFromPEM(caCerts) {
		return nil, fmt.Errorf("error adding certificate authorities certificates to the pool: %s", err)
	}

	fs := &Server{
		// Network address this file server listens on
		addr: addr,

		// TLS configuration
		tlsConfig: &tls.Config{
			// This server's certificate chain
			Certificates: []tls.Certificate{serverCert},

			// Server policy for client authentication
			ClientAuth: tls.VerifyClientCertIfGiven, // tls.RequireAndVerifyClientCert,

			// Root certificate authorities used by this server to verify
			// client certificates
			ClientCAs: clientCAPool,

			// Minimum TLS version that is acceptable
			MinVersion: tls.VersionTLS12,

			// Prefer this server cipher suites, as opposed to the client's
			PreferServerCipherSuites: true,

			// List of supported cipher suites
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				// tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305, // Go 1.8 only
				// tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,   // Go 1.8 only
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},

			// Elliptic curves that will be used in an ECDHE handshake.
			// Use only those which have assembly implementation
			CurvePreferences: []tls.CurveID{
				tls.CurveP256,
				// tls.X25519, // Go 1.8 only
			},
		},
	}
	return fs, nil
}

// NewPlainServer creates a new file server. The server will listen for HTTP
// requests on the addr address.
func NewPlainServer(addr string) (*Server, error) {
        fs := &Server{
                // Network address this file server listens on
                addr: addr,
        }
        return fs, nil
}

// Serve listens for new incoming HTTP requests and serves them
func (fs *Server) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/file", handleGetFile)
	mux.HandleFunc("/", http.NotFound)
	srv := &http.Server{
		Addr:      fs.addr,
		Handler:   mux,
		TLSConfig: fs.tlsConfig,
		// ReadTimeout:  60 * time.Second,  // TODO: what these values should be?
		// WriteTimeout: 60 * time.Second,
		// IdleTimeout: 120 * time.Second, // Go v1.8 onwards
	}
	return srv.ListenAndServeTLS("", "")
}

// Serve listens for new incoming HTTP requests and serves them
func (fs *Server) PlainServe() error {
        mux := http.NewServeMux()
        mux.HandleFunc("/file", handleGetFile)
        mux.HandleFunc("/", http.NotFound)
        srv := &http.Server{
                Addr:      fs.addr,
                Handler:   mux,
                // ReadTimeout:  60 * time.Second,  // TODO: what these values should be?
                // WriteTimeout: 60 * time.Second,
                // IdleTimeout: 120 * time.Second, // Go v1.8 onwards
        }
        return srv.ListenAndServe("", "")
}

// handleGetFile handles GET requests for files. The form of the
// URL path must be /file?id=<fileid>&size=<file size in bytes>
func handleGetFile(w http.ResponseWriter, req *http.Request) {
	// Log this request
	start := time.Now()
	log.Printf("%s %s %s %s\n", req.RemoteAddr, req.Proto, req.Method, req.RequestURI)

	// Ensure method GET
	// TODO: HTTP HEAD should also be supported
	if req.Method != http.MethodGet {
		http.Error(w, "405 Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the components of the URL query
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		// The request URL does not contain a query string
		http.Error(w, "400 Bad request: no query in URL", http.StatusBadRequest)
		return
	}

	id, ok := query["id"]
	if !ok || len(id) != 1 {
		// File id not provided in the request or more than one is provided
		http.Error(w, "400 Bad request: no id in query", http.StatusBadRequest)
		return
	}
	fileID := id[0]

	sz, ok := query["size"]
	if !ok || len(sz) != 1 {
		// File size not provided in the request or more than one size provided
		http.Error(w, "400 Bad request: no file size in query", http.StatusBadRequest)
		return
	}
	size, err := parseSize(sz[0])
	if err != nil {
		// Could not parse the provided size
		httpErrorf(w, http.StatusBadRequest, "400 Bad request: invalid size value %q", sz[0])
		return
	}

	checksumAlg := ""
	checksumQry, ok := query["checksum"]
	if ok && len(checksumQry) != 1 {
		// Client requested multiple checksums
		http.Error(w, "400 Bad request: invalid requested checksum", http.StatusBadRequest)
		return
	}
	if ok {
		checksumAlg = checksumQry[0]
		if !isChecksumAvailable(checksumAlg) {
			httpErrorf(w, http.StatusBadRequest, "400 Bad request: invalid requested checksum %q", checksumAlg)
			return
		}
	}

	// Retrieve client's certificate, if any
	isClientAnonymous := len(req.TLS.PeerCertificates) == 0
	if isClientAnonymous {
		// Ensure that an anonymous client can access the requested file
		if !isAuthorized(fileID, size, "", "") {
			http.Error(w, "403 Forbidden: you are not authorized to retrieve the requested file", http.StatusForbidden)
			return
		}
	} else {
		// Retrieve the client's certificate and make sure it is authorized
		// to retrieve the requested file
		for _, cert := range req.TLS.PeerCertificates {
			issuer, subject := getCertName(cert.Issuer), getCertName(cert.Subject)
			if !isAuthorized(fileID, size, subject, issuer) {
				http.Error(w, "403 Forbidden: you are not authorized to retrieve the requested file", http.StatusForbidden)
				return
			}
		}
	}

	// Serve file contents
	status, err := serveFile(w, fileID, size, checksumAlg)
	if err != nil {
		log.Printf("Error serveFile: %s\n", err)
	}

	log.Printf("%s %s %s %s %d %s\n", req.RemoteAddr, req.Proto, req.Method, req.RequestURI, status, time.Now().Sub(start))
}

// serveFile sends the response to a GET HTTP request. The body of the response contains
// the (made up) contents of the requested file.
// checksumAlg is the name of the hash algorithm requested by the client (e.g. "sha256").
// If checksumAlg is the empty string, no checksum is computed.
func serveFile(w http.ResponseWriter, fileid string, size int64, checksumAlg string) (int, error) {
	var hasher hash.Hash
	if checksumAlg != "" {
		var err error
		hasher, err = getChecksumByName(checksumAlg)
		if err != nil {
			// Should not happen because the caller checked that the specified checksum algorithm
			// is supported
			s := fmt.Sprintf("%q is not a supported checksum algorithm", checksumAlg)
			http.Error(w, s, http.StatusBadRequest)
			return http.StatusBadRequest, fmt.Errorf(s)
		}
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Trailer", "X-Content-Length")
	if hasher != nil {
		w.Header().Set("X-Checksum-Algorithm", checksumAlg)
		w.Header().Add("Trailer", "X-Checksum-Value")
	}

	rdr := bytes.NewReader(contentsBuffer)
	var src io.Reader = rdr
	if hasher != nil {
		// We need to compute checksum of the reponse body
		src = io.TeeReader(rdr, hasher)
	}
	var err error
	for remain, sent := size, int64(0); remain > 0; remain -= sent {
		if rdr.Len() == 0 {
			rdr.Seek(0, 0)
		}
		count := min(int64(rdr.Len()), remain)
		sent, err = io.CopyN(w, src, count)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return http.StatusInternalServerError, fmt.Errorf("io.Copy error count=%d %s", count, err)
		}
	}

	// Send the content length and the checksum trailers
	w.Header().Set("X-Content-Length", strconv.FormatInt(size, 10))
	if hasher != nil {
		w.Header().Set("X-Checksum-Value", hex.EncodeToString(hasher.Sum(nil)))
	}
	return http.StatusOK, nil
}

// parseSize parses a string representing the file size and returns the value
// in bytes. The argument string can have the following suffixes representing
// the unit:
//    <None>: the size is interpreted as bytes
//         K: kilo (i.e. 1024 bytes)
//         M: mega (i.e. 1024*1024 bytes)
//         G: giga (i.e. 1024*1024*1024 bytes)
func parseSize(sz string) (int64, error) {
	if len(sz) == 0 {
		return 0, fmt.Errorf("empty size is not valid")
	}
	factor := int64(1)
	if strings.HasSuffix(sz, "K") {
		factor = KB
		sz = sz[:len(sz)-1]
	} else if strings.HasSuffix(sz, "M") {
		factor = MB
		sz = sz[:len(sz)-1]
	} else if strings.HasSuffix(sz, "G") {
		factor = GB
		sz = sz[:len(sz)-1]
	}
	res, err := strconv.ParseInt(sz, 10, 64)
	if err != nil {
		return 0, err
	}
	res *= factor

	if res <= 0 || res > TB {
		return 0, fmt.Errorf("invalid file size %d", res)
	}
	return res, nil
}

// getCertName retrieves and formats the given distinguished name of a certificate
// Returns a string of the form:
//    '/C=XX/ST=Province/L=Locality/O=Organizationy/OU=Organiational Unit/CN=Common Name'
// Only the fields present on the given distinguished names are included in the
// returned string.
func getCertName(name pkix.Name) string {
	format := func(a []string, sep string) string {
		result := ""
		if len(a) > 0 {
			sep = fmt.Sprintf("/%s=", sep)
			return sep + strings.Join(a, sep)
		}
		return result
	}
	result := format(name.Country, "C") +
		format(name.Province, "ST") +
		format(name.Locality, "L") +
		format(name.Organization, "O") +
		format(name.OrganizationalUnit, "OU") +
		format([]string{name.CommonName}, "CN")
	return result
}

func min(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

// isAuthorized verfifies that the given user is authorized to download the
// given file. The user is identified by their certificate subject and the
// subject of the issuer. If no certificate is given, they user is considered anonymous.
// This function is a place holder.
// TODO: implement an authorization manager
func isAuthorized(fileID string, size int64, userName string, issuerName string) bool {
	if len(userName) == 0 || len(issuerName) == 0 {
		// Anonymous user
		return true
	}
	// TODO: verify that user whose certificate subject is userName issued by
	// issuerName is authorized to download the file identified by fileID with
	// size size
	return true
}

// httpErrorf replies to the HTTP request with the specified HTTP code and error
// message.
// Based on the implementation of http.Error() [https://golang.org/pkg/net/http/#Error]
func httpErrorf(w http.ResponseWriter, code int, format string, v ...interface{}) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	fmt.Fprintf(w, format, v...)
	return
}
