package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return json.Unmarshal(StringToByteSlice(data), v)
}

func DecodeJson(reader io.Reader, v any) error {
	return json.NewDecoder(reader).Decode(v)
}

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func GetJsonType(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "unknown"
	}
	firstChar := trimmed[0]
	switch firstChar {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] != '"' {
		return string(trimmed)
	}
	var value string
	if err := Unmarshal(trimmed, &value); err != nil {
		return string(trimmed)
	}
	return value
}

// FlexibleBool accepts standard JSON booleans plus legacy numeric/string flags.
type FlexibleBool bool

func (b *FlexibleBool) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*b = false
		return nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := Unmarshal(trimmed, &value); err != nil {
			return err
		}
		parsed, ok := parseFlexibleBool(value)
		if !ok {
			return fmt.Errorf("invalid boolean value %q", value)
		}
		*b = FlexibleBool(parsed)
		return nil
	}
	parsed, ok := parseFlexibleBool(string(trimmed))
	if !ok {
		return fmt.Errorf("invalid boolean value %s", string(trimmed))
	}
	*b = FlexibleBool(parsed)
	return nil
}

func parseFlexibleBool(value string) (bool, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "true":
		return true, true
	case "false", "":
		return false, true
	}
	number, err := strconv.ParseFloat(normalized, 64)
	if err == nil {
		return number != 0, true
	}
	return false, false
}
