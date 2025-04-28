package common

type McpServerConfig struct {
	Transport string `json:"transport"` // stdio / sse
	// for stdio
	Cmd  string   `json:"cmd"` // for stdio
	Args []string `json:"args"`
	Env  []string `json:"env"`
	// for sse
	Url     string            `json:"url"` // for sse
	Headers map[string]string `json:"headers"`
}
