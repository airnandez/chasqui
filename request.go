package main

import (
	"encoding/json"
)

type DownloadRequest struct {
	RequestID  uint64 `json:"id"`
	ServerAddr string `json:"server"`
	FileSize   int64  `json:"size"`
}

func NewDownloadRequest(reqID uint64, server string, size int64) *DownloadRequest {
	return &DownloadRequest{
		RequestID:  reqID,
		ServerAddr: server,
		FileSize:   size,
	}
}

func (req *DownloadRequest) Serialize() ([]byte, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (req *DownloadRequest) Deserialize(blob []byte) error {
	err := json.Unmarshal(blob, req)
	if err != nil {
		return err
	}
	return nil
}

type DownloadResponse struct {
	RequestID  uint64 `json:"id"`
	ClientAddr string `json:"client"`
	ServerAddr string `json:"server"`
	FileSize   int64  `json:"size"`
	Seconds    int64  `json:"seconds"`
}

func NewDownloadResponse(reqID uint64, server, client string, size, seconds int64) *DownloadResponse {
	return &DownloadResponse{
		RequestID:  reqID,
		ClientAddr: client,
		ServerAddr: server,
		FileSize:   size,
		Seconds:    seconds,
	}
}

func (req *DownloadResponse) Serialize() ([]byte, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (resp *DownloadResponse) Deserialize(blob []byte) error {
	err := json.Unmarshal(blob, resp)
	if err != nil {
		return err
	}
	return nil
}
