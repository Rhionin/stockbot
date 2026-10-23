package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGrocySyncStaysOffTheDefaultCompose(t *testing.T) {
	root := filepath.Join("..", "..", "deploy")
	mainRaw, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainRaw), "pantry_cf-tunnel") {
		t.Fatal("docker-compose.yml must not attach to pantry_cf-tunnel")
	}
	if !strings.Contains(string(mainRaw), "PANTRY_SYNC_TOKEN: ${PANTRY_SYNC_TOKEN:-}") {
		t.Fatal("pantry must receive PANTRY_SYNC_TOKEN")
	}

	raw, err := os.ReadFile(filepath.Join(root, "docker-compose.grocy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Services map[string]struct {
			Image       string   `yaml:"image"`
			Profiles    []string `yaml:"profiles"`
			MemLimit    string   `yaml:"mem_limit"`
			Ports       []any    `yaml:"ports"`
			ReadOnly    bool     `yaml:"read_only"`
			CapDrop     []string `yaml:"cap_drop"`
			SecurityOpt []string `yaml:"security_opt"`
			Networks    []string `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]struct {
			External bool   `yaml:"external"`
			Name     string `yaml:"name"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	svc, ok := file.Services["grocy-sync"]
	if !ok {
		t.Fatal("grocy-sync service missing")
	}
	if len(svc.Profiles) != 1 || svc.Profiles[0] != "grocy-sync" {
		t.Fatalf("profiles = %v, want [grocy-sync]", svc.Profiles)
	}
	if svc.MemLimit != "64m" {
		t.Fatalf("mem_limit = %q", svc.MemLimit)
	}
	if len(svc.Ports) != 0 {
		t.Fatalf("ports = %v", svc.Ports)
	}
	if !svc.ReadOnly || !contains(svc.CapDrop, "ALL") || !contains(svc.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("service is not locked down: %+v", svc)
	}
	if !contains(svc.Networks, "default") || !contains(svc.Networks, "grocy") {
		t.Fatalf("networks = %v", svc.Networks)
	}
	if !strings.Contains(svc.Image, "ghcr.io/rhionin/pantry-grocy-sync:") {
		t.Fatalf("image = %q", svc.Image)
	}
	net := file.Networks["grocy"]
	if !net.External || net.Name != "pantry_cf-tunnel" {
		t.Fatalf("grocy network = %+v", net)
	}

	setup, err := os.ReadFile(filepath.Join(root, "setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(setup), "docker-compose.grocy.yml") {
		t.Fatal("setup.sh must copy docker-compose.grocy.yml")
	}
	env, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(env)
	for _, line := range []string{"GROCY_URL=", "GROCY_API_KEY=", "PANTRY_SYNC_TOKEN="} {
		if !strings.Contains(text, line+"\n") && !strings.Contains(text, line+"\r\n") {
			t.Fatalf(".env.example missing empty %s", line)
		}
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
