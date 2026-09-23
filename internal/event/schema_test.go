package event_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kobadaidesu/hook-test/internal/event"
)

// The schema in docs/ and the Go types must describe the same document.
// There is no JSON Schema validator in the standard library, so these tests
// compare the property lists and check the schema's rules by hand.

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "commit-snapshot.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return s
}

func jsonTags(typ reflect.Type) []string {
	var tags []string
	for i := 0; i < typ.NumField(); i++ {
		tags = append(tags, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(tags)
	return tags
}

func schemaKeys(t *testing.T, obj map[string]any) (props, required []string) {
	t.Helper()
	for k := range obj["properties"].(map[string]any) {
		props = append(props, k)
	}
	for _, r := range obj["required"].([]any) {
		required = append(required, r.(string))
	}
	sort.Strings(props)
	sort.Strings(required)
	if obj["additionalProperties"] != false {
		t.Errorf("object schema allows additional properties")
	}
	return props, required
}

func TestSchemaMatchesGoTypes(t *testing.T) {
	s := loadSchema(t)
	defs := s["$defs"].(map[string]any)
	for name, typ := range map[string]reflect.Type{
		"":           reflect.TypeOf(event.Event{}),
		"repository": reflect.TypeOf(event.Repository{}),
		"commit":     reflect.TypeOf(event.Commit{}),
		"comparison": reflect.TypeOf(event.Comparison{}),
		"file":       reflect.TypeOf(event.File{}),
		"summary":    reflect.TypeOf(event.Summary{}),
	} {
		obj := s
		if name != "" {
			obj = defs[name].(map[string]any)
		}
		props, required := schemaKeys(t, obj)
		tags := jsonTags(typ)
		if !reflect.DeepEqual(props, tags) || !reflect.DeepEqual(required, tags) {
			t.Errorf("%s: schema properties %v, required %v; Go fields %v", typ.Name(), props, required, tags)
		}
	}
	props := s["properties"].(map[string]any)
	if props["schema_version"].(map[string]any)["const"] != event.SchemaVersion ||
		props["event_type"].(map[string]any)["const"] != event.EventType {
		t.Error("schema constants differ from the Go constants")
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
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var ev event.Event
		if err := dec.Decode(&ev); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		// Every key is present (null is allowed, missing is not).
		reencoded, _ := event.Marshal(&ev)
		if !sameKeys(data, reencoded) {
			t.Errorf("%s: keys differ from the Go encoding", f)
		}
		checkInvariants(t, filepath.Base(f), &ev, data)
	}
}

func sameKeys(a, b []byte) bool {
	var x, y any
	json.Unmarshal(a, &x)
	json.Unmarshal(b, &y)
	return reflect.DeepEqual(keyShape(x), keyShape(y))
}

// keyShape replaces every scalar with nil so only the structure is compared.
func keyShape(v any) any {
	switch v := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range v {
			m[k] = keyShape(e)
		}
		return m
	case []any:
		var l []any
		for _, e := range v {
			l = append(l, keyShape(e))
		}
		return l
	}
	return nil
}

var objectIDRE = regexp.MustCompile(`^[0-9a-f]+$`)

// checkInvariants checks the rules that docs/commit-snapshot.schema.json
// states with const, enum, pattern, if/then and oneOf.
func checkInvariants(t *testing.T, name string, ev *event.Event, raw []byte) {
	t.Helper()
	fail := func(format string, a ...any) { t.Helper(); t.Errorf(name+": "+format, a...) }
	if ev.SchemaVersion != "1.0" || ev.EventType != "commit.snapshot" {
		fail("header %s %s", ev.SchemaVersion, ev.EventType)
	}
	for _, d := range []string{ev.CapturedAt, ev.Commit.AuthoredAt, ev.Commit.CommittedAt} {
		if _, err := time.Parse(time.RFC3339, d); err != nil {
			fail("date %q", d)
		}
	}
	if !strings.HasSuffix(ev.CapturedAt, "Z") {
		fail("captured_at is not UTC: %s", ev.CapturedAt)
	}
	if !objectIDRE.MatchString(ev.Commit.SHA) {
		fail("sha %q", ev.Commit.SHA)
	}
	for _, p := range ev.Commit.Parents {
		if !objectIDRE.MatchString(p) {
			fail("parent %q", p)
		}
	}
	if ev.Commit.Parents == nil || ev.Files == nil || ev.Warnings == nil {
		fail("an array is null")
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
		if len(f.Status) != 1 || f.Status[0] < 'A' || f.Status[0] > 'Z' {
			fail("files[%d].status %q", i, f.Status)
		}
		switch f.Status {
		case "A":
			if f.OldPath != nil || f.NewPath == nil {
				fail("files[%d]: added file paths", i)
			}
		case "D":
			if f.OldPath == nil || f.NewPath != nil {
				fail("files[%d]: deleted file paths", i)
			}
		case "M", "R", "T":
			if f.OldPath == nil || f.NewPath == nil {
				fail("files[%d]: paths", i)
			}
		}
		if (f.Patch == nil) == (f.OmittedReason == nil) {
			fail("files[%d]: patch and omitted_reason must be exclusive", i)
		}
		if f.OmittedReason != nil {
			switch *f.OmittedReason {
			case "binary", "sensitive_path":
			case "total_patch_limit":
				truncated = true
			default:
				fail("files[%d].omitted_reason %q", i, *f.OmittedReason)
			}
			if f.PatchTruncated {
				fail("files[%d]: omitted patch marked truncated", i)
			}
		}
		if f.Binary && (f.Patch != nil || f.Additions != nil || f.Deletions != nil || *f.OmittedReason != "binary") {
			fail("files[%d]: binary file fields", i)
		}
		if f.PatchTruncated {
			truncated = true
		}
	}
	s := ev.Summary
	if s.IncludedFiles != len(ev.Files) || s.OmittedFiles != s.ChangedFiles-s.IncludedFiles || s.Truncated != truncated {
		fail("summary %+v inconsistent with files", s)
	}
	for _, key := range []string{`"files": [`, `"parents": [`, `"warnings": [`} {
		if !bytes.Contains(raw, []byte(key)) {
			fail("JSON lacks %s", key)
		}
	}
}
