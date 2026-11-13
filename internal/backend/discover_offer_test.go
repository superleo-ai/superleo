package backend

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeRegistry answers a lookup with the servers whose text carries the whole
// of it, the way the registry matches a phrase as one string.
type fakeRegistry struct {
	mu      sync.Mutex
	asked   []string
	servers []MCPCandidate
	err     error
}

func (r *fakeRegistry) search(_ context.Context, query string, _ int) ([]MCPCandidate, error) {
	r.mu.Lock()
	r.asked = append(r.asked, query)
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	var out []MCPCandidate
	for _, c := range r.servers {
		if strings.Contains(strings.ToLower(c.Name+" "+c.Description), query) {
			out = append(out, c)
		}
	}
	return out, nil
}

func testFinder(reg *fakeRegistry, skills []SkillCandidate, programs ...string) installableFinder {
	return installableFinder{
		mcpServers: reg.search,
		skills: func(query string, _ int) []SkillCandidate {
			var out []SkillCandidate
			for _, sk := range skills {
				if matchesAll(strings.Fields(query), sk.Name+" "+sk.Description) {
					out = append(out, sk)
				}
			}
			return out
		},
		findProgram: func(prog string) (string, bool) {
			return "/bin/" + prog, containsString(programs, prog)
		},
		hasMCP:   func(name string) bool { return name == "already-here" },
		hasSkill: func(name string) bool { return name == "loaded-skill" },
	}
}

func TestOfferQueries(t *testing.T) {
	lookups, words := offerQueries("  Mermaid  render mermaid SVG ")
	if !reflect.DeepEqual(words, []string{"mermaid", "render", "svg"}) {
		t.Errorf("words = %v", words)
	}
	if !reflect.DeepEqual(lookups, []string{"mermaid render svg", "mermaid", "render", "svg"}) {
		t.Errorf("lookups = %v", lookups)
	}
	if lookups, _ := offerQueries("mermaid"); !reflect.DeepEqual(lookups, []string{"mermaid"}) {
		t.Errorf("one word is looked up once, got %v", lookups)
	}
	if lookups, _ := offerQueries("   "); lookups != nil {
		t.Errorf("an empty query looks nothing up, got %v", lookups)
	}
	if _, words := offerQueries("a b c d e f g h i"); len(words) != offerQueryWords {
		t.Errorf("words are capped at %d, got %d", offerQueryWords, len(words))
	}
}

// The phrase finds nothing in the registry; its words do, and the candidate
// carrying the rare word comes before the ones carrying the common one.
func TestInstallableSearchLooksUpEachWordAndRanksByRarity(t *testing.T) {
	reg := &fakeRegistry{servers: []MCPCandidate{
		{Name: "io.example/pdf-render", Description: "Render PDF pages", Command: "npx", Args: []string{"-y", "pdf-render@1.0.0"}},
		{Name: "io.example/html-render", Description: "Render HTML to images", Command: "npx", Args: []string{"-y", "html-render"}},
		{Name: "io.example/mermaid-mcp", Title: "Mermaid", Description: "Render Mermaid diagrams to SVG", Command: "npx", Args: []string{"-y", "mermaid-mcp@2.1.0"}, RequiredEnv: []string{"MERMAID_KEY"}},
	}}
	got := testFinder(reg, nil, "npx").search(context.Background(), "render Mermaid diagram")

	sort.Strings(reg.asked)
	if !reflect.DeepEqual(reg.asked, []string{"diagram", "mermaid", "render", "render mermaid diagram"}) {
		t.Errorf("registry was asked %v", reg.asked)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 installables, got %d: %+v", len(got), got)
	}
	first := got[0]
	if first.Kind != "mcp" || first.Name != "io.example/mermaid-mcp" || first.InstallTool != "add_mcp_server" {
		t.Errorf("first = %+v", first)
	}
	wantArgs := map[string]any{"name": "mermaid-mcp", "command": "npx", "args": []string{"-y", "mermaid-mcp@2.1.0"}}
	if !reflect.DeepEqual(first.InstallArgs, wantArgs) {
		t.Errorf("install args = %v, want %v", first.InstallArgs, wantArgs)
	}
	if !reflect.DeepEqual(first.Needs, []string{"MERMAID_KEY"}) {
		t.Errorf("needs = %v", first.Needs)
	}
	if !strings.HasPrefix(first.Description, "Mermaid. ") {
		t.Errorf("description = %q", first.Description)
	}
}

