package tools

import (
	"testing"
)

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
