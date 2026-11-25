package app

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/liliang-cn/superleo/internal/backend"
)

// A paired phone's settings screen.
//
// A device token may call any App method, and GetSettings answers with the
// whole file: every API key, the shared memory token, the hive's join and
// peer tokens, each linked agent's token. The phone gets its own pair
// instead. PhoneSettings names, field by field, what the screen shows, with
// each secret only as set or not set; PatchPhoneSettings takes a merge patch
// of only those fields, so a phone can replace a key but never read one, and
// cannot reach a field its screen does not have. The full-file methods are
// refused to a device (deviceDenied).

// PhoneSettingsView is the settings as a phone reads them.
type PhoneSettingsView struct {
	QueenName    string   `json:"queen_name"`
	QueenAliases []string `json:"queen_aliases"`

	WorkspaceDir      string            `json:"workspace_dir"`
	Proxy             PhoneProxyView    `json:"proxy"`
	MaxRounds         int               `json:"max_rounds"`
	TurnMaxTokens     int               `json:"turn_max_tokens"`
	LongTaskMaxTokens int               `json:"long_task_max_tokens"`
	PIIRedaction      bool              `json:"pii_redaction"`
	DisablePTC        bool              `json:"disable_ptc"`
	LLMBaseURL        string            `json:"llm_base_url"`
	LLMModel          string            `json:"llm_model"`
	MemoryBackend     string            `json:"memory_backend"`
	SharedEndpoint    string            `json:"shared_memory_endpoint"`
	SharedNamespace   string            `json:"shared_memory_namespace"`
	EmbedBaseURL      string            `json:"embed_base_url"`
	EmbedModel        string            `json:"embed_model"`
	DisableApproval   bool              `json:"disable_tool_approval"`
	RemoteAgents      PhoneRemoteAgents `json:"remote_agents"`
	HiveRole          string            `json:"hive_role"`
	PhoneRelay        string            `json:"phone_relay"`
	PhoneURLs         []string          `json:"phone_urls"`

	// Features is each switchable part by name (browser, desktop, coding, wellbeing,
	// finance); the phone hides what is off.
	Features map[string]bool `json:"features"`

	// Secrets says which secrets are set, by their settings key.
	Secrets map[string]bool `json:"secrets"`
}

// PhoneProxyView is the proxy with any password in its address redacted.
type PhoneProxyView struct {
	Mode string `json:"mode"`
	URL  string `json:"url"`
}

// PhoneRemoteAgents is the linked agents by name and host, never by token.
type PhoneRemoteAgents struct {
	Enabled bool               `json:"enabled"`
	Agents  []PhoneRemoteAgent `json:"agents"`
}

type PhoneRemoteAgent struct {
	Name  string   `json:"name"`
	About string   `json:"about,omitempty"`
	Hosts []string `json:"hosts"`
}

// phoneSecretKeys are the secrets the screen can replace.
var phoneSecretKeys = []string{"llm_key", "embed_key", "shared_memory_token"}

// phonePlainKeys are the top-level fields a phone may set as they are.
var phonePlainKeys = map[string]bool{
	"queen_name": true, "queen_aliases": true,
	"workspace_dir": true, "max_rounds": true, "turn_max_tokens": true, "long_task_max_tokens": true,
	"pii_redaction": true, "disable_ptc": true,
	"llm_base_url": true, "llm_key": true, "llm_model": true,
	"memory_backend": true, "shared_memory_endpoint": true, "shared_memory_token": true, "shared_memory_namespace": true,
	"embed_base_url": true, "embed_key": true, "embed_model": true,
	"disable_tool_approval": true,
	"phone_relay":           true, "phone_urls": true,
}

