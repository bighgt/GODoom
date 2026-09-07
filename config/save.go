package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// jsonValuePattern matches one of Config's possible encoded leaf values —
// every field is a plain string, number, or bool, never an object or array,
// so this never needs to understand nesting.
const jsonValuePattern = `(?:"(?:[^"\\]|\\.)*"|-?[0-9]+(?:\.[0-9]+)?(?:[eE][-+]?[0-9]+)?|true|false|null)`

// SaveMerge writes cfg to path, preserving everything about the file that
// isn't one of Config's own values — most importantly the hand-authored
// `"//fieldName": "description"` documentation lines the shipped
// config.json carries above nearly every real key (see game_design.txt
// section 17): a plain json.MarshalIndent(cfg) would silently discard every
// one of those on save, which is exactly the kind of surprise a settings
// screen must not spring on a file the user already has open and annotated.
//
// If path doesn't exist yet there's nothing to preserve, so this just
// writes a complete file (the same shape as DefaultJSON). Otherwise, for
// each of Config's json-tagged fields it finds the line
// `"key": <value>,` (anchored so "shadows" can't match "groundShadows", and
// so a `"//key"` comment line — which has different text between its
// quotes — is never touched) and replaces only the value, keeping
// indentation, trailing comma, and every surrounding line byte-for-byte. A
// key not yet present in the file (e.g. a field added to Config after this
// config.json was written) is appended just before the closing brace,
// comma-joined with whatever was already last.
func SaveMerge(cfg Config, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			data, mErr := json.MarshalIndent(cfg, "", "  ")
			if mErr != nil {
				return fmt.Errorf("config: %w", mErr)
			}
			return os.WriteFile(path, append(data, '\n'), 0o644)
		}
		return fmt.Errorf("config: %w", err)
	}

	text := string(raw)
	ct := reflect.TypeOf(cfg)
	cv := reflect.ValueOf(cfg)
	var missing []string
	for _, f := range configJSONFields(ct) {
		valJSON, err := json.Marshal(cv.Field(f.Index).Interface())
		if err != nil {
			return fmt.Errorf("config: encode %s: %w", ct.Field(f.Index).Name, err)
		}
		if updated, ok := replaceJSONValue(text, f.Key, string(valJSON)); ok {
			text = updated
		} else {
			missing = append(missing, fmt.Sprintf("  %q: %s", f.Key, valJSON))
		}
	}
	if len(missing) > 0 {
		text = appendMissingKeys(text, missing)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// replaceJSONValue finds `"key": <value>[,]` in text — key matched exactly
// (its own closing quote must follow immediately, so it can't match a
// longer key that merely starts with the same text, or a "//key" doc line,
// whose quoted text is different) — and swaps in newVal, keeping the
// original indentation and trailing comma. ok is false if key isn't
// present, meaning text is returned unchanged.
func replaceJSONValue(text, key, newVal string) (result string, ok bool) {
	re := regexp.MustCompile(`(?m)^([ \t]*"` + regexp.QuoteMeta(key) + `"[ \t]*:[ \t]*)` + jsonValuePattern + `([ \t]*,?[ \t]*)$`)
	if !re.MatchString(text) {
		return text, false
	}
	return re.ReplaceAllStringFunc(text, func(m string) string {
		sub := re.FindStringSubmatch(m)
		return sub[1] + newVal + sub[2]
	}), true
}

// appendMissingKeys inserts lines (each already formatted as `  "key":
// value`, no trailing comma) just before text's closing brace, adding a
// comma after whatever line was previously last if it didn't already end
// with one.
func appendMissingKeys(text string, lines []string) string {
	idx := strings.LastIndex(text, "}")
	if idx < 0 {
		return text // not a well-formed object; leave it alone rather than guess
	}
	head := strings.TrimRight(text[:idx], " \t\r\n")
	if len(head) > 0 && head[len(head)-1] != ',' && head[len(head)-1] != '{' {
		head += ","
	}
	return head + "\n" + strings.Join(lines, ",\n") + "\n" + text[idx:]
}
