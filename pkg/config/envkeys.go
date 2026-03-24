// Halfmoon - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Halfmoon contributors

package config

// Runtime environment variable keys for the halfmoon process.
// These control the location of files and binaries at runtime and are read
// directly via os.Getenv / os.LookupEnv. All halfmoon-specific keys use the
// HALFMOON_ prefix. Reference these constants instead of inline string
// literals to keep all supported knobs visible in one place and to prevent
// typos.
const (
	// EnvHome overrides the base directory for all halfmoon data
	// (config, workspace, skills, auth store, …).
	// Default: ~/.halfmoon
	EnvHome = "HALFMOON_HOME"

	// EnvConfig overrides the full path to the JSON config file.
	// Default: $HALFMOON_HOME/config.json
	EnvConfig = "HALFMOON_CONFIG"

	// EnvBuiltinSkills overrides the directory from which built-in
	// skills are loaded.
	// Default: <cwd>/skills
	EnvBuiltinSkills = "HALFMOON_BUILTIN_SKILLS"

	// EnvBinary overrides the path to the halfmoon executable.
	// Used by the web launcher when spawning the gateway subprocess.
	// Default: resolved from the same directory as the current executable.
	EnvBinary = "HALFMOON_BINARY"

	// EnvGatewayHost overrides the host address for the gateway server.
	// Default: "127.0.0.1"
	EnvGatewayHost = "HALFMOON_GATEWAY_HOST"
)
