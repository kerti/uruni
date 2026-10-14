package uruni

import (
	"os"
	"regexp"
	"testing"
)

// The image's build stage compiles with the Go that go.mod's toolchain line
// names, so a release binary is the one CI tested and govulncheck scanned.
// Dependabot bumps the two separately; this fails the PR that splits them.
func TestDockerfileGoMatchesToolchain(t *testing.T) {
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	toolchain := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindSubmatch(gomod)
	if toolchain == nil {
		t.Fatal("go.mod has no toolchain line")
	}
	image := regexp.MustCompile(`(?m)^FROM\b.*\bgolang:([0-9.]+)-`).FindSubmatch(dockerfile)
	if image == nil {
		t.Fatal("Dockerfile has no golang:<version>- build stage")
	}
	if string(image[1]) != string(toolchain[1]) {
		t.Errorf("Dockerfile builds with golang:%s, go.mod's toolchain is go%s", image[1], toolchain[1])
	}
}

// The compose file is downloaded on its own (a release asset), so its first
// line says which Uruni it was written for. It must name the tag
// .env.example pins; the release steps bump both together.
func TestComposeVersionMatchesURUNITag(t *testing.T) {
	compose, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	envExample, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatal(err)
	}
	header := regexp.MustCompile(`\A# Uruni (v\S+)\n`).FindSubmatch(compose)
	if header == nil {
		t.Fatal("docker-compose.yml's first line is not `# Uruni vX.Y.Z`")
	}
	tag := regexp.MustCompile(`(?m)^URUNI_TAG=(\S+)$`).FindSubmatch(envExample)
	if tag == nil {
		t.Fatal(".env.example has no URUNI_TAG=<tag> line")
	}
	if string(header[1]) != string(tag[1]) {
		t.Errorf("docker-compose.yml is headed %s, .env.example pins URUNI_TAG=%s", header[1], tag[1])
	}
}
