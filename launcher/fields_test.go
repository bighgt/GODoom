package launcher

import (
	"reflect"
	"strings"
	"testing"

	"twopointfive/config"
)

// excludedFields lists every config.Config field this launcher deliberately
// does NOT expose as a settings-panel row, and why — see the fields var's
// doc comment. Kept as its own table (rather than just subtracting a count)
// so a mismatch names exactly which field is missing.
var excludedFields = map[string]string{
	"Map":            "the dedicated map dropdown, not a settings row",
	"ShowStatusBar":  "deprecated back-compat shim superseded by hudStyle",
	"WADPath":        "set programmatically when a WAD is picked",
	"CartoonShading": "promoted to its own row in screen.go's top bar",
	"Music":          "no checkbox — the Music Volume slider is the on/off (run.go derives Music from it)",
}

// TestFieldsCoverEveryConfigField guards against silently forgetting a
// newly-added config.Config field the same way config_test.go's
// TestDefaultJSONIsComplete guards DefaultJSON.
func TestFieldsCoverEveryConfigField(t *testing.T) {
	byJSONTag := make(map[string]bool, len(fields))
	for _, f := range fields {
		if byJSONTag[f.id] {
			t.Errorf("duplicate field id %q", f.id)
		}
		byJSONTag[f.id] = true
	}

	ct := reflect.TypeOf(config.Config{})
	seen := 0
	for i := 0; i < ct.NumField(); i++ {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("json"), ",")
		if _, excluded := excludedFields[ct.Field(i).Name]; excluded {
			if byJSONTag[name] {
				t.Errorf("Config.%s is both excluded and present in fields — pick one", ct.Field(i).Name)
			}
			continue
		}
		if !byJSONTag[name] {
			t.Errorf("Config.%s (json %q) has no launcher settings row — add one to fields, or to excludedFields with a reason", ct.Field(i).Name, name)
			continue
		}
		seen++
	}
	if seen != len(fields) {
		t.Errorf("fields has %d rows but only %d matched a Config field — a row's id doesn't match any json tag", len(fields), seen)
	}
}

// TestEveryFieldHasAHint keeps the hover-tooltip coverage honest: a new
// settings row without a hint would just show nothing on rollover.
func TestEveryFieldHasAHint(t *testing.T) {
	for _, f := range fields {
		if strings.TrimSpace(f.hint) == "" {
			t.Errorf("%s: no hint text", f.id)
		}
	}
}

// TestEverySectionHasATabTitle guards the tab strip: a section in
// sectionOrder with no tabTitles entry would draw a blank tab.
func TestEverySectionHasATabTitle(t *testing.T) {
	for _, s := range sectionOrder {
		if strings.TrimSpace(tabTitles[s]) == "" {
			t.Errorf("section %q has no tab title", s)
		}
		if len(fieldsInSection(s)) == 0 {
			t.Errorf("section %q has a tab but no fields", s)
		}
	}
}

// TestOptionLabelsAreDistinctFromRawKeywords guards the actual complaint
// this table exists to fix: a dropdown showing config.json's literal
// keyword (aniso8x, quake2, hudStyle's "none", ...) instead of a real
// settings label. Every kindOptions field's choices must have a non-empty
// label, a value unique within that field's own list (so labelFor's
// value->label lookup is never ambiguous), and at least one choice whose
// label actually differs from its raw value — a list where every label
// equals its value would silently defeat the whole point of this table.
func TestOptionLabelsAreDistinctFromRawKeywords(t *testing.T) {
	for _, f := range fields {
		if f.kind != kindOptions {
			continue
		}
		seen := make(map[string]bool, len(f.options))
		anyDifferent := false
		for _, o := range f.options {
			if o.label == "" {
				t.Errorf("%s: choice %q has an empty label", f.id, o.value)
			}
			if seen[o.value] {
				t.Errorf("%s: duplicate option value %q", f.id, o.value)
			}
			seen[o.value] = true
			if o.label != o.value {
				anyDifferent = true
			}
		}
		if !anyDifferent {
			t.Errorf("%s: every label equals its raw value — labels were meant to read better than that", f.id)
		}
	}
}

// TestLabelForFindsAndFallsBack checks the closed dropdown box's own
// value->label lookup directly.
func TestLabelForFindsAndFallsBack(t *testing.T) {
	opts := []choice{{"a", "Alpha"}, {"b", "Beta"}}
	if got := labelFor(opts, "b"); got != "Beta" {
		t.Errorf("labelFor(b) = %q, want Beta", got)
	}
	if got := labelFor(opts, "unknown"); got != "unknown" {
		t.Errorf("labelFor(unknown) = %q, want the raw value back as a fallback", got)
	}
}

// TestFieldAccessorsAreWired sanity-checks every row actually has the
// accessor its kind needs, and that slider/int-slider bounds are sane —
// catches a copy-paste row with the wrong kind or a forgotten accessor.
func TestFieldAccessorsAreWired(t *testing.T) {
	cfg := config.Default()
	for _, f := range fields {
		switch f.kind {
		case kindCheckbox:
			if f.boolAt == nil {
				t.Errorf("%s: kindCheckbox with no boolAt", f.id)
			} else {
				_ = *f.boolAt(&cfg)
			}
		case kindSlider:
			if f.floatAt == nil {
				t.Errorf("%s: kindSlider with no floatAt", f.id)
			} else {
				_ = *f.floatAt(&cfg)
			}
			if f.max <= f.min {
				t.Errorf("%s: slider max %v <= min %v", f.id, f.max, f.min)
			}
			if f.format == "" {
				t.Errorf("%s: slider with no format", f.id)
			}
		case kindIntSlider:
			if f.intAt == nil {
				t.Errorf("%s: kindIntSlider with no intAt", f.id)
			} else {
				_ = *f.intAt(&cfg)
			}
			if f.max <= f.min {
				t.Errorf("%s: int slider max %v <= min %v", f.id, f.max, f.min)
			}
		case kindIntField:
			if f.intAt == nil {
				t.Errorf("%s: kindIntField with no intAt", f.id)
			} else {
				_ = *f.intAt(&cfg)
			}
		case kindOptions:
			if f.stringAt == nil {
				t.Errorf("%s: kindOptions with no stringAt", f.id)
			} else {
				v := *f.stringAt(&cfg)
				found := false
				for _, o := range f.options {
					if o.value == v {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: default value %q isn't one of options %v", f.id, v, f.options)
				}
			}
			if len(f.options) == 0 {
				t.Errorf("%s: kindOptions with no options", f.id)
			}
		case kindTextField:
			if f.stringAt == nil {
				t.Errorf("%s: kindTextField with no stringAt", f.id)
			} else {
				_ = *f.stringAt(&cfg)
			}
		}
		found := false
		for _, s := range sectionOrder {
			if s == f.section {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: section %q isn't in sectionOrder", f.id, f.section)
		}
	}
}
