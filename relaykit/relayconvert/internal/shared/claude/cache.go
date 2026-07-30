package claude

func NormalizeCacheCreationSplit(totalTokens int, tokens5m int, tokens1h int) (int, int) {
	if totalTokens < 0 {
		totalTokens = 0
	}
	if tokens5m < 0 {
		tokens5m = 0
	}
	if tokens1h < 0 {
		tokens1h = 0
	}
	knownTotal := tokens5m + tokens1h
	if knownTotal >= totalTokens {
		return tokens5m, tokens1h
	}
	return tokens5m + totalTokens - knownTotal, tokens1h
}
