package wellbeing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Copied from Vita (apps/health/server/knowledge/health.json), which bundles
// the same file into its iPhone app, so both read identical
// guidance and reference ranges.
//
//go:embed knowledge/health.json
var knowledgeJSON []byte

type kbEntry struct {
	ID     string   `json:"id"`
	Topic  string   `json:"topic"`
	Tags   []string `json:"tags"`
	Text   string   `json:"text"`
	Source string   `json:"source"`
	Urgent bool     `json:"urgent"`
}

type kbBand struct {
	Below  *float64 `json:"below"`
	Level  int      `json:"level"`
	Label  string   `json:"label"`
	Advice string   `json:"advice"`
}

type kbRange struct {
	Metric    string   `json:"metric"`
	Unit      string   `json:"unit"`
	Sex       *string  `json:"sex"`
	AgeMin    *int     `json:"age_min"`
	AgeMax    *int     `json:"age_max"`
	Knowledge string   `json:"knowledge"`
	Bands     []kbBand `json:"bands"`
}

type knowledgeBase struct {
	Version int       `json:"version"`
	Entries []kbEntry `json:"entries"`
	Ranges  []kbRange `json:"ranges"`
	byID    map[string]kbEntry
}

var kb = mustLoadKnowledge(knowledgeJSON)

func mustLoadKnowledge(b []byte) *knowledgeBase {
	var k knowledgeBase
	if err := json.Unmarshal(b, &k); err != nil {
		panic(fmt.Sprintf("knowledge/health.json: %v", err))
	}
	k.byID = make(map[string]kbEntry, len(k.Entries))
	for _, e := range k.Entries {
		k.byID[e.ID] = e
	}
	return &k
}

type kbHit struct {
	kbEntry
	score  int
	tagHit bool
}

// search ranks entries for a question in any language: a tag found inside the
// question counts most (this is what makes Chinese and Japanese questions
// work without a tokenizer), then Latin words shared with the topic or tags.
// Urgent entries matched by a tag come first.
func (k *knowledgeBase) search(query string, limit int) []kbEntry {
	q := strings.ToLower(query)
	words := latinWords(q)
	var hits []kbHit
	for _, e := range k.Entries {
		h := kbHit{kbEntry: e}
		topic := strings.ToLower(e.Topic)
		for _, tag := range e.Tags {
			t := strings.ToLower(tag)
			if t != "" && strings.Contains(q, t) {
				h.score += 3
				if utf8.RuneCountInString(t) >= 4 {
					h.score++
				}
				h.tagHit = true
			}
		}
		text := strings.ToLower(e.Text)
		inText := 0
		for _, w := range words {
			if strings.Contains(topic, w) || containsTag(e.Tags, w) {
				h.score++
			}
			if inText < 3 && strings.Contains(text, w) {
				inText++
			}
		}
		// Words in the body only help an entry that something else matched.
		if h.score > 0 {
			h.score += inText
		}
		if h.score > 0 {
			hits = append(hits, h)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		ui, uj := hits[i].Urgent && hits[i].tagHit, hits[j].Urgent && hits[j].tagHit
		if ui != uj {
			return ui
		}
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].ID < hits[j].ID
	})
	if limit <= 0 {
		limit = 3
	}
	out := []kbEntry{}
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].kbEntry)
	}
	return out
}

func containsTag(tags []string, w string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), w) {
			return true
		}
	}
	return false
}

// latinWords keeps words of three or more letters that are not stop words.
func latinWords(q string) []string {
	stop := map[string]bool{"the": true, "and": true, "for": true, "what": true, "how": true, "why": true, "was": true, "are": true,
		"you": true, "your": true, "my": true, "this": true, "that": true, "with": true, "should": true, "can": true, "today": true, "does": true}
	var out []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r)) || r > unicode.MaxLatin1 }) {
		if len(w) >= 3 && !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

type assessment struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Label  string  `json:"label"`
	// Level: 0 normal, 1 watch, 2 concern, 3 see a doctor, 4 urgent.
	Level     int    `json:"level"`
	Advice    string `json:"advice"`
	Knowledge string `json:"knowledge"`
}

// pick returns the most specific range for a metric that fits the person:
// sex- or age-bound tables only apply when sex or age is known.
func (k *knowledgeBase) pick(metric string, age int, sex string) (*kbRange, error) {
	var best *kbRange
	bestScore := -1
	known := false
	for i := range k.Ranges {
		r := &k.Ranges[i]
		if r.Metric != metric {
			continue
		}
		known = true
		score := 0
		if r.Sex != nil {
			if sex != *r.Sex {
				continue
			}
			score++
		}
		if r.AgeMin != nil {
			if age <= 0 || age < *r.AgeMin {
				continue
			}
			score++
		}
		if r.AgeMax != nil {
			if age <= 0 || age > *r.AgeMax {
				continue
			}
			score++
		}
		if score > bestScore {
			best, bestScore = r, score
		}
	}
	switch {
	case !known:
		return nil, fmt.Errorf("unknown metric %q; known: %s", metric, strings.Join(k.metrics(), ", "))
	case best == nil:
		return nil, fmt.Errorf("%s needs the person's sex and/or age", metric)
	}
	return best, nil
}

func (k *knowledgeBase) metrics() []string {
	seen := map[string]bool{"blood_pressure": true}
	out := []string{"blood_pressure"}
	for _, r := range k.Ranges {
		if !seen[r.Metric] {
			seen[r.Metric] = true
			out = append(out, r.Metric)
		}
	}
	sort.Strings(out)
	return out
}

func (k *knowledgeBase) assess(metric string, value float64, age int, sex string) (assessment, error) {
	r, err := k.pick(metric, age, sex)
	if err != nil {
		return assessment{}, err
	}
	for _, b := range r.Bands {
		if b.Below == nil || value < *b.Below {
			return assessment{Metric: metric, Value: value, Unit: r.Unit, Label: b.Label, Level: b.Level, Advice: b.Advice, Knowledge: r.Knowledge}, nil
		}
	}
	return assessment{}, fmt.Errorf("%s: no band", metric)
}

// assessBloodPressure judges systolic and diastolic and keeps the worse one,
// which is how the guidelines classify a reading.
func (k *knowledgeBase) assessBloodPressure(sys, dia float64) (assessment, error) {
	s, err := k.assess("systolic", sys, 0, "")
	if err != nil {
		return assessment{}, err
	}
	d, err := k.assess("diastolic", dia, 0, "")
	if err != nil {
		return assessment{}, err
	}
	worse := s
	if d.Level > s.Level {
		worse = d
	}
	worse.Metric, worse.Value, worse.Unit = "blood_pressure", sys, fmt.Sprintf("mmHg (%g/%g)", sys, dia)
	return worse, nil
}
