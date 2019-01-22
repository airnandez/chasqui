package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultDuration     time.Duration = time.Duration(10) * time.Second
	defaultMeanFileSize int           = 100 // MB
	defaultStdFileSize  float64       = 0.2 // [0..1]
)

type driverConfig struct {
	// Command line options
	help        bool
	clients     string
	servers     string
	concurrency int
	duration    time.Duration
	http1       bool
	meanSize    int
	stdSize     float64
	plainHttp   bool
}

func driverCmd() command {
	config := driverConfig{
		meanSize: defaultMeanFileSize,
		stdSize:  defaultStdFileSize,
	}
	fset := flag.NewFlagSet("chasqui driver", flag.ExitOnError)
	fset.StringVar(&config.clients, "clients", defaultClientAddr, "")
	fset.StringVar(&config.servers, "servers", defaultServerAddr, "")
	fset.DurationVar(&config.duration, "duration", defaultDuration, "")
	fset.IntVar(&config.meanSize, "size", defaultMeanFileSize, "")
	fset.IntVar(&config.concurrency, "concurrency", 0, "")
	fset.BoolVar(&config.http1, "http1", false, "")
	fset.BoolVar(&config.help, "help", false, "")
	fset.BoolVar(&config.plainHttp, "plain-http", false, "")
	run := func(args []string) error {
		fset.Usage = func() { driverUsage(args[0], os.Stderr) }
		fset.Parse(args[1:])
		return driverRun(args[0], config)
	}
	return command{fset: fset, run: run}
}

func driverRun(cmdName string, config driverConfig) error {
	if config.help {
		driverUsage(cmdName, os.Stderr)
		return nil
	}
	if config.duration < 0 {
		config.duration *= -1
	}
	errlog = setErrlog(cmdName)
	debug(1, "running driver:")
	debug(1, "   clients='%s'\n", config.clients)
	debug(1, "   servers='%s'\n", config.servers)
	debug(1, "   duration='%s'\n", config.duration)
	debug(1, "   concurrency=%d\n", config.concurrency)
	debug(1, "   meanSize=%d MB\n", config.meanSize)
	debug(1, "   http1=%t\n", config.http1)
	debug(1, "   plainHttp='%s'\n", config.plainHttp)

	// Prepare collector of execution reports
	clientAddrs := splitAndClean(config.clients)
	reports := make(chan *LoadReport)
	var collectGroup sync.WaitGroup
	collectGroup.Add(1)
	go driverCollectLoadReports(len(clientAddrs), reports, &collectGroup)

	// Send the same load request to each client processes
	meanSize := uint64(config.meanSize) * uint64(MB)
	loadReq := &LoadRequest{
		ServerAddrs: splitAndClean(config.servers),
		Concurrency: config.concurrency,
		Duration:    config.duration,
		MeanSize:    meanSize,
		StdSize:     uint64(config.stdSize * float64(meanSize)),
		UseHttp1:    config.http1,
		PlainHttp:   config.plainHttp,
	}
	var sendGroup sync.WaitGroup
	for _, cli := range clientAddrs {
		sendGroup.Add(1)
		go driverSendLoadRequest(cli, reports, &sendGroup, loadReq)
	}
	sendGroup.Wait()
	debug(1, "finished sending requests to clients")

	// Wait for the report collector to finish
	collectGroup.Wait()

	return nil
}

func driverSendLoadRequest(clientAddr string, reports chan<- *LoadReport, wg *sync.WaitGroup, loadReq *LoadRequest) {
	defer wg.Done()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(loadReq)

	// Prepare this execution report
	rep := LoadReport{
		client: clientAddr,
	}
	defer func() {
		reports <- &rep
	}()

	// Send the JSON-encoded HTTP request
	u := url.URL{
		Scheme: "http", // TODO: should be https
		Host:   clientAddr,
		Path:   "load",
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), &buf)
	if err != nil {
		debug(1, "error creating HTTP request for URL '%s' %s", u.String(), err)
		return
	}
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rep.err = fmt.Errorf("could not submit load request to client '%s': %s", clientAddr, err)
		return
	}

	// Deserialize the response
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()
	var loadResp LoadResponse
	if err := json.NewDecoder(resp.Body).Decode(&loadResp); err != nil {
		rep.err = fmt.Errorf("could not deserialize response to load request from client '%s': %s", clientAddr, err)
		return
	}
	rep.req = loadReq
	rep.resp = &loadResp
}

type LoadReport struct {
	client string
	req    *LoadRequest
	resp   *LoadResponse
	err    error
}

func driverCollectLoadReports(n int, reports chan *LoadReport, wg *sync.WaitGroup) {
	results := map[string]*LoadReport{}
	defer wg.Done()
	for i := 0; i < n; i++ {
		rep := <-reports
		results[rep.client] = rep
		if rep.err != nil {
			debug(1, "received error from client %s %#v: ", rep.client, rep.err)
		} else {
			fmt.Printf("%s: download report\n", appName)
			fmt.Printf("\tclient:           '%s'\n", rep.client)
			fmt.Printf("\tconcurrency:      %d\n", rep.resp.Concurrency)
			fmt.Printf("\telapsed time:     %s\n", rep.resp.End.Sub(rep.resp.Start))
			fmt.Printf("\tfiles downloaded: %d\n", rep.resp.NumFiles)
			fmt.Printf("\tdata volume:      %.2f MB\n", rep.resp.DataSize)
			fmt.Printf("\tdownload rate:    %.2f MB/sec\n", rep.resp.Rate)
			fmt.Printf("\terrors:           %d\n", rep.resp.ErrCount)
			// debug(1, "received response from client %s %#v: ", rep.client, rep.resp)
		}
	}
	close(reports)
	printSummary(results)
}

