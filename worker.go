package main

import (
	"io/ioutil"
	"sync"
	"time"

	"github.com/airnandez/chasqui/fileserver"
)

// DownloadReq is a HTTP download operation sent to a client worker for execution
type DownloadReq struct {
	seqNumber uint64
	server    string
	fsclient  *fileserver.Client
	fileID    string
	size      uint64
	notAfter  time.Time
	replyTo   chan<- *DownloadResp
}

// DownloadResp is the report sent back by a worker after performing a download operation
// against the file server
type DownloadResp struct {
	seqNumber uint64
	start     time.Time
	end       time.Time
	size      uint64
	err       error
}

// clientWorker is the goroutine executed by each client worker. It receives incoming
// download requests, performs the requested operation and sends the result back
// via the channel specified in the request
func clientWorker(workerId int, wg *sync.WaitGroup, reqChan <-chan *DownloadReq) {
	defer wg.Done()
	for req := range reqChan {
		if time.Now().After(req.notAfter) {
			continue
		}
		debug(1, "worker %d: processing download [seqNo:%d server:%s size:%d]", workerId, req.seqNumber, req.server, req.size)
		req.replyTo <- processDownloadRequest(req)
		debug(1, "worker %d seqNo:%d ended", workerId, req.seqNumber)
	}
}

// processDownloadRequest perform a single file download against the server
// specified in the argument request
func processDownloadRequest(req *DownloadReq) *DownloadResp {
	report := req.fsclient.DownloadFile(req.server, req.fileID, int(req.size), fileserver.ChecksumNone, fileserver.SHA256, ioutil.Discard)
	return &DownloadResp{
		seqNumber: req.seqNumber,
		start:     report.Start,
		end:       report.End,
		size:      req.size,
		err:       report.Err,
	}
}