func phoneSettingsOf(s backend.Settings) PhoneSettingsView {
	v := PhoneSettingsView{
		QueenName: s.QueenName, QueenAliases: nonNilStrings(s.QueenAliases),
		WorkspaceDir: s.WorkspaceDir,
		Proxy:        PhoneProxyView{Mode: s.Proxy.Mode, URL: redactURL(s.Proxy.URL)},
		MaxRounds:    s.MaxRounds, TurnMaxTokens: s.TurnMaxTokens, LongTaskMaxTokens: s.LongTaskMaxTokens,
		PIIRedaction: s.PIIRedaction, DisablePTC: s.DisablePTC,
		LLMBaseURL: redactURL(s.LLMBaseURL), LLMModel: s.LLMModel,
		MemoryBackend: s.MemoryBackend, SharedEndpoint: s.SharedMemoryEndpoint, SharedNamespace: s.SharedMemoryNamespace,
		EmbedBaseURL: redactURL(s.EmbedBaseURL), EmbedModel: s.EmbedModel,
		DisableApproval: s.DisableToolApproval,
		RemoteAgents:    PhoneRemoteAgents{Enabled: s.RemoteAgents.Enabled, Agents: []PhoneRemoteAgent{}},
		HiveRole:        s.Hive.Role,
		PhoneRelay:      s.PhoneRelay, PhoneURLs: nonNilStrings(s.PhoneURLs),
		Features: s.Features(),
		Secrets: map[string]bool{
			"llm_key":             s.LLMKey != "",
			"embed_key":           s.EmbedKey != "",
			"shared_memory_token": s.SharedMemoryToken != "",
		},
	}
	names := make([]string, 0, len(s.RemoteAgents.Agents))
	for n := range s.RemoteAgents.Agents {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := s.RemoteAgents.Agents[n]
		v.RemoteAgents.Agents = append(v.RemoteAgents.Agents, PhoneRemoteAgent{Name: n, About: a.About, Hosts: nonNilStrings(a.Hosts)})
	}
	return v
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// redactURL hides a password written into an address (user:pass@host). An
// address that does not parse is left out rather than shown as it is.
func redactURL(raw string) string {
	if raw == "" || !strings.Contains(raw, "@") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Redacted()
}

// PhoneSettings is the settings a paired phone's screen shows, secrets only
// as set or not set.
func (a *App) PhoneSettings() PhoneSettingsView {
	return phoneSettingsOf(a.GetSettings())
}

// PatchPhoneSettings applies a merge patch of the fields PhoneSettings shows
// and returns them as saved. Anything else in it is refused whole.
func (a *App) PatchPhoneSettings(patch map[string]any) (PhoneSettingsView, error) {
	features, rest, err := phoneFeatures(patch)
	if err != nil {
		return PhoneSettingsView{}, err
	}
	clean, err := phonePatch(a.GetSettings(), rest)
	if err != nil {
		return PhoneSettingsView{}, err
	}
	if len(features) > 0 {
		// Applied to the settings as they are, then saved with the rest.
		cur := a.GetSettings()
		next, err := mergeSettingsPatch(cur, clean)
		if err != nil {
			return PhoneSettingsView{}, err
		}
		for _, name := range backend.FeatureNames {
			if on, ok := features[name]; ok {
				next.SetFeature(name, on)
			}
		}
		if err := a.SaveSettings(next); err != nil {
			return PhoneSettingsView{}, err
		}
		return a.PhoneSettings(), nil
	}
	if len(clean) == 0 {
		return a.PhoneSettings(), nil
	}
	s, err := a.PatchSettings(clean)
	if err != nil {
		return PhoneSettingsView{}, err
	}
	return phoneSettingsOf(s), nil
}

// phonePatch checks a phone's patch against what its screen has, and drops a
// redacted address sent back unchanged so the real one is kept.
func phonePatch(cur backend.Settings, patch map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range patch {
		switch {
		case k == "cliproxy_enabled":
			// Gone with the embedded proxy; an older phone may still send it.
			continue
		case phonePlainKeys[k]:
			if v == nil {
				return nil, fmt.Errorf("%s cannot be removed", k)
			}
			if s, ok := v.(string); ok && (k == "llm_base_url" || k == "embed_base_url") {
				var was string
				if k == "llm_base_url" {
					was = cur.LLMBaseURL
				} else {
					was = cur.EmbedBaseURL
				}
				if s != was && s == redactURL(was) {
					continue
				}
			}
			out[k] = v
		case k == "proxy":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("proxy must be an object")
			}
			p := map[string]any{}
			for pk, pv := range m {
				if pk != "mode" && pk != "url" {
					return nil, fmt.Errorf("proxy.%s cannot be set from a phone", pk)
				}
				s, ok := pv.(string)
				if !ok {
					return nil, fmt.Errorf("proxy.%s must be text", pk)
				}
				if pk == "url" && s != cur.Proxy.URL && s == redactURL(cur.Proxy.URL) {
					continue
				}
				p[pk] = s
			}
			if len(p) > 0 {
				out["proxy"] = p
			}
		case k == "remote_agents":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("remote_agents must be an object")
			}
			r := map[string]any{}
			for rk, rv := range m {
				switch rk {
				case "enabled":
					b, ok := rv.(bool)
					if !ok {
						return nil, fmt.Errorf("remote_agents.enabled must be true or false")
					}
					r[rk] = b
				case "agents":
					// Only taking one out: adding one is linking it, which
					// has its own call (LinkSuperLeo) that checks the code.
					am, ok := rv.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("remote_agents.agents must be an object")
					}
					for n, av := range am {
						if av != nil {
							return nil, fmt.Errorf("an agent can only be removed from a phone")
						}
						if _, ok := cur.RemoteAgents.Agents[n]; !ok {
							return nil, fmt.Errorf("no agent called %s", n)
						}
					}
					if len(am) > 0 {
						r[rk] = am
					}
				default:
					return nil, fmt.Errorf("remote_agents.%s cannot be set from a phone", rk)
				}
			}
			if len(r) > 0 {
				out["remote_agents"] = r
			}
		case k == "hive":
			// The one switch the screen has: act as queen.
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("hive must be an object")
			}
			if cur.Hive.Role == "worker" {
				return nil, fmt.Errorf("a worker cannot be made queen")
			}
			h := map[string]any{}
			for hk, hv := range m {
				switch hk {
				case "role":
					s, _ := hv.(string)
					if hv == nil || (s != "" && s != "queen") {
						return nil, fmt.Errorf("hive.role must be queen or empty")
					}
					h[hk] = s
				case "standalone":
					b, ok := hv.(bool)
					if !ok {
						return nil, fmt.Errorf("hive.standalone must be true or false")
					}
					h[hk] = b
				default:
					return nil, fmt.Errorf("hive.%s cannot be set from a phone", hk)
				}
			}
			if len(h) > 0 {
				out["hive"] = h
			}
		default:
			return nil, fmt.Errorf("%s cannot be set from a phone", k)
		}
	}
	return out, nil
}

// phoneFeatures takes the features out of a phone's patch: an object of
// feature names to true or false. The rest goes through phonePatch.
func phoneFeatures(patch map[string]any) (map[string]bool, map[string]any, error) {
	raw, ok := patch["features"]
	if !ok {
		return nil, patch, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("features must be an object")
	}
	known := map[string]bool{}
	for _, n := range backend.FeatureNames {
		known[n] = true
	}
	out := map[string]bool{}
	for k, v := range m {
		if !known[k] {
			return nil, nil, fmt.Errorf("no feature called %s", k)
		}
		b, ok := v.(bool)
		if !ok {
			return nil, nil, fmt.Errorf("features.%s must be true or false", k)
		}
		out[k] = b
	}
	rest := make(map[string]any, len(patch))
	for k, v := range patch {
		if k != "features" {
			rest[k] = v
		}
	}
	return out, rest, nil
}
