package tools

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// apiErrorFor builds the error shape internal/client produces for a rejected
// write, so the tests match against the real text rather than an idealised one.
func apiErrorFor(body string) error {
	return fmt.Errorf("API error 400: %s", body)
}

// TestExplainQuerysetError covers InvenTree reporting a part that exists
// but sits outside an endpoint's filtered queryset as if it were missing.
func TestExplainQuerysetError(t *testing.T) {
	// The body as it arrives over the wire: JSON, so the quotes around the pk
	// are backslash-escaped inside the string.
	filtered := apiErrorFor(`{"part":["Invalid pk \"239\" - object does not exist."]}`)

	tests := []struct {
		name     string
		err      error
		field    string
		pk       int
		wantHint bool
	}{
		{"part outside the queryset", filtered, "part", 239, true},
		{"pk of a different object", filtered, "part", 240, false},
		{"different field rejected", filtered, "manufacturer", 239, false},
		{"unrelated validation error", apiErrorFor(`{"MPN":["This field may not be blank."]}`), "part", 239, false},
		{"other complaint about the same field", apiErrorFor(`{"part":["This field is required."]}`), "part", 239, false},
		{"no error", nil, "part", 239, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := explainQuerysetError(tc.err, tc.field, tc.pk, "must be purchaseable")
			if tc.err == nil {
				if got != nil {
					t.Fatalf("expected nil error, got %v", got)
				}
				return
			}
			hinted := strings.Contains(got.Error(), "must be purchaseable")
			if hinted != tc.wantHint {
				t.Errorf("hint present = %v, want %v: %s", hinted, tc.wantHint, got)
			}
			// The API's own message must survive either way - the pk really
			// can be wrong, and then the hint would be the misleading part.
			if !errors.Is(got, tc.err) {
				t.Errorf("original error was dropped: %s", got)
			}
			if !strings.Contains(got.Error(), "API error 400") {
				t.Errorf("original API text was dropped: %s", got)
			}
		})
	}
}

// The message is read by an agent deciding what to do next, so it has to name
// the fix rather than just stating that something is wrong.
func TestExplainQuerysetErrorNamesTheFix(t *testing.T) {
	err := explainQuerysetError(
		apiErrorFor(`{"part":["Invalid pk \"239\" - object does not exist."]}`),
		"part", 239,
		"call update_part with purchaseable=true first")
	for _, want := range []string{"part 239", "update_part with purchaseable=true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not mention %q: %s", want, err)
		}
	}
}

// A body that already went through an unescaping step must be recognised too:
// the pk quotes lose their backslashes, the field key does not.
func TestExplainQuerysetErrorUnescapedBody(t *testing.T) {
	err := explainQuerysetError(
		apiErrorFor(`{"part": ["Invalid pk "239" - object does not exist."]}`),
		"part", 239, "must be purchaseable")
	if !strings.Contains(err.Error(), "must be purchaseable") {
		t.Errorf("expected the hint on an unescaped body: %s", err)
	}
}

// create_manufacturer_part asks about its part and its manufacturer in turn.
// When both carry the same pk number, the second question must not latch onto
// the wording of the first answer.
func TestExplainQuerysetErrorHintsOnlyOnce(t *testing.T) {
	err := apiErrorFor(`{"part":["Invalid pk \"7\" - object does not exist."]}`)
	err = explainQuerysetError(err, "part", 7, "part must be purchaseable")
	err = explainQuerysetError(err, "manufacturer", 7, "company must have is_manufacturer")

	if strings.Contains(err.Error(), "company must have is_manufacturer") {
		t.Errorf("second hint was added to an error about the part field: %s", err)
	}
	if !strings.Contains(err.Error(), "part must be purchaseable") {
		t.Errorf("first hint was lost: %s", err)
	}
}

// The filter form of the error, which a GET produces for a part outside the
// endpoint's queryset. It names no pk at all, so the pk being asked about is
// the one the caller filtered by.
func TestExplainQuerysetErrorFilterForm(t *testing.T) {
	err := explainQuerysetError(
		fmt.Errorf("listing sale price breaks for part 253: %w",
			apiErrorFor(`{"part":["Select a valid choice. That choice is not one of the available choices."]}`)),
		"part", 253, "call update_part with salable=true first")
	if !strings.Contains(err.Error(), "update_part with salable=true") {
		t.Errorf("expected the hint on a filter rejection: %s", err)
	}
}

// The word "part" appears in the surrounding error text of every supplier and
// manufacturer tool, so attribution has to key on the JSON field name.
func TestExplainQuerysetErrorIgnoresProseMatches(t *testing.T) {
	err := explainQuerysetError(
		fmt.Errorf("listing supplier parts: %w",
			apiErrorFor(`{"supplier":["Select a valid choice. That choice is not one of the available choices."]}`)),
		"part", 7, "part must be purchaseable")
	if strings.Contains(err.Error(), "must be purchaseable") {
		t.Errorf("hint was attached to an error about a different field: %s", err)
	}
}
