package gemini

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

// geminiCodeAssistBaseURL is the Code Assist (cloudcode-pa) API base URL.
//
// It is a package-level var (not const) so tests can override it with an
// httptest server URL. It is kept self-contained in the gemini package so the
// adaptor does not need to reach into the service package for routing.
var geminiCodeAssistBaseURL = "https://cloudcode-pa.googleapis.com"

// isGeminiCodeAssist reports whether the runtime account uses the Google Code
// Assist endpoint. Everything in this file is a strict no-op when this is false,
// preserving the standard / API-key / AI-Studio Gemini path byte-for-byte.
func isGeminiCodeAssist(info *relaycommon.RelayInfo) bool {
	return info != nil && info.RuntimeGeminiOAuthType == service.AccountPoolGeminiOAuthTypeCodeAssist
}

// geminiCodeAssistRequest is the cloudcode-pa request wrapper. The standard
// Gemini GenerateContentRequest JSON is nested verbatim under "request"; the
// project id and upstream model name are top-level.
//
//	{"project":"<projectID>","model":"<upstreamModel>","request":<standard request>}
type geminiCodeAssistRequest struct {
	Project string          `json:"project"`
	Model   string          `json:"model"`
	Request json.RawMessage `json:"request"`
}

// geminiCodeAssistResponse is the cloudcode-pa response wrapper. The standard
// Gemini GenerateContentResponse is nested under "response"; sibling fields such
// as responseId / modelVersion are ignored (the inner response carries its own
// modelVersion that the existing handler already reads).
//
//	{"response":<standard response>,"responseId":"...","modelVersion":"..."}
type geminiCodeAssistResponse struct {
	Response json.RawMessage `json:"response"`
}

// wrapGeminiCodeAssistRequest wraps a standard Gemini request body in the
// cloudcode-pa envelope. requestBody is the marshaled standard request JSON; it
// is embedded verbatim under "request". Returns the wrapped JSON bytes.
func wrapGeminiCodeAssistRequest(requestBody []byte, projectID, model string) ([]byte, error) {
	wrapped := geminiCodeAssistRequest{
		Project: projectID,
		Model:   model,
		Request: json.RawMessage(requestBody),
	}
	return common.Marshal(wrapped)
}

// unwrapGeminiCodeAssistResponse extracts the inner standard response from a
// cloudcode-pa non-stream response body. If body does not parse as a wrapper or
// the inner "response" is absent/empty, it returns the original body unchanged
// (graceful fallback) so a valid-but-already-unwrapped response is not broken.
func unwrapGeminiCodeAssistResponse(body []byte) []byte {
	var wrapper geminiCodeAssistResponse
	if err := common.Unmarshal(body, &wrapper); err != nil {
		return body
	}
	inner := bytes.TrimSpace(wrapper.Response)
	if len(inner) == 0 || string(inner) == "null" {
		return body
	}
	return wrapper.Response
}

// geminiCodeAssistStreamReader is an io.ReadCloser that wraps an upstream SSE
// body and unwraps the cloudcode-pa envelope from each event line-by-line.
//
// For every `data: <json>` line where <json> parses to {"response":<inner>}, it
// emits `data: <inner>` (the SSE framing — the `data:` prefix and the trailing
// newline — is preserved). All other lines pass through verbatim: blank lines,
// non-data lines (e.g. event:/id:/comments), `data: [DONE]`, and any data line
// whose payload is not a wrapper (already unwrapped or non-JSON).
//
// Output is buffered in a bytes.Buffer drained across Read calls, so partial
// reads by the consumer (the stream scanner) work correctly.
type geminiCodeAssistStreamReader struct {
	src     io.ReadCloser
	scanner *bufio.Scanner
	buf     bytes.Buffer
	done    bool
}

func newGeminiCodeAssistStreamReader(src io.ReadCloser) *geminiCodeAssistStreamReader {
	scanner := bufio.NewScanner(src)
	// Allow large SSE events (default bufio limit is 64KiB).
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	scanner.Split(bufio.ScanLines)
	return &geminiCodeAssistStreamReader{
		src:     src,
		scanner: scanner,
	}
}

func (r *geminiCodeAssistStreamReader) Read(p []byte) (int, error) {
	// Drain any buffered output first.
	for r.buf.Len() == 0 && !r.done {
		if !r.scanner.Scan() {
			r.done = true
			if err := r.scanner.Err(); err != nil {
				return 0, err
			}
			break
		}
		line := r.scanner.Text()
		r.buf.WriteString(transformGeminiCodeAssistSSELine(line))
		// bufio.ScanLines strips the line terminator; re-add a newline so SSE
		// framing (line + blank-line separators) is preserved downstream.
		r.buf.WriteByte('\n')
	}

	if r.buf.Len() == 0 {
		return 0, io.EOF
	}
	return r.buf.Read(p)
}

func (r *geminiCodeAssistStreamReader) Close() error {
	if r.src != nil {
		return r.src.Close()
	}
	return nil
}

// transformGeminiCodeAssistSSELine unwraps a single SSE line. A `data:` line
// whose payload parses to {"response":<inner>} becomes `data: <inner>`; every
// other line (blank, non-data, [DONE], non-wrapper payload) is returned
// verbatim.
func transformGeminiCodeAssistSSELine(line string) string {
	const dataPrefix = "data:"
	if !strings.HasPrefix(line, dataPrefix) {
		return line
	}
	payload := strings.TrimSpace(line[len(dataPrefix):])
	if payload == "" || payload == "[DONE]" {
		return line
	}

	var wrapper geminiCodeAssistResponse
	if err := common.UnmarshalJsonStr(payload, &wrapper); err != nil {
		return line
	}
	inner := bytes.TrimSpace(wrapper.Response)
	if len(inner) == 0 || string(inner) == "null" {
		return line
	}
	return "data: " + string(inner)
}
