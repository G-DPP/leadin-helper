package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	// MCPConfigName is the MCP configuration of a run, with the token.
	MCPConfigName = "mcp.json"
)

// WriteMCPConfig writes mcp.json into dir with the server URL and the token,
// readable by the user only, and returns its path. The file must not exist
// yet (O_EXCL): dir has just been created.
func WriteMCPConfig(dir, url, token string) (string, error) {
	config := map[string]any{
		"mcpServers": map[string]any{
			MCPServerName: map[string]any{
				"type":    "http",
				"url":     url,
				"headers": map[string]string{"Authorization": "Bearer " + token},
			},
		},
	}
	return writePrivateJSON(filepath.Join(dir, MCPConfigName), config)
}

func writePrivateJSON(path string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	return path, file.Close()
}