func TestInstallableSearchOffersOnlyWhatCanRunHere(t *testing.T) {
	reg := &fakeRegistry{servers: []MCPCandidate{
		{Name: "io.example/chart-docker", Description: "chart in a container", Command: "docker", Args: []string{"img"}},
		{Name: "io.example/chart-uvx", Description: "chart in python", Command: "uvx", Args: []string{"chart"}},
		{Name: "io.example/chart-hosted", Description: "chart, hosted", RemoteURL: "https://chart.example/mcp", RemoteType: "streamable-http"},
		{Name: "io.example/chart-both", Description: "chart, python or hosted", Command: "uvx", RemoteURL: "https://both.example/sse", RemoteType: "sse"},
		{Name: "io.example/chart-odd", Description: "chart over a transport nobody speaks", RemoteURL: "https://odd.example", RemoteType: "carrier-pigeon"},
		{Name: "io.example/already-here", Description: "chart, installed", Command: "npx"},
	}}
	got := testFinder(reg, nil, "npx").search(context.Background(), "chart")

	byName := map[string]map[string]any{}
	for _, in := range got {
		byName[in.Name] = in.InstallArgs
	}
	want := map[string]map[string]any{
		"io.example/chart-hosted": {"name": "chart-hosted", "url": "https://chart.example/mcp", "transport": "http"},
		"io.example/chart-both":   {"name": "chart-both", "url": "https://both.example/sse", "transport": "sse"},
	}
	if !reflect.DeepEqual(byName, want) {
		t.Errorf("offered %v\n   want %v", byName, want)
	}
}

func TestInstallableSearchOffersSkillsThatAreNotInstalled(t *testing.T) {
	skills := []SkillCandidate{
		{Name: "diagrams", Description: "Draw a diagram", Path: "/skills/diagrams"},
		{Name: "diagram-old", Description: "diagram, on disk already", Path: "/skills/old", Installed: true},
		{Name: "loaded-skill", Description: "diagram, loaded", Path: "/skills/loaded"},
	}
	got := testFinder(&fakeRegistry{err: errors.New("registry down")}, skills).search(context.Background(), "diagram tool")
	if len(got) != 1 {
		t.Fatalf("want the one skill that is not installed, got %+v", got)
	}
	want := map[string]any{"name": "diagrams", "source_path": "/skills/diagrams"}
	if got[0].Kind != "skill" || got[0].InstallTool != "install_skill" || !reflect.DeepEqual(got[0].InstallArgs, want) {
		t.Errorf("got %+v", got[0])
	}
}

func TestInstallableSearchIsCapped(t *testing.T) {
	reg := &fakeRegistry{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		reg.servers = append(reg.servers, MCPCandidate{Name: "io.example/notes-" + n, Description: "notes", Command: "npx"})
	}
	got := testFinder(reg, nil, "npx").search(context.Background(), "notes")
	if len(got) != offeredInstallables {
		t.Errorf("want %d, got %d", offeredInstallables, len(got))
	}
	if got := testFinder(reg, nil, "npx").search(context.Background(), ""); got != nil {
		t.Errorf("an empty query offers nothing, got %v", got)
	}
}

func TestMCPInstallName(t *testing.T) {
	for in, want := range map[string]string{
		"io.github.someone/thing-mcp": "thing-mcp",
		"plain":                       "plain",
		" com.example/with space ":    "with-space",
	} {
		if got := mcpInstallName(in); got != want {
			t.Errorf("mcpInstallName(%q) = %q, want %q", in, got, want)
		}
	}
}
