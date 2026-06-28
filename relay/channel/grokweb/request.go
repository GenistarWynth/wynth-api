// Best-effort reverse-engineered grok.com web request construction.
// See package doc in constants.go for the fragility warning.
package grokweb

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// grokChatRequest is the JSON body POSTed to grok.com's
// /rest/app-chat/conversations/new endpoint.
//
// Field set mirrors grok2api xai_chat.py build_chat_payload. grok.com takes a
// SINGLE flattened `message` string, not an OpenAI messages[] array.
type grokChatRequest struct {
	Message                     string            `json:"message"`
	ModeID                      string            `json:"modeId"`
	CollectionIDs               []string          `json:"collectionIds"`
	Connectors                  []string          `json:"connectors"`
	FileAttachments             []string          `json:"fileAttachments"`
	ImageAttachments            []string          `json:"imageAttachments"`
	DeviceEnvInfo               grokDeviceEnvInfo `json:"deviceEnvInfo"`
	ToolOverrides               grokToolOverrides `json:"toolOverrides"`
	ResponseMetadata            map[string]any    `json:"responseMetadata"`
	DisableMemory               bool              `json:"disableMemory"`
	DisableSearch               bool              `json:"disableSearch"`
	DisableSelfHarmShortCircuit bool              `json:"disableSelfHarmShortCircuit"`
	DisableTextFollowUps        bool              `json:"disableTextFollowUps"`
	EnableImageGeneration       bool              `json:"enableImageGeneration"`
	EnableImageStreaming        bool              `json:"enableImageStreaming"`
	EnableSideBySide            bool              `json:"enableSideBySide"`
	ForceConcise                bool              `json:"forceConcise"`
	ForceSideBySide             bool              `json:"forceSideBySide"`
	ImageGenerationCount        int               `json:"imageGenerationCount"`
	IsAsyncChat                 bool              `json:"isAsyncChat"`
	ReturnImageBytes            bool              `json:"returnImageBytes"`
	ReturnRawGrokInXaiRequest   bool              `json:"returnRawGrokInXaiRequest"`
	SearchAllConnectors         bool              `json:"searchAllConnectors"`
	SendFinalMetadata           bool              `json:"sendFinalMetadata"`
	Temporary                   bool              `json:"temporary"`
}

type grokDeviceEnvInfo struct {
	DarkModeEnabled  bool `json:"darkModeEnabled"`
	DevicePixelRatio int  `json:"devicePixelRatio"`
	ScreenHeight     int  `json:"screenHeight"`
	ScreenWidth      int  `json:"screenWidth"`
	ViewportHeight   int  `json:"viewportHeight"`
	ViewportWidth    int  `json:"viewportWidth"`
}

type grokToolOverrides struct {
	GmailSearch           bool `json:"gmailSearch"`
	GoogleCalendarSearch  bool `json:"googleCalendarSearch"`
	OutlookSearch         bool `json:"outlookSearch"`
	OutlookCalendarSearch bool `json:"outlookCalendarSearch"`
	GoogleDriveSearch     bool `json:"googleDriveSearch"`
}

// buildGrokRequest converts an OpenAI chat request into a grok web chat request
// for the given resolved modeId.
func buildGrokRequest(request *dto.GeneralOpenAIRequest, modeID string) *grokChatRequest {
	return &grokChatRequest{
		Message:          flattenMessages(request.Messages),
		ModeID:           modeID,
		CollectionIDs:    []string{},
		Connectors:       []string{},
		FileAttachments:  []string{},
		ImageAttachments: []string{},
		DeviceEnvInfo: grokDeviceEnvInfo{
			DarkModeEnabled:  false,
			DevicePixelRatio: 2,
			ScreenHeight:     1329,
			ScreenWidth:      2056,
			ViewportHeight:   1083,
			ViewportWidth:    2056,
		},
		ToolOverrides:               grokToolOverrides{}, // all false
		ResponseMetadata:            map[string]any{},
		DisableMemory:               true,
		DisableSearch:               false,
		DisableSelfHarmShortCircuit: false,
		DisableTextFollowUps:        false,
		EnableImageGeneration:       false,
		EnableImageStreaming:        false,
		EnableSideBySide:            false,
		ForceConcise:                false,
		ForceSideBySide:             false,
		ImageGenerationCount:        0,
		IsAsyncChat:                 false,
		ReturnImageBytes:            false,
		ReturnRawGrokInXaiRequest:   false,
		SearchAllConnectors:         false,
		SendFinalMetadata:           true,
		Temporary:                   true,
	}
}

// flattenMessages collapses an OpenAI messages[] array into the single prompt
// string grok.com expects.
//
// grok2api flattens history into role-prefixed lines. We mirror that: each
// message becomes "<Role>: <content>" on its own block, joined by blank lines,
// preserving system + history + user ordering. A trailing newline is omitted.
func flattenMessages(messages []dto.Message) string {
	var b strings.Builder
	first := true
	for i := range messages {
		content := strings.TrimRight(messages[i].StringContent(), "\n")
		if content == "" {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		first = false
		b.WriteString(rolePrefix(messages[i].Role))
		b.WriteString(content)
	}
	return b.String()
}

// rolePrefix maps an OpenAI role to a human-readable line prefix.
func rolePrefix(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return "System: "
	case "assistant":
		return "Assistant: "
	case "tool":
		return "Tool: "
	case "user":
		return "Human: "
	case "":
		return ""
	default:
		// Capitalize unknown roles for readability.
		return capitalize(role) + ": "
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
