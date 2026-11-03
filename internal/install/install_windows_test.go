package install

import (
	"encoding/json"
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// prepareBrowsers points the installer to registry keys of the test, removed
// at the end, and returns the browsers that must end up registered.
func prepareBrowsers(t *testing.T) []string {
	t.Helper()
	base := `Software\LeadInHelperTest\` + t.Name()
	saved := registryKeys
	registryKeys = []browser{
		{"Chrome", base + `\Chrome`},
		{"Chromium", base + `\Chromium`},
		{"Edge", base + `\Edge`},
	}
	t.Cleanup(func() {
		registryKeys = saved
		deleteTree(registry.CURRENT_USER, base)
	})
	return []string{"Brave", "Chrome", "Chromium", "Edge", "Vivaldi"}
}

func deleteTree(root registry.Key, path string) {
	key, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err == nil {
		names, _ := key.ReadSubKeyNames(-1)
		key.Close()
		for _, name := range names {
			deleteTree(root, path+`\`+name)
		}
	}
	registry.DeleteKey(root, path)
}

func assertManifestsPointTo(t *testing.T, installed string) {
	t.Helper()
	path, err := manifestPath(HostName)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	var manifest map[string]any
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest["path"] != installed {
		t.Fatalf("manifest = %s (%v)", data, err)
	}
}
