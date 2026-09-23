package event_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/event"
)

// The schema in docs/ and the Payload type must describe the same document.
// There is no JSON Schema validator in the standard library, so these tests
// compare the property lists and check the schema's rules by hand, using
// the patterns written in the schema itself.

var (
	schemaOnce sync.Once
	schema     map[string]any
	schemaErr  error
)

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	schemaOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", "commit-payload.schema.json"))
		if err != nil {
			schemaErr = err
			return
		}
		schemaErr = json.Unmarshal(data, &schema)
	})
	if schemaErr != nil {
		t.Fatalf("loading the schema: %v", schemaErr)
	}
	return schema
}

func schemaPattern(t *testing.T, prop string) *regexp.Regexp {
	t.Helper()
	p := loadSchema(t)["properties"].(map[string]any)[prop].(map[string]any)["pattern"].(string)
	return regexp.MustCompile(p)
}

var payloadKeys = []string{"branch", "commit_sha", "diff", "files", "message", "repository_id"}

func TestSchemaMatchesPayload(t *testing.T) {
	s := loadSchema(t)
	var props, required, tags []string
	for k := range s["properties"].(map[string]any) {
		props = append(props, k)
	}
	for _, r := range s["required"].([]any) {
		required = append(required, r.(string))
	}
	typ := reflect.TypeOf(event.Payload{})
	for i := 0; i < typ.NumField(); i++ {
		tags = append(tags, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(props)
	sort.Strings(required)
	sort.Strings(tags)
	if !reflect.DeepEqual(props, payloadKeys) || !reflect.DeepEqual(required, payloadKeys) || !reflect.DeepEqual(tags, payloadKeys) {
		t.Errorf("schema properties %v, required %v, Go fields %v; want %v", props, required, tags, payloadKeys)
	}
	if s["additionalProperties"] != false {
		t.Error("the schema allows additional properties")
	}
}

func TestExamplesMatchSchema(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.json"))
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checkPayload(t, filepath.Base(f), data)
	}
}

// checkPayload checks one JSON document against the schema's rules and
// returns it decoded.
func checkPayload(t *testing.T, name string, data []byte) *event.Payload {
	t.Helper()
	fail := func(format string, a ...any) { t.Helper(); t.Errorf(name+": "+format, a...) }

	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("%s: not a JSON object: %v", name, err)
	}
	var keys []string
	for k := range generic {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, payloadKeys) {
		fail("top-level keys %v, want exactly %v", keys, payloadKeys)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p event.Payload
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		fail("more than one JSON value")
	}
	if !schemaPattern(t, "repository_id").MatchString(p.RepositoryID) {
		fail("repository_id %q", p.RepositoryID)
	}
	if !schemaPattern(t, "commit_sha").MatchString(p.CommitSHA) {
		fail("commit_sha %q", p.CommitSHA)
	}
	if p.Branch != nil && *p.Branch == "" {
		fail("empty branch")
	}
	if p.Files == nil {
		fail("files is null")
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if f == "" || seen[f] {
			fail("files has an empty or repeated entry %q", f)
		}
		seen[f] = true
	}
	// files and diff describe the same files, in the same order.
	headers := sectionHeaders(p.Diff)
	if len(p.Files) == 0 && p.Diff != "" || len(headers) < len(p.Files) {
		fail("%d files but %d diff sections", len(p.Files), len(headers))
	}
	h := 0
	for _, f := range p.Files {
		quoted := gitQuoter.Replace(f) // how git writes the path in a header
		for h < len(headers) && !strings.Contains(headers[h], f) && !strings.Contains(headers[h], quoted) {
			h++
		}
		if h == len(headers) {
			fail("no diff section (in order) for %q", f)
			break
		}
	}
	if p.Diff != "" && !strings.HasPrefix(p.Diff, "diff --git ") {
		fail("diff does not start with a section header")
	}
	return &p
}

// gitQuoter escapes the characters git escapes in quoted header paths.
var gitQuoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\t", `\t`, "\n", `\n`)

// sectionHeaders returns the lines that start a patch section.
func sectionHeaders(diff string) []string {
	var h []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			h = append(h, line)
		}
	}
	return h
}

func countSections(diff string) int { return len(sectionHeaders(diff)) }

// checkEvent checks the invariants of the internal snapshot.
func checkEvent(t *testing.T, name string, ev *event.Event) {
	t.Helper()
	fail := func(format string, a ...any) { t.Helper(); t.Errorf(name+": "+format, a...) }
	if ev.Commit.Parents == nil || ev.Files == nil || ev.Warnings == nil {
		fail("a list is nil")
	}
	switch ev.Comparison.Strategy {
	case "first_parent":
		if ev.Comparison.BaseSHA == nil || len(ev.Commit.Parents) == 0 || *ev.Comparison.BaseSHA != ev.Commit.Parents[0] {
			fail("first_parent comparison %+v with parents %v", ev.Comparison, ev.Commit.Parents)
		}
	case "empty_tree":
		if ev.Comparison.BaseSHA != nil || len(ev.Commit.Parents) != 0 {
			fail("empty_tree comparison %+v with parents %v", ev.Comparison, ev.Commit.Parents)
		}
	default:
		fail("strategy %q", ev.Comparison.Strategy)
	}
	truncated := ev.Summary.OmittedFiles > 0
	for i, f := range ev.Files {
		switch f.Status {
		case "A":
			if f.OldPath != nil || f.NewPath == nil {
				fail("files[%d]: added file paths", i)
			}
		case "D":
			if f.OldPath == nil || f.NewPath != nil {
				fail("files[%d]: deleted file paths", i)
			}
		default:
			if f.OldPath == nil || f.NewPath == nil {
				fail("files[%d]: paths", i)
			}
		}
		if (f.Patch == nil) == (f.OmittedReason == nil) {
			fail("files[%d]: patch and omitted reason must be exclusive", i)
		}
		if f.OmittedReason != nil && *f.OmittedReason == "total_patch_limit" || f.PatchTruncated {
			truncated = true
		}
		if f.Binary && (f.Patch != nil || f.Additions != nil || *f.OmittedReason != "binary") {
			fail("files[%d]: binary file fields", i)
		}
	}
	s := ev.Summary
	if s.IncludedFiles != len(ev.Files) || s.OmittedFiles != s.ChangedFiles-s.IncludedFiles || s.Truncated != truncated {
		fail("summary %+v inconsistent with files", s)
	}
}
