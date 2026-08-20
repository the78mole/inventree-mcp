package tools_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setupServer(t *testing.T) (*mcp.Server, *client.Client) {
	t.Helper()
	url := os.Getenv("INVENTREE_URL")
	token := os.Getenv("INVENTREE_TOKEN")
	if url == "" || token == "" {
		t.Skip("INVENTREE_URL and INVENTREE_TOKEN must be set for integration tests")
	}

	c := client.New(url, token)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	registry := tools.RegisterAll(server, c, nil)
	server.AddReceivingMiddleware(registry.Middleware())
	return server, c
}

// These tests run against whatever InvenTree instance the environment points
// at, so they must not assume any particular dataset. The helpers below
// discover a fixture from the instance itself and skip the test when the
// instance genuinely has nothing to work with.

// anyPart returns an arbitrary existing part.
func anyPart(t *testing.T, c *client.Client) tools.Part {
	t.Helper()
	var resp client.PaginatedResponse[tools.Part]
	if err := c.Get("/api/part/?limit=1&format=json", &resp); err != nil {
		t.Fatalf("list parts: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Skip("instance has no parts to search for")
	}
	return resp.Results[0]
}

// anyLocation returns an arbitrary existing stock location.
func anyLocation(t *testing.T, c *client.Client) tools.StockLocation {
	t.Helper()
	var resp client.PaginatedResponse[tools.StockLocation]
	if err := c.Get("/api/stock/location/?limit=1&format=json", &resp); err != nil {
		t.Fatalf("list locations: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Skip("instance has no stock locations to search for")
	}
	return resp.Results[0]
}

// searchWord derives a search term from a fixture's name: the longest run of
// letters or digits, lowercased so the search also has to be case-insensitive.
// Returns "" when the name holds nothing usable.
func searchWord(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	best := ""
	for _, w := range words {
		if len([]rune(w)) > len([]rune(best)) {
			best = w
		}
	}
	if len([]rune(best)) < 3 {
		return ""
	}
	return strings.ToLower(best)
}

// innerFragment cuts the first and last rune off a word, yielding a fragment
// that matches neither as a prefix nor as a suffix - the point being to prove
// that search really is a substring match. Returns "" if too little is left.
func innerFragment(word string) string {
	r := []rune(word)
	if len(r) < 5 {
		return ""
	}
	return string(r[1 : len(r)-1])
}

// searchCount runs a search against endpoint and reports how many results the
// instance claims.
func searchCount[T any](t *testing.T, c *client.Client, endpoint, term string) (int, []T) {
	t.Helper()
	var resp client.PaginatedResponse[T]
	path := fmt.Sprintf("%s?search=%s&limit=10&format=json", endpoint, url.QueryEscape(term))
	if err := c.Get(path, &resp); err != nil {
		t.Fatalf("search %s for %q: %v", endpoint, term, err)
	}
	return resp.Count, resp.Results
}

// uniqueName keeps the "_MCP_TEST_..._DELETE_ME" convention but adds a
// suffix, so a re-run after a failed cleanup cannot trip over its own
// leftovers ("Duplicate names cannot exist under the same parent").
func uniqueName(prefix string) string {
	return fmt.Sprintf("_MCP_TEST_%s_%d_DELETE_ME", prefix, time.Now().UnixNano())
}

// deleteWithBody issues a DELETE carrying a JSON body. InvenTree requires
// explicit confirmation flags when deleting a stock location or a part
// category (delete_sub_locations, delete_parts, ...) and rejects the request
// with a 400 without them; client.Delete sends no body. Older InvenTree
// releases ignore the extra fields, so this is safe either way.
func deleteWithBody(c *client.Client, path string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling delete body: %w", err)
	}
	resp, err := c.Do(http.MethodDelete, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DELETE %s: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

// deleteLocation removes a stock location, declining to cascade so that a
// buggy test cannot take unrelated data with it.
func deleteLocation(c *client.Client, pk int) error {
	return deleteWithBody(c, fmt.Sprintf("/api/stock/location/%d/", pk), map[string]any{
		"delete_stock_items":   false,
		"delete_sub_locations": false,
	})
}

// deleteCategory removes a part category, likewise without cascading.
func deleteCategory(c *client.Client, pk int) error {
	return deleteWithBody(c, fmt.Sprintf("/api/part/category/%d/", pk), map[string]any{
		"delete_parts":            false,
		"delete_child_categories": false,
	})
}

// createTempLocation makes a throwaway stock location and registers its
// deletion. It is created at the top level so it is guaranteed non-structural
// and therefore able to hold stock.
func createTempLocation(t *testing.T, c *client.Client) tools.StockLocation {
	t.Helper()
	var created tools.StockLocation
	err := c.Post("/api/stock/location/", map[string]any{
		"name":        uniqueName("STOCK_LOC"),
		"description": "Integration test location - safe to delete",
	}, &created)
	if err != nil {
		t.Fatalf("create temp location: %v", err)
	}
	t.Cleanup(func() {
		t.Logf("Deleting temp location [%d]", created.PK)
		if err := deleteLocation(c, created.PK); err != nil {
			t.Errorf("cleanup - delete location %d: %v", created.PK, err)
		}
	})
	return created
}

// TestSearchParts tests searching for parts.
func TestSearchParts(t *testing.T) {
	_, c := setupServer(t)

	seed := anyPart(t, c)
	term := searchWord(seed.Name)
	if term == "" {
		t.Skipf("part [%d] %q has no word usable as a search term", seed.PK, seed.Name)
	}

	count, results := searchCount[tools.Part](t, c, "/api/part/", term)
	if count == 0 {
		t.Fatalf("expected at least one result for %q, taken from part [%d] %q", term, seed.PK, seed.Name)
	}
	t.Logf("Found %d parts matching %q", count, term)
	for _, p := range results {
		t.Logf("  - [%d] %s (category: %s, in_stock: %.0f)", p.PK, p.Name, p.CategoryName, p.InStock)
	}
}

// TestSearchLocations tests location search with partial matching.
func TestSearchLocations(t *testing.T) {
	_, c := setupServer(t)

	seed := anyLocation(t, c)
	term := searchWord(seed.Name)
	if term == "" {
		t.Skipf("location [%d] %q has no word usable as a search term", seed.PK, seed.Name)
	}

	count, results := searchCount[tools.StockLocation](t, c, "/api/stock/location/", term)
	if count == 0 {
		t.Fatalf("expected at least one result for %q, taken from location [%d] %q", term, seed.PK, seed.Name)
	}
	t.Logf("Found %d locations matching %q", count, term)
	for _, l := range results {
		t.Logf("  - [%d] %s (path: %s)", l.PK, l.Name, l.PathString)
	}
}

// TestSearchLocationsFuzzy tests that location search matches approximate
// names: a fragment from the middle of a name (neither prefix nor suffix)
// must find it, and the match must be case-insensitive.
func TestSearchLocationsFuzzy(t *testing.T) {
	_, c := setupServer(t)

	seed := anyLocation(t, c)
	word := searchWord(seed.Name)
	fragment := innerFragment(word)
	if fragment == "" {
		t.Skipf("location [%d] %q is too short to derive a partial search term", seed.PK, seed.Name)
	}

	count, results := searchCount[tools.StockLocation](t, c, "/api/stock/location/", fragment)
	if count == 0 {
		t.Fatalf("expected at least one result for partial term %q, taken from location [%d] %q",
			fragment, seed.PK, seed.Name)
	}
	t.Logf("Found %d locations matching partial term %q: %s", count, fragment, results[0].Name)

	// The same fragment in a different case must behave identically.
	upperCount, _ := searchCount[tools.StockLocation](t, c, "/api/stock/location/", strings.ToUpper(fragment))
	if upperCount != count {
		t.Errorf("search is case-sensitive: %q returned %d results, %q returned %d",
			fragment, count, strings.ToUpper(fragment), upperCount)
	}
}

// TestListCategories tests listing part categories.
func TestListCategories(t *testing.T) {
	_, c := setupServer(t)

	var resp client.PaginatedResponse[tools.PartCategory]
	err := c.Get("/api/part/category/?limit=50&format=json", &resp)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least one category")
	}
	t.Logf("Found %d categories", resp.Count)
	for _, cat := range resp.Results {
		t.Logf("  - [%d] %s (path: %s, parts: %d)", cat.PK, cat.Name, cat.PathString, cat.PartCount)
	}
}

// TestCreateAndDeletePart tests the full lifecycle: create part → add stock → delete stock → delete part.
func TestCreateAndDeletePart(t *testing.T) {
	_, c := setupServer(t)

	// 1. Create a test part
	partName := uniqueName("PART")
	var created tools.Part
	err := c.Post("/api/part/", map[string]any{
		"name":        partName,
		"description": "Integration test part - safe to delete",
		"component":   true,
	}, &created)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	t.Logf("Created part: [%d] %s", created.PK, created.Name)

	// Cleanup: always delete the part at the end
	defer func() {
		// Delete any stock items for this part first
		var stockResp client.PaginatedResponse[tools.StockItem]
		if err := c.Get(fmt.Sprintf("/api/stock/?part=%d&limit=50&format=json", created.PK), &stockResp); err == nil {
			for _, s := range stockResp.Results {
				t.Logf("Cleaning up stock item [%d]", s.PK)
				_ = c.Delete(fmt.Sprintf("/api/stock/%d/", s.PK))
			}
		}
		// Must deactivate before deleting
		t.Logf("Deactivating part [%d]", created.PK)
		_ = c.Patch(fmt.Sprintf("/api/part/%d/", created.PK), map[string]any{"active": false}, nil)
		t.Logf("Deleting part [%d]", created.PK)
		if err := c.Delete(fmt.Sprintf("/api/part/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete part: %v", err)
		}
	}()

	// 2. Verify we can find it by search
	var searchResp client.PaginatedResponse[tools.Part]
	err = c.Get(fmt.Sprintf("/api/part/?search=%s&limit=25&format=json", url.QueryEscape(partName)), &searchResp)
	if err != nil {
		t.Fatalf("search for test part: %v", err)
	}
	if searchResp.Count == 0 {
		t.Fatal("created part not found via search")
	}
	t.Logf("Found test part via search: %s", searchResp.Results[0].Name)

	// 3. Add stock for the part in a location created for this test, so the
	// test does not depend on any particular location existing.
	var stockItems []tools.StockItem
	location := createTempLocation(t, c).PK
	err = c.Post("/api/stock/", map[string]any{
		"part":     created.PK,
		"quantity": 5,
		"location": location,
	}, &stockItems)
	if err != nil {
		t.Fatalf("add stock: %v", err)
	}
	if len(stockItems) == 0 {
		t.Fatal("no stock items returned from creation")
	}
	stockItem := stockItems[0]
	t.Logf("Added stock item [%d]: qty=%.0f at location %d", stockItem.PK, stockItem.Quantity, location)

	// 4. Verify stock by querying
	var stockResp client.PaginatedResponse[tools.StockItem]
	err = c.Get(fmt.Sprintf("/api/stock/?part=%d&limit=50&format=json", created.PK), &stockResp)
	if err != nil {
		t.Fatalf("get stock: %v", err)
	}
	if stockResp.Count == 0 {
		t.Fatal("no stock found for test part")
	}
	t.Logf("Found %d stock items for test part", stockResp.Count)

	// 5. Delete the stock item
	err = c.Delete(fmt.Sprintf("/api/stock/%d/", stockItem.PK))
	if err != nil {
		t.Fatalf("delete stock: %v", err)
	}
	t.Logf("Deleted stock item [%d]", stockItem.PK)

	// Part deletion happens in defer
}

// TestPartTags covers the `tags` field on create and update, including the two
// InvenTree quirks the README documents: tags are absent from a GET response,
// and they are only discoverable through the full-text search.
func TestPartTags(t *testing.T) {
	_, c := setupServer(t)

	var created tools.Part
	err := c.Post("/api/part/", map[string]any{
		"name":        "_MCP_TEST_TAGS_DELETE_ME",
		"description": "Integration test part for tags - safe to delete",
		"component":   true,
		"tags":        []string{"mcptesttag"},
	}, &created)
	if err != nil {
		t.Fatalf("create part with tags: %v", err)
	}
	t.Logf("Created part: [%d] %s tags=%v", created.PK, created.Name, created.Tags)

	defer func() {
		_ = c.Patch(fmt.Sprintf("/api/part/%d/", created.PK), map[string]any{"active": false}, nil)
		if err := c.Delete(fmt.Sprintf("/api/part/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete part: %v", err)
		}
	}()

	// The POST response carries the tags back...
	if len(created.Tags) != 1 || created.Tags[0] != "mcptesttag" {
		t.Fatalf("expected tags [mcptesttag] in create response, got %v", created.Tags)
	}

	// ...but a GET does not return the field at all.
	var fetched tools.Part
	if err := c.Get(fmt.Sprintf("/api/part/%d/?format=json", created.PK), &fetched); err != nil {
		t.Fatalf("get part: %v", err)
	}
	if len(fetched.Tags) != 0 {
		t.Logf("note: GET returned tags=%v - InvenTree may have started serialising them, "+
			"the README caveat can be relaxed", fetched.Tags)
	}

	// Tags are reachable through the full-text search.
	var searchResp client.PaginatedResponse[tools.Part]
	if err := c.Get("/api/part/?search=mcptesttag&limit=25&format=json", &searchResp); err != nil {
		t.Fatalf("search by tag: %v", err)
	}
	found := false
	for _, p := range searchResp.Results {
		if p.PK == created.PK {
			found = true
		}
	}
	if !found {
		t.Errorf("part [%d] not found when searching for its tag", created.PK)
	}

	// An update replaces the tag list rather than appending to it.
	var updated tools.Part
	err = c.Patch(fmt.Sprintf("/api/part/%d/", created.PK),
		map[string]any{"tags": []string{"mcpothertag"}}, &updated)
	if err != nil {
		t.Fatalf("update tags: %v", err)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "mcpothertag" {
		t.Errorf("expected tags to be replaced by [mcpothertag], got %v", updated.Tags)
	}
}

// TestCreateAndDeleteLocation tests location lifecycle.
func TestCreateAndDeleteLocation(t *testing.T) {
	_, c := setupServer(t)

	// Nest the test location under whichever location the instance already
	// has; if it has none, create it at the top level.
	locName := uniqueName("LOC")
	payload := map[string]any{
		"name":        locName,
		"description": "Integration test location - safe to delete",
	}
	var existing client.PaginatedResponse[tools.StockLocation]
	if err := c.Get("/api/stock/location/?limit=1&format=json", &existing); err != nil {
		t.Fatalf("list locations: %v", err)
	}
	if len(existing.Results) > 0 {
		payload["parent"] = existing.Results[0].PK
	}

	var created tools.StockLocation
	err := c.Post("/api/stock/location/", payload, &created)
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	t.Logf("Created location: [%d] %s (path: %s)", created.PK, created.Name, created.PathString)

	defer func() {
		t.Logf("Deleting location [%d]", created.PK)
		if err := deleteLocation(c, created.PK); err != nil {
			t.Errorf("cleanup - delete location: %v", err)
		}
	}()

	// Search should find it
	var searchResp client.PaginatedResponse[tools.StockLocation]
	err = c.Get(fmt.Sprintf("/api/stock/location/?search=%s&limit=25&format=json", url.QueryEscape(locName)), &searchResp)
	if err != nil {
		t.Fatalf("search for test location: %v", err)
	}
	if searchResp.Count == 0 {
		t.Fatal("created location not found via search")
	}
	t.Logf("Found test location via search: %s", searchResp.Results[0].PathString)
}

// TestCreateAndDeleteCategory tests category lifecycle.
func TestCreateAndDeleteCategory(t *testing.T) {
	_, c := setupServer(t)

	// Nest the test category under whichever category the instance already
	// has; if it has none, create it at the top level.
	catName := uniqueName("CAT")
	payload := map[string]any{
		"name":        catName,
		"description": "Integration test category - safe to delete",
	}
	var existing client.PaginatedResponse[tools.PartCategory]
	if err := c.Get("/api/part/category/?limit=1&format=json", &existing); err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(existing.Results) > 0 {
		payload["parent"] = existing.Results[0].PK
	}

	var created tools.PartCategory
	err := c.Post("/api/part/category/", payload, &created)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	t.Logf("Created category: [%d] %s (path: %s)", created.PK, created.Name, created.PathString)

	defer func() {
		t.Logf("Deleting category [%d]", created.PK)
		if err := deleteCategory(c, created.PK); err != nil {
			t.Errorf("cleanup - delete category: %v", err)
		}
	}()

	// Search should find it
	var searchResp client.PaginatedResponse[tools.PartCategory]
	err = c.Get(fmt.Sprintf("/api/part/category/?search=%s&limit=25&format=json", url.QueryEscape(catName)), &searchResp)
	if err != nil {
		t.Fatalf("search for test category: %v", err)
	}
	if searchResp.Count == 0 {
		t.Fatal("created category not found via search")
	}
	t.Logf("Found test category via search: %s", searchResp.Results[0].PathString)
}
