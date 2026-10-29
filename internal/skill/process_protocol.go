package skill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	ProcessProtocolVersion = 1
	MaxProcessRequestBytes = 1 << 20
)

type ProcessSource struct {
	Kind     string `json:"kind"`
	Root     string `json:"root,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type ProcessRequest struct {
	ProtocolVersion int           `json:"protocolVersion"`
	RequestID       string        `json:"requestId"`
	Operation       string        `json:"operation"`
	ProjectRoot     string        `json:"projectRoot,omitempty"`
	Agent           string        `json:"agent,omitempty"`
	SkillID         string        `json:"skillId,omitempty"`
	Source          ProcessSource `json:"source,omitempty"`
	PlanToken       string        `json:"planToken,omitempty"`
}

type ProcessError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ProcessCapabilities struct {
	Operations      []string `json:"operations"`
	Sources         []string `json:"sources"`
	ProjectScope    bool     `json:"projectScope"`
	LockPath        string   `json:"lockPath"`
	LockSchema      int      `json:"lockSchemaVersion"`
	LockKey         []string `json:"lockKey"`
	DigestAlgorithm string   `json:"digestAlgorithm"`
	Migration       string   `json:"migration"`
}

type ProcessResponse struct {
	ProtocolVersion int                  `json:"protocolVersion"`
	RequestID       string               `json:"requestId"`
	Status          string               `json:"status"`
	Capabilities    *ProcessCapabilities `json:"capabilities,omitempty"`
	Plan            *InstallPlan         `json:"plan,omitempty"`
	Decision        string               `json:"decision,omitempty"`
	Result          *InstallResult       `json:"result,omitempty"`
	Error           *ProcessError        `json:"error,omitempty"`
}

// DecodeProcessRequest accepts exactly one strict JSON document and rejects
// oversized, unknown-field, trailing-data, and unsupported-version requests.
func DecodeProcessRequest(r io.Reader) (ProcessRequest, error) {
	limited := io.LimitReader(r, MaxProcessRequestBytes+1)
	src, err := io.ReadAll(limited)
	if err != nil {
		return ProcessRequest{}, fmt.Errorf("read request: %w", err)
	}
	if len(src) > MaxProcessRequestBytes {
		return ProcessRequest{}, fmt.Errorf("request exceeds %d bytes", MaxProcessRequestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(src))
	decoder.DisallowUnknownFields()
	var request ProcessRequest
	if err := decoder.Decode(&request); err != nil {
		return ProcessRequest{}, fmt.Errorf("invalid JSON request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ProcessRequest{}, fmt.Errorf("request must contain exactly one JSON document")
		}
		return ProcessRequest{}, fmt.Errorf("trailing request data: %w", err)
	}
	if request.ProtocolVersion != ProcessProtocolVersion {
		return request, fmt.Errorf("unsupported protocolVersion %d; supported: %d", request.ProtocolVersion, ProcessProtocolVersion)
	}
	if request.RequestID == "" || len(request.RequestID) > 128 || strings.ContainsAny(request.RequestID, "\r\n\x00") {
		return request, fmt.Errorf("requestId is required and must be at most 128 safe characters")
	}
	return request, nil
}
