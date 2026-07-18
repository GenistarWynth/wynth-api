package doubao

import "strings"

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
}

var ChannelName = "doubao-video"

type videoRatioKey struct {
	resolution string
	hasVideo   bool
}

// videoInputRatioMap stores provider compatibility multipliers relative to
// each model's low-resolution request without video input.
var videoInputRatioMap = map[string]map[videoRatioKey]float64{
	"doubao-seedance-2-0-260128": {
		{resolution: "low", hasVideo: false}:   1,
		{resolution: "low", hasVideo: true}:    28.0 / 46.0,
		{resolution: "1080p", hasVideo: false}: 51.0 / 46.0,
		{resolution: "1080p", hasVideo: true}:  31.0 / 46.0,
		{resolution: "4k", hasVideo: false}:    26.0 / 46.0,
		{resolution: "4k", hasVideo: true}:     16.0 / 46.0,
	},
	"doubao-seedance-2-0-fast-260128": {
		{resolution: "low", hasVideo: false}: 1,
		{resolution: "low", hasVideo: true}:  22.0 / 37.0,
	},
}

func GetVideoInputRatio(modelName, resolution string, hasVideo bool) (float64, bool) {
	ratios, ok := videoInputRatioMap[modelName]
	if !ok {
		return 0, false
	}

	resolutionTier := "low"
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "1080p":
		resolutionTier = "1080p"
	case "4k":
		resolutionTier = "4k"
	}

	ratio, ok := ratios[videoRatioKey{resolution: resolutionTier, hasVideo: hasVideo}]
	if !ok {
		// Unsupported combinations are left to the provider to reject.
		return 1, true
	}
	return ratio, true
}
