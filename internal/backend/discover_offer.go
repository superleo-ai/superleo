package backend

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// What a tool search offers beyond what is installed.
//
// search_mcp_servers and search_skills only help a model that knows to call
// them, and with a large catalogue they sit behind the tool search like
// everything else: a run that searched for "mermaid" was told there was no
// such tool, never saw them, and wrote an npx line in a shell. So the tool
// search itself asks here, and what could be installed comes back in the same
// result with the call that installs it.

const (
	// offeredInstallables caps what one search returns: enough to choose
	// from, few enough to read.
	offeredInstallables = 5
	// offerQueryWords caps how many separate words of a query are looked up.
	offerQueryWords = 6
	// offerPerQuery is how many candidates each lookup may bring back.
	offerPerQuery = 10
)

// installableFinder is where installables come from. The fields are the
// things a test replaces: the registry, the skill directories, the programs
// on this machine and what is already here.
type installableFinder struct {
	mcpServers  func(ctx context.Context, query string, limit int) ([]MCPCandidate, error)
	skills      func(query string, limit int) []SkillCandidate
	findProgram func(prog string) (string, bool)
	hasMCP      func(name string) bool
	hasSkill    func(name string) bool
}

// installableFinder is this service's: the official registry, the skills on
// this machine, and its own servers and skills as what is already installed.
func (s *Service) installableFinder() installableFinder {
	return installableFinder{
		mcpServers:  SearchMCPServers,
		skills:      SearchSkills,
		findProgram: FindProgram,
		hasMCP: func(name string) bool {
			if s == nil || s.svc == nil || s.svc.MCP == nil {
				return false
			}
			for _, srv := range s.svc.MCP.ListServers() {
				if srv.Name == name {
					return true
				}
			}
			return false
		},
		hasSkill: func(name string) bool {
			return s != nil && s.svc != nil && s.svc.HasSkill(name)
		},
	}
}

// offerQueries is what a query is looked up as: the whole of it, and each of
// its words on their own. The registry matches a phrase as one string, so a
// four-word query finds nothing where one of its words finds the server.
func offerQueries(query string) (lookups, words []string) {
	seen := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
		if len(words) == offerQueryWords {
			break
		}
	}
	if len(words) == 0 {
		return nil, nil
	}
	lookups = append(lookups, words...)
	if len(words) > 1 {
		lookups = append([]string{strings.Join(words, " ")}, lookups...)
	}
	return lookups, words
}

// offered is one installable with the text it is ranked by.
type offered struct {
	agent.Installable
	text  string
	score float64
}

// search finds what could be installed for query and can run on this
// machine, best match first.
func (f installableFinder) search(ctx context.Context, query string) []agent.Installable {
	lookups, words := offerQueries(query)
	if len(lookups) == 0 {
		return nil
	}

	// Every lookup at once: the caller is a model waiting on a tool result.
	servers := make([][]MCPCandidate, len(lookups))
	var wg sync.WaitGroup
	if f.mcpServers != nil {
		for i, q := range lookups {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// An unreachable registry is nothing found.
				servers[i], _ = f.mcpServers(ctx, q, offerPerQuery)
			}()
		}
	}
	var skills []SkillCandidate
	if f.skills != nil {
		for _, q := range lookups {
			skills = append(skills, f.skills(q, offerPerQuery)...)
		}
	}
	wg.Wait()

	pool := []offered{}
	taken := map[string]bool{}
	for _, sk := range skills {
		if sk.Installed || taken["skill:"+sk.Name] || (f.hasSkill != nil && f.hasSkill(sk.Name)) {
			continue
		}
		taken["skill:"+sk.Name] = true
		pool = append(pool, offered{
			Installable: agent.Installable{
				Kind: "skill", Name: sk.Name, Description: sk.Description,
				InstallTool: "install_skill",
				InstallArgs: map[string]any{"name": sk.Name, "source_path": sk.Path},
			},
			text: strings.ToLower(sk.Name + " " + sk.Description),
		})
	}
	for _, found := range servers {
		for _, c := range found {
			if taken["mcp:"+c.Name] {
				continue
			}
			taken["mcp:"+c.Name] = true
			in, ok := f.installableServer(c)
			if !ok {
				continue
			}
			pool = append(pool, offered{
				Installable: in,
				text:        strings.ToLower(c.Name + " " + c.Title + " " + c.Description),
			})
		}
	}

	// A candidate is worth the words of the query it carries, and a word is
	// worth less the more candidates carry it: the one server that says
	// "mermaid" outranks the many that say "render".
	for _, w := range words {
		carriers := 0
		for _, o := range pool {
			if strings.Contains(o.text, w) {
				carriers++
			}
		}
		if carriers == 0 {
			continue
		}
		for i := range pool {
			if strings.Contains(pool[i].text, w) {
				pool[i].score += 1 / float64(carriers)
			}
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].score != pool[j].score {
			return pool[i].score > pool[j].score
		}
		return pool[i].Name < pool[j].Name
	})
	if len(pool) > offeredInstallables {
		pool = pool[:offeredInstallables]
	}
	out := make([]agent.Installable, 0, len(pool))
	for _, o := range pool {
		out = append(out, o.Installable)
	}
	return out
}

// installableServer turns a registry candidate into the add_mcp_server call
// that installs it, or reports that this machine cannot run it: a command
// that is not here and no hosted address to use instead.
func (f installableFinder) installableServer(c MCPCandidate) (agent.Installable, bool) {
	name := mcpInstallName(c.Name)
	if name == "" || (f.hasMCP != nil && f.hasMCP(name)) {
		return agent.Installable{}, false
	}
	in := agent.Installable{
		Kind: "mcp", Name: c.Name, Description: c.Description,
		InstallTool: "add_mcp_server",
	}
	if c.Title != "" && c.Title != c.Description {
		in.Description = strings.TrimSpace(c.Title + ". " + c.Description)
	}
	runs := false
	if c.Command != "" && f.findProgram != nil {
		_, runs = f.findProgram(c.Command)
	}
	switch {
	case runs:
		args := c.Args
		if args == nil {
			args = []string{}
		}
		in.InstallArgs = map[string]any{"name": name, "command": c.Command, "args": args}
		in.Needs = c.RequiredEnv
	case c.RemoteURL != "":
		transport, err := MCPTransport(c.RemoteType)
		if err != nil {
			return agent.Installable{}, false
		}
		in.InstallArgs = map[string]any{"name": name, "url": c.RemoteURL, "transport": transport}
	default:
		return agent.Installable{}, false
	}
	return in, true
}

// mcpInstallName is the name a registry server is installed under: the last
// part of "io.github.someone/thing-mcp", which is what its tools are then
// prefixed with.
func mcpInstallName(registryName string) string {
	return sanitizeName(path.Base(strings.TrimSpace(registryName)))
}
