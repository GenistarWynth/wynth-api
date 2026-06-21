package common

import (
	"strings"

	common2 "github.com/QuantumNous/new-api/common"
)

type ActualResponseModelKind string

const (
	ActualResponseModelKindOpenAIChat      ActualResponseModelKind = "openai_chat"
	ActualResponseModelKindOpenAIResponses ActualResponseModelKind = "openai_responses"
	ActualResponseModelKindAnthropic       ActualResponseModelKind = "anthropic"
	ActualResponseModelKindGemini          ActualResponseModelKind = "gemini"
)

type ActualResponseModelSource string

const (
	ActualResponseModelSourceOpenAIChat         ActualResponseModelSource = "openai_chat"
	ActualResponseModelSourceOpenAIResponses    ActualResponseModelSource = "openai_responses"
	ActualResponseModelSourceAnthropicMessage   ActualResponseModelSource = "anthropic_message"
	ActualResponseModelSourceGeminiModelVersion ActualResponseModelSource = "gemini_model_version"
)

type ActualResponseModelAudit struct {
	Model  string
	Source ActualResponseModelSource
}

func DetectActualResponseModel(kind ActualResponseModelKind, payload []byte) ActualResponseModelAudit {
	if len(payload) == 0 {
		return ActualResponseModelAudit{}
	}

	switch kind {
	case ActualResponseModelKindOpenAIChat:
		var response struct {
			Model string `json:"model"`
		}
		if common2.Unmarshal(payload, &response) != nil {
			return ActualResponseModelAudit{}
		}
		return newActualResponseModelAudit(response.Model, ActualResponseModelSourceOpenAIChat)
	case ActualResponseModelKindOpenAIResponses:
		var response struct {
			Model    string `json:"model"`
			Response struct {
				Model string `json:"model"`
			} `json:"response"`
		}
		if common2.Unmarshal(payload, &response) != nil {
			return ActualResponseModelAudit{}
		}
		model := response.Response.Model
		if strings.TrimSpace(model) == "" {
			model = response.Model
		}
		return newActualResponseModelAudit(model, ActualResponseModelSourceOpenAIResponses)
	case ActualResponseModelKindAnthropic:
		var response struct {
			Type    string `json:"type"`
			Model   string `json:"model"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if common2.Unmarshal(payload, &response) != nil {
			return ActualResponseModelAudit{}
		}
		if response.Type == "message_start" {
			return newActualResponseModelAudit(response.Message.Model, ActualResponseModelSourceAnthropicMessage)
		}
		return newActualResponseModelAudit(response.Model, ActualResponseModelSourceAnthropicMessage)
	case ActualResponseModelKindGemini:
		var response struct {
			ModelVersion      string `json:"modelVersion"`
			SnakeModelVersion string `json:"model_version"`
		}
		if common2.Unmarshal(payload, &response) != nil {
			return ActualResponseModelAudit{}
		}
		model := response.ModelVersion
		if strings.TrimSpace(model) == "" {
			model = response.SnakeModelVersion
		}
		return newActualResponseModelAudit(model, ActualResponseModelSourceGeminiModelVersion)
	default:
		return ActualResponseModelAudit{}
	}
}

func (info *RelayInfo) SetActualResponseModel(model string, source ActualResponseModelSource) bool {
	audit := newActualResponseModelAudit(model, source)
	if audit.Model == "" {
		return false
	}
	info.ActualResponseModel = audit.Model
	info.ActualResponseModelSource = audit.Source
	return true
}

func (info *RelayInfo) ApplyActualResponseModelAudit(kind ActualResponseModelKind, payload []byte) bool {
	audit := DetectActualResponseModel(kind, payload)
	return info.SetActualResponseModel(audit.Model, audit.Source)
}

func newActualResponseModelAudit(model string, source ActualResponseModelSource) ActualResponseModelAudit {
	model = strings.TrimSpace(model)
	if model == "" || source == "" {
		return ActualResponseModelAudit{}
	}
	return ActualResponseModelAudit{
		Model:  model,
		Source: source,
	}
}
