package tools

import (
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

// TestRemoteImageWarning pins down when the silent remote_image no-op is
// reported. This is the bug the upload_part_image fallback exists for: an
// InvenTree host without outbound internet access returns HTTP 200 and
// image: null, with no error anywhere in the response.
func TestRemoteImageWarning(t *testing.T) {
	tests := []struct {
		name         string
		requestedURL string
		image        *string
		wantWarning  bool
	}{
		{"no image requested", "", nil, false},
		{"no image requested, part already has one", "", strPtr("/media/part_images/1.jpg"), false},
		{"image requested and stored", "https://example.com/a.jpg", strPtr("/media/part_images/1.jpg"), false},
		{"image requested, silently dropped", "https://example.com/a.jpg", nil, true},
		{"image requested, empty string back", "https://example.com/a.jpg", strPtr(""), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := remoteImageWarning(tc.requestedURL, tc.image, 42)
			if tc.wantWarning && got == "" {
				t.Error("expected a warning, got none")
			}
			if !tc.wantWarning && got != "" {
				t.Errorf("expected no warning, got %q", got)
			}
		})
	}
}

func TestRemoteImageWarningNamesTheFallbackTool(t *testing.T) {
	got := remoteImageWarning("https://example.com/a.jpg", nil, 42)
	if got == "" {
		t.Fatal("expected a warning")
	}
	// The reader is an agent deciding what to do next, so the message has to
	// name the fallback tool and carry the arguments to call it with.
	for _, want := range []string{"upload_part_image", "id=42", "https://example.com/a.jpg"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning does not mention %q: %s", want, got)
		}
	}
}

func TestImageFileName(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		contentType string
		want        string
	}{
		{"plain file name", "https://example.com/media/121350.jpg", "image/jpeg", "121350.jpg"},
		{"double extension kept as-is", "https://shop.example.com/pbm_13521.jpg.jpg", "image/jpeg", "pbm_13521.jpg.jpg"},
		{"query string ignored", "https://example.com/img/photo.png?w=800", "image/png", "photo.png"},
		{"no extension gets one from the content type", "https://example.com/img/photo", "image/png", "photo.png"},
		{"empty path falls back", "https://example.com", "image/jpeg", "image.jpg"},
		// mime.ExtensionsByType would answer ".jfif" here on a stock Debian.
		{"jpeg does not become jfif", "https://example.com/img/photo", "image/jpeg", "photo.jpg"},
		{"content type parameters tolerated", "https://example.com/img/photo", "image/png; charset=binary", "photo.png"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := imageFileName(tc.url, tc.contentType)
			if got != tc.want {
				t.Errorf("imageFileName(%q, %q) = %q, want %q", tc.url, tc.contentType, got, tc.want)
			}
		})
	}
}