// printSummary prints a summary of the client reports
func printSummary(results map[string]*LoadReport) {
	var (
		start     = time.Now().Add(3000 * time.Hour)
		end       = time.Now().Add(-3000 * time.Hour)
		dataSize  float64
		numFiles  uint64
		numErrors int
	)
	for _, rep := range results {
		if rep.err != nil {
			numErrors += 1
			fmt.Printf("   ERROR %s\n", rep.err)
			continue
		}
		if rep.resp.Start.Before(start) {
			start = rep.resp.Start
		}
		if rep.resp.End.After(end) {
			end = rep.resp.End
		}
		dataSize += rep.resp.DataSize
		numFiles += rep.resp.NumFiles
	}
	rate := dataSize / end.Sub(start).Seconds()
	fmt.Printf("Summary:\n")
	fmt.Printf("   download operations: %d\n", numFiles)
	fmt.Printf("   data volume:         %.2f MB\n", dataSize)
	fmt.Printf("   avg file size:       %.2f MB\n", float64(dataSize)/float64(numFiles))
	fmt.Printf("   download rate:       %.2f MB/sec\n", rate)
	if numErrors > 0 {
		fmt.Printf("   download errors:       %d\n", numErrors)
	}
}

//  driverUsage prints the usage information about the 'driver' subcommand
func driverUsage(cmd string, f *os.File) {
	const driverTempl = `
USAGE:
{{.Tab1}}{{.AppName}} {{.SubCmd}} [-clients=<network addresses>] [-servers=<network addresses>]
{{.Tab1}}{{.AppNameFiller}} {{.SubCmdFiller}} [-duration=duration] [-concurrency=integer] [-http1]
{{.Tab1}}{{.AppName}} {{.SubCmd}} -help

DESCRIPTION:
{{.Tab1}}Use '{{.AppName}} driver' to start a driver process for performing a test.
{{.Tab1}}campaign. A driver process generates a set of file download requests and
{{.Tab1}}submits them for asychronous execution by the client processes. The driver
{{.Tab1}}process collects and summarizes the results of execution of the requests
{{.Tab1}}composing a test campaign.

OPTIONS:
{{.Tab1}}-clients=<network addresses>
{{.Tab2}}list comma-separated network addresses of the client processes. The
{{.Tab2}}form of each address is 'host:port'.
{{.Tab2}}Default: {{.DefaultClientAddr}}

{{.Tab1}}-servers=<network addresses>
{{.Tab2}}list of comma-separated network addresses of file servers involved
{{.Tab2}}in the test. The form of each address is 'host:port'.
{{.Tab2}}Default: {{.DefaultServerAddr}}

{{.Tab1}}-duration=duration
{{.Tab2}}specifies the duration of the test campaign. Examples of valid values
{{.Tab2}}for this option are '60s', '1h30m', '120s', '2h', etc.
{{.Tab2}}Default: {{.DefaultDuration}}

{{.Tab1}}-size=<average file size>
{{.Tab2}}specifies the average file size in MegaBytes this test campaign
{{.Tab2}}uses for each download operation. The actual size of files downloaded
{{.Tab2}}is a normal distribution around this mean file size and a standard
{{.Tab2}}deviation of {{.DefaultStdSize}}
{{.Tab2}}Default: {{.DefaultMeanSize}}

{{.Tab1}}-concurrency=integer
{{.Tab2}}specifies the number of concurrent download requests that each client
{{.Tab2}}process will execute against the servers specified in the -servers
{{.Tab2}}option. Valid accepted values are in the range of 1 to 1000 times the
{{.Tab2}}number of CPU cores in each host running the client process.
{{.Tab2}}Default: twice the number of CPU cores in each host running the client
{{.Tab2}}process.

{{.Tab1}}-http1
{{.Tab2}}specifies that the protocol to be used for downloading files from the
{{.Tab2}}server is HTTP1.1 instead ofthe default HTTP/2.

{{.Tab1}}-plain-http=<file>
{{.Tab2}}uses plain HTTP without TLS.
{{.Tab2}}Default: false

{{.Tab1}}-help
{{.Tab2}}print this help

EXAMPLES:
{{.Tab1}}Use the command

{{.Tab2}}{{.AppName}} {{.SubCmd}} -clients=localhost:5000 \
{{.Tab2}}{{.AppNameFiller}} {{.SubCmdFiller}} -servers=hostA.example.com:443,hostB.example.com:8443

{{.Tab1}}to start a driver process to submit file download requests
{{.Tab1}}to be executed by the client in 'localhost:5000' against file servers
{{.Tab1}}in 'hostA.example.com' and 'hostB.example.com'. Those file servers
{{.Tab1}}listen for download requests to the ports 443 and 8443 respectively.


ADDITIONAL INFORMATION:
{{.Tab1}}Use '{{.AppName}} client -help' to get more information on how to
{{.Tab1}}start clients and '{{.AppName}} server -help' to know how to
{{.Tab1}}start servers.
`
	tmplFields["SubCmd"] = cmd
	tmplFields["SubCmdFiller"] = strings.Repeat(" ", len(cmd))
	tmplFields["DefaultClientAddr"] = defaultClientAddr
	tmplFields["DefaultServerAddr"] = defaultServerAddr
	tmplFields["DefaultDuration"] = defaultDuration.String()
	tmplFields["DefaultMeanSize"] = fmt.Sprintf("%d", defaultMeanFileSize)
	tmplFields["DefaultStdSize"] = fmt.Sprintf("%.1f", defaultStdFileSize)
	render(driverTempl, tmplFields, f)
}
