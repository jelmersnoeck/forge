package phase

// CheapModels returns a prioritized list of cheap/fast models for the given
// provider. Used for intent classification, PR generation, and other lightweight
// LLM tasks where cost and speed matter more than capability.
func CheapModels(providerName string) []string {
	switch providerName {
	case "openai":
		return []string{"gpt-4.1-mini", "gpt-4.1-nano"}
	default:
		// Anthropic, Claude CLI, and unknown providers use Haiku models.
		return []string{"claude-haiku-4-5-20251001", "claude-3-5-haiku-20241022"}
	}
}
