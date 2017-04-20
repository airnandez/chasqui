package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/airnandez/chasqui/fileserver"
)

const (
	defaultClientCA   = "ca.pem"
	defaultClientCert = ""
	defaultClientKey  = ""
)

type clientConfig struct {
	// Command line options
	help bool
	addr string
	ca   string
	cert string
	key  string
}

func clientCmd() command {
	fset := flag.NewFlagSet("chasqui client", flag.ExitOnError)
	config := clientConfig{}

	fset.BoolVar(&config.help, "help", false, "")
	fset.StringVar(&config.addr, "addr", defaultClientAddr, "")
	fset.StringVar(&config.ca, "ca", defaultClientCA, "")
	fset.StringVar(&config.cert, "cert", defaultClientCert, "")
	fset.StringVar(&config.key, "key", defaultClientKey, "")
	run := func(args []string) error {
		fset.Usage = func() { clientUsage(args[0], os.Stderr) }
		fset.Parse(args[1:])
		return clientRun(args[0], config)
	}
	return command{fset: fset, run: run}
}

func clientRun(cmdName string, config clientConfig) error {
	if config.help {
		clientUsage(cmdName, os.Stderr)
		return nil
	}
	errlog = setErrlog("client")
	debug(1, "running client with:")
	debug(1, "   addr='%s'\n", config.addr)
	debug(1, "   ca='%s'\n", config.ca)
	debug(1, "   cert='%s'\n", config.cert)
	debug(1, "   key='%s'\n", config.key)

	// Process requests
	return clientHandleRequests(config)
}

func clientHandleRequests(config clientConfig) error {
	http.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		clientLoadRequestHandler(config, w, r)
	})
	http.HandleFunc("/stop", clientStopRequestHandler)
	return http.ListenAndServe(config.addr, nil) // TODO: should be HTTPS
}

