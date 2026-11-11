package extensions

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func searchTerms(text string) []string {
	terms := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for i, term := range terms {
		// Fold a plural so "schedule" finds list_schedules.
		if len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss") {
			terms[i] = term[:len(term)-1]
		}
	}
	return terms
}

// RankTools orders tools by lexical relevance to a query; tools that match no
// query term are left out. Callers rank the built-in tool set with it.
func RankTools(tools []runtime.Tool, query string) []runtime.Tool {
	return rankToolMatches(tools, query)
}

// QueryCoverage is the share of the query's distinct terms that appear in the
// tool's name or description: 1 when every term does, 0 when none does.
func QueryCoverage(tool runtime.Tool, query string) float64 {
	have := map[string]bool{}
	for _, t := range searchTerms(tool.Name + " " + tool.Description) {
		have[t] = true
	}
	want := map[string]bool{}
	for _, t := range searchTerms(query) {
		want[t] = true
	}
	if len(want) == 0 {
		return 0
	}
	hit := 0
	for t := range want {
		if have[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

// BM25 lexical ranking over the bounded server page. Unlike AND matching,
// extra words in a natural-language query do not discard a relevant tool.
// This does not provide translation or semantic/synonym matching.
func rankToolMatches(tools []runtime.Tool, query string) []runtime.Tool {
	if len(tools) == 0 {
		return nil
	}
	type doc struct {
		tool   runtime.Tool
		terms  map[string]int
		length int
		score  float64
	}
	docs := make([]doc, 0, len(tools))
	df := map[string]int{}
	total := 0
	for _, tool := range tools {
		terms := searchTerms(tool.Name + " " + tool.Description)
		freq := map[string]int{}
		for _, term := range terms {
			freq[term]++
		}
		for term := range freq {
			df[term]++
		}
		docs = append(docs, doc{tool: tool, terms: freq, length: len(terms)})
		total += len(terms)
	}
	avg := math.Max(1, float64(total)/float64(len(docs)))
	unique := map[string]bool{}
	for _, term := range searchTerms(query) {
		unique[term] = true
	}
	// Sort query terms so floating-point accumulation and ties are deterministic.
	terms := make([]string, 0, len(unique))
	for term := range unique {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	for i := range docs {
		d := &docs[i]
		for _, term := range terms {
			tf := float64(d.terms[term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(docs)-df[term])+0.5)/(float64(df[term])+0.5))
			d.score += idf * tf * 2.2 / (tf + 1.2*(0.25+0.75*float64(d.length)/avg))
		}
		if exactToolNameMatch(d.tool.Name, query) {
			d.score += 100
		}
	}
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].score == docs[j].score {
			return docs[i].tool.Name < docs[j].tool.Name
		}
		return docs[i].score > docs[j].score
	})
	var out []runtime.Tool
	for _, d := range docs {
		if d.score > 0 {
			out = append(out, d.tool)
		}
	}
	return out
}

// rankExpandedToolMatches combines a few independently ranked search phrases.
// It only receives metadata from servers already selected and authorized by
// discovery. The model supplies phrases, never callable names or permissions.
func rankExpandedToolMatches(tools []runtime.Tool, phrases []string) []runtime.Tool {
	if len(phrases) == 0 || len(tools) == 0 {
		return nil
	}
	type candidate struct {
		tool  runtime.Tool
		score float64
	}
	byName := map[string]*candidate{}
	for _, phrase := range phrases {
		if len(phrase) == 0 || len(phrase) > 256 {
			continue
		}
		for rank, tool := range rankToolMatches(tools, phrase) {
			entry := byName[tool.Name]
			if entry == nil {
				entry = &candidate{tool: tool}
				byName[tool.Name] = entry
			}
			entry.score += 1 / float64(60+rank+1)
		}
	}
	out := make([]candidate, 0, len(byName))
	for _, entry := range byName {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score == out[j].score {
			return out[i].tool.Name < out[j].tool.Name
		}
		return out[i].score > out[j].score
	})
	result := make([]runtime.Tool, 0, len(out))
	for _, entry := range out {
		result = append(result, entry.tool)
	}
	return result
}

// exactToolNameMatch reports a query that names the tool: the full name, the
// server's own tool name, or a tail of it that starts at a separator
// ("query-data-sources" for "mcp_notion__notion-query-data-sources").
func exactToolNameMatch(name, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	name = strings.ToLower(name)
	remote := name
	if at := strings.LastIndex(name, "__"); at >= 0 {
		remote = name[at+2:]
	}
	if name == q || remote == q {
		return true
	}
	for _, sep := range []string{"-", "_", "."} {
		if strings.HasSuffix(remote, sep+q) {
			return true
		}
	}
	return false
}
