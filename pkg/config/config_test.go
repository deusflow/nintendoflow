package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadKeywordsHardwareTechShape(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	path := filepath.Join(root, "keywords.yaml")

	topics, err := LoadKeywords(path)
	if err != nil {
		t.Fatalf("LoadKeywords failed: %v", err)
	}

	hardware, ok := topics["hardware_tech"]
	if !ok {
		t.Fatal("hardware_tech topic not found")
	}
	if !hardware.Enabled {
		t.Fatal("hardware_tech topic should be enabled")
	}
	if hardware.Priority <= 0 {
		t.Fatalf("hardware_tech priority must be > 0, got %d", hardware.Priority)
	}

	found := false
	for _, kw := range hardware.Keywords {
		if kw.Word == "architecture" {
			found = true
			if kw.Role != "tech" || kw.Weight != 85 {
				t.Fatalf("architecture keyword malformed: role=%q weight=%d", kw.Role, kw.Weight)
			}
		}
	}
	if !found {
		t.Fatal("architecture keyword not found in hardware_tech")
	}
}