func clientLoadRequestHandler(config clientConfig, w http.ResponseWriter, r *http.Request) {
	// Ensure method is POST
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Ensure HTTP request body is not empty
	if r.Body == nil {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}
	// Decode the request contained in the body
	var payload LoadRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	debug(1, "received request %v", payload)

	// Verify the incoming request is valid
	if err := clientVerifyLoadRequest(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Execute the requested operation
	resp, err := clientProcessLoadRequest(config, &payload)
	if err != nil {
		debug(1, "error processing load request: %s", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Serialize and send the response
	debug(1, "sending response %v", resp)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "application/json")
	io.Copy(w, &buf)
}

func clientVerifyLoadRequest(req *LoadRequest) error {
	if len(req.ServerAddrs) == 0 {
		return fmt.Errorf("server addresses not included in load request")
	}
	if req.Duration < 0 {
		return fmt.Errorf("invalid duration %s", req.Duration)
	}
	return nil
}

func clientProcessLoadRequest(config clientConfig, req *LoadRequest) (*LoadResponse, error) {
	// Create channels to send requests to the workers and receive
	// responses from them. We start as many workers as specified
	// in the request. If nothing was specified in the request,
	// create twice as many workers as there are CPU cores in this
	// computer.
	numWorkers := req.Concurrency
	if numWorkers <= 0 {
		numWorkers = 2 * runtime.NumCPU()
	}
	numWorkers = minInt(numWorkers, 1000*runtime.NumCPU())
	requests := make(chan *DownloadReq, numWorkers)
	responses := make(chan *DownloadResp, numWorkers)

	// Prepare the fileserver clients for serving this load request
	fsclients := make([]*fileserver.Client, len(req.ServerAddrs))
	for i := range req.ServerAddrs {
		c, err := fileserver.NewClient(req.UseHttp1, config.cert, config.key, config.ca)
		if err != nil {
			return nil, fmt.Errorf("could not initialize fileserver client [%s]", err)
		}
		fsclients[i] = c
	}

	// Start collecting responses from workers
	summary := make(chan *LoadResponse)
	go clientCollectResponses(numWorkers, responses, summary)

	// Start the workers
	debug(1, "starting %d workers", numWorkers)
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go clientWorker(i, &wg, requests)
	}

	// Start emitting requests
	go clientEmitRequests(config, req, fsclients, requests, responses)

	// Wait for workers to finish their execution
	wg.Wait()
	debug(1, "all workers finished execution")
	close(responses)

	// Close connections to servers
	for i := range fsclients {
		fsclients[i].CloseIdleConnections()
	}

	// Receive summary of worker responses
	finalResp := <-summary
	close(summary)
	return finalResp, nil
}

// clientEmitRequests emits file download requests against the file servers. The emitted requests are
// executed by workers
func clientEmitRequests(config clientConfig, req *LoadRequest, fsclients []*fileserver.Client, requests chan *DownloadReq, responses chan *DownloadResp) {
	timeout := time.After(req.Duration)
	numServers := len(req.ServerAddrs)
	seqNumber := uint64(0)
	notAfter := time.Now().Add(req.Duration)
loop:
	for {
		seqNumber += 1
		s := rand.Intn(numServers)
		newreq := &DownloadReq{
			seqNumber: seqNumber,
			server:    req.ServerAddrs[s],
			fsclient:  fsclients[s],
			fileID:    fmt.Sprintf("file-%d", seqNumber),
			size:      uint64(req.MeanSize) + uint64(rand.NormFloat64()*float64(req.StdSize)),
			notAfter:  notAfter,
			replyTo:   responses,
		}
		select {
		case <-timeout:
			// Stop generating requests
			break loop
		case requests <- newreq:
		}
	}
	// Inform the workers no more requests will be emitted
	close(requests)
	debug(1, "stopped emitting download requests")
}

func clientCollectResponses(numWorkers int, responses chan *DownloadResp, summary chan *LoadResponse) {
	totalSize := float64(0) // MB
	fileCount, errCount := uint64(0), uint64(0)
	start := time.Now()
	for resp := range responses {
		if resp.err != nil {
			errCount += 1
			debug(1, "error from worker: seqNumber=%d %s\n", resp.seqNumber, resp.err)
			continue
		}
		fileCount += 1
		totalSize += float64(resp.size) / float64(MB)
	}
	summary <- &LoadResponse{
		Start:       start,
		End:         time.Now(),
		Concurrency: numWorkers,
		NumFiles:    fileCount,
		DataSize:    totalSize,
		Rate:        float64(totalSize) / time.Since(start).Seconds(),
		ErrCount:    errCount,
	}
}

type LoadRequest struct {
	// Network addresses of the servers involved in this test
	ServerAddrs []string

	// Duration of this test
	Duration time.Duration

	// Number of concurrent download operations
	Concurrency int

	// Use HTTP1 for download operations. By default use HTTP2
	UseHttp1 bool

	// Mean and std of the file size to request to the servers (bytes)
	MeanSize uint64
	StdSize  uint64
}

type LoadResponse struct {
	// Start and end times
	Start time.Time
	End   time.Time

	// Number of concurrent download operations
	Concurrency int

	// Number of files downloaded in this test
	NumFiles uint64

	// Volume of data downloaded data in this test (in MB)
	DataSize float64

	// Download rate for this test: MB/sec
	Rate float64

	// Number of errors observed in this test
	ErrCount uint64
}

func clientStopRequestHandler(w http.ResponseWriter, r *http.Request) {
	// Ensure method is POST
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// TODO: stop execution: this is not an elegant way of stopping
	os.Exit(0)
}

//  masterUsage prints the usage information about the 'master' subcommand
func clientUsage(cmd string, f *os.File) {
	const clientTempl = `
USAGE:
{{.Tab1}}{{.AppName}} {{.SubCmd}} [-addr=<network address>] [-ca=<file>] [-cert=<file>]
{{.Tab1}}{{.AppNameFiller}} {{.SubCmdFiller}} [-key=<file>]
{{.Tab1}}{{.AppName}} {{.SubCmd}} -help

DESCRIPTION:
{{.Tab1}}'{{.AppName}} {{.SubCmd}}' starts a process to download data from the file servers.
{{.Tab1}}A client waits for instructions from the driver process and emits download
{{.Tab1}}requests against the file servers. It sends back to the driver process a
{{.Tab1}}report on the execution of each request.

OPTIONS:
{{.Tab1}}-addr=<network address>
{{.Tab2}}network address this client process listens to for receiving
{{.Tab2}}instructions from the driver process. The form of the address is
{{.Tab2}}'host:port'.
{{.Tab2}}Default: {{.DefaultClientAddr}}

{{.Tab1}}-ca=<file>
{{.Tab2}}path of the PEM-formatted file which contains the certificates of the
{{.Tab2}}certification authorities this client process trusts. The client process
{{.Tab2}}requires the file server to present a certificate and verifies that
{{.Tab2}}certificate is issued by one of the trusted certification authorities
{{.Tab2}}included in this file.
{{.Tab2}}Default: "{{.DefaultClientCA}}"

{{.Tab1}}-cert=<file>
{{.Tab2}}path of the PEM-formatted file which contains the certificate this
{{.Tab2}}client process uses for identifying itself to the file server.
{{.Tab2}}Default: "{{.DefaultClientCert}}"

{{.Tab1}}-key=<file>
{{.Tab2}}path of the PEM-formatted file which contains the private key of
{{.Tab2}}the certificate specified with the '-cert' option.
{{.Tab2}}Default: "{{.DefaultClientKey}}"

{{.Tab1}}-help
{{.Tab2}}print this help
`
	tmplFields["SubCmd"] = cmd
	tmplFields["SubCmdFiller"] = strings.Repeat(" ", len(cmd))
	tmplFields["DefaultClientAddr"] = defaultClientAddr
	tmplFields["DefaultClientCA"] = defaultClientCA
	tmplFields["DefaultClientCert"] = defaultClientCert
	tmplFields["DefaultClientKey"] = defaultClientKey
	render(clientTempl, tmplFields, f)
}
