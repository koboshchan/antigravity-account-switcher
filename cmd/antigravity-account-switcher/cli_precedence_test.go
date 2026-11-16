package main

// TestCLI_PrecedenceMatrix verifies the complete 4-tier precedence hierarchy:
// Tier 1 (Lowest): Built-in Defaults
// Tier 2: ~/.config/antigravity-account-switcher/config.json
// Tier 3: Environment Variables (ANTIGRAVITY_*)
// Tier 4 (Highest): CLI Flags (--fallback-secondary, --model-primary, --model-secondary)

// TestCLI_CLIFlag_BooleanPermutations tests all standard Go boolean flag formats

// TestCLI_DefaultBehaviorPreservation verifies that when fallback is disabled,
// existing systems and legacy setups behave exactly as before.

// TestCLI_SubprocessBinaryExecution runs the actual compiled binary from bin/antigravity-account-switcher
// to test CLI flag and environment variable precedence in a real process environment.

// TestCLI_AdversarialEdgeCases tests edge cases, whitespace handling, and flag delimiters
