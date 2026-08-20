package tools_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests in this file drive delete_stock_location and delete_part_category
// through a real MCP session rather than calling the REST API directly: the
// behaviour under test is the confirmation body the handlers now send and the
// summary they build from it, and a direct API call would bypass both.

// deleteToolSession wires an in-memory MCP client to the tool server.
func deleteToolSession(t *testing.T) (context.Context, *mcp.ClientSession, *client.Client) {
	t.Helper()
	server, c := setupServer(t)

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	clientSession, err := mcpClient.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return ctx, clientSession, c
}

// callDeleteTool invokes a tool and returns its text content, failing the test
// if the tool reported an error.
func callDeleteTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	var sb strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if res.IsError {
		t.Fatalf("tool %s returned an error: %s", name, sb.String())
	}
	return sb.String()
}

// deleteFixtureName keeps the "_MCP_TEST_..._DELETE_ME" convention and adds a
// suffix so a run that dies before its cleanup cannot poison the next one.
func deleteFixtureName(prefix string) string {
	return fmt.Sprintf("_MCP_TEST_%s_%d_DELETE_ME", prefix, time.Now().UnixNano())
}

// makeLocation creates a stock location, optionally under a parent.
func makeLocation(t *testing.T, c *client.Client, prefix string, parent *int) tools.StockLocation {
	t.Helper()
	payload := map[string]any{"name": deleteFixtureName(prefix)}
	if parent != nil {
		payload["parent"] = *parent
	}
	var created tools.StockLocation
	if err := c.Post("/api/stock/location/", payload, &created); err != nil {
		t.Fatalf("create location %s: %v", prefix, err)
	}
	t.Cleanup(func() { _ = deleteLocationFixture(c, created.PK) })
	return created
}

// makeCategory creates a part category, optionally under a parent.
func makeCategory(t *testing.T, c *client.Client, prefix string, parent *int) tools.PartCategory {
	t.Helper()
	payload := map[string]any{
		"name":        deleteFixtureName(prefix),
		"description": "Integration test category - safe to delete",
	}
	if parent != nil {
		payload["parent"] = *parent
	}
	var created tools.PartCategory
	if err := c.Post("/api/part/category/", payload, &created); err != nil {
		t.Fatalf("create category %s: %v", prefix, err)
	}
	t.Cleanup(func() { _ = deleteCategoryFixture(c, created.PK) })
	return created
}

// makePart creates a part, optionally in a category.
func makePart(t *testing.T, c *client.Client, prefix string, category *int) tools.Part {
	t.Helper()
	payload := map[string]any{
		"name":        deleteFixtureName(prefix),
		"description": "Integration test part - safe to delete",
		"component":   true,
	}
	if category != nil {
		payload["category"] = *category
	}
	var created tools.Part
	if err := c.Post("/api/part/", payload, &created); err != nil {
		t.Fatalf("create part %s: %v", prefix, err)
	}
	t.Cleanup(func() {
		_ = c.Patch(fmt.Sprintf("/api/part/%d/", created.PK), map[string]any{"active": false}, nil)
		_ = c.Delete(fmt.Sprintf("/api/part/%d/", created.PK))
	})
	return created
}

// makeStockItem puts quantity of part into location.
func makeStockItem(t *testing.T, c *client.Client, part, location int, quantity float64) tools.StockItem {
	t.Helper()
	var items []tools.StockItem
	err := c.Post("/api/stock/", map[string]any{
		"part":     part,
		"quantity": quantity,
		"location": location,
	}, &items)
	if err != nil {
		t.Fatalf("create stock item: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no stock item returned from creation")
	}
	t.Cleanup(func() { _ = c.Delete(fmt.Sprintf("/api/stock/%d/", items[0].PK)) })
	return items[0]
}

// The fixture cleanups swallow errors: by the time they run the object under
// test has usually been deleted by the test itself, and a 404 is the expected
// outcome rather than a failure.
func deleteLocationFixture(c *client.Client, pk int) error {
	return c.DeleteWithBody(fmt.Sprintf("/api/stock/location/%d/", pk), map[string]any{
		"delete_stock_items":   false,
		"delete_sub_locations": false,
	})
}

func deleteCategoryFixture(c *client.Client, pk int) error {
	return c.DeleteWithBody(fmt.Sprintf("/api/part/category/%d/", pk), map[string]any{
		"delete_parts":            false,
		"delete_child_categories": false,
	})
}

// gone reports whether a detail endpoint 404s.
func gone(c *client.Client, path string) bool {
	return c.Get(path, &struct{}{}) != nil
}

// TestDeleteStockLocationMovesContents is the regression test for the bug this
// file was added for: a body-less DELETE is rejected with
// {"delete_stock_items":["This field is required."]}, so the tool could not
// delete anything at all. It also pins the default behaviour - contents are
// moved to the parent, not deleted.
func TestDeleteStockLocationMovesContents(t *testing.T) {
	ctx, cs, c := deleteToolSession(t)

	parent := makeLocation(t, c, "DELLOC_PARENT", nil)
	child := makeLocation(t, c, "DELLOC_CHILD", &parent.PK)
	grandchild := makeLocation(t, c, "DELLOC_GRANDCHILD", &child.PK)
	part := makePart(t, c, "DELLOC_PART", nil)
	item := makeStockItem(t, c, part.PK, child.PK, 3)

	msg := callDeleteTool(t, ctx, cs, "delete_stock_location", map[string]any{"id": child.PK})
	t.Logf("delete_stock_location said: %s", msg)

	if !gone(c, fmt.Sprintf("/api/stock/location/%d/?format=json", child.PK)) {
		t.Fatalf("location %d still exists after delete", child.PK)
	}

	// The stock item and the sub-location must have moved up, not vanished.
	var movedItem tools.StockItem
	if err := c.Get(fmt.Sprintf("/api/stock/%d/?format=json", item.PK), &movedItem); err != nil {
		t.Fatalf("stock item %d was deleted, expected it to move to the parent: %v", item.PK, err)
	}
	if movedItem.Location == nil || *movedItem.Location != parent.PK {
		t.Errorf("expected stock item to move to parent location %d, got %v", parent.PK, movedItem.Location)
	}

	var movedLoc tools.StockLocation
	if err := c.Get(fmt.Sprintf("/api/stock/location/%d/?format=json", grandchild.PK), &movedLoc); err != nil {
		t.Fatalf("sub-location %d was deleted, expected it to move to the parent: %v", grandchild.PK, err)
	}
	if movedLoc.Parent == nil || *movedLoc.Parent != parent.PK {
		t.Errorf("expected sub-location to move to parent %d, got %v", parent.PK, movedLoc.Parent)
	}

	// The caller must be told the contents moved rather than left to assume
	// the location was empty.
	if !strings.Contains(msg, "Moved to the parent location") {
		t.Errorf("expected the result to report the moved contents, got: %s", msg)
	}
	if !strings.Contains(msg, "1 stock item") || !strings.Contains(msg, "1 sub-location") {
		t.Errorf("expected the result to count both kinds of content, got: %s", msg)
	}
}

// TestDeleteStockLocationDeletesContents covers the opt-in cascade.
func TestDeleteStockLocationDeletesContents(t *testing.T) {
	ctx, cs, c := deleteToolSession(t)

	loc := makeLocation(t, c, "DELLOC_CASCADE", nil)
	part := makePart(t, c, "DELLOC_CASCADE_PART", nil)
	item := makeStockItem(t, c, part.PK, loc.PK, 2)

	msg := callDeleteTool(t, ctx, cs, "delete_stock_location", map[string]any{
		"id":                 loc.PK,
		"delete_stock_items": true,
	})
	t.Logf("delete_stock_location said: %s", msg)

	if !gone(c, fmt.Sprintf("/api/stock/%d/?format=json", item.PK)) {
		t.Errorf("stock item %d survived delete_stock_items=true", item.PK)
	}
	if !strings.Contains(msg, "Deleted with it") {
		t.Errorf("expected the result to report the deleted contents, got: %s", msg)
	}
	// The part itself is not stock and must be left alone.
	if gone(c, fmt.Sprintf("/api/part/%d/?format=json", part.PK)) {
		t.Errorf("part %d was deleted along with its stock", part.PK)
	}
}

// TestDeletePartCategoryMovesContents is the category half of the regression,
// and pins the same default: parts move up rather than being deleted.
func TestDeletePartCategoryMovesContents(t *testing.T) {
	ctx, cs, c := deleteToolSession(t)

	parent := makeCategory(t, c, "DELCAT_PARENT", nil)
	child := makeCategory(t, c, "DELCAT_CHILD", &parent.PK)
	grandchild := makeCategory(t, c, "DELCAT_GRANDCHILD", &child.PK)
	part := makePart(t, c, "DELCAT_PART", &child.PK)

	msg := callDeleteTool(t, ctx, cs, "delete_part_category", map[string]any{"id": child.PK})
	t.Logf("delete_part_category said: %s", msg)

	if !gone(c, fmt.Sprintf("/api/part/category/%d/?format=json", child.PK)) {
		t.Fatalf("category %d still exists after delete", child.PK)
	}

	var movedPart tools.Part
	if err := c.Get(fmt.Sprintf("/api/part/%d/?format=json", part.PK), &movedPart); err != nil {
		t.Fatalf("part %d was deleted, expected it to move to the parent category: %v", part.PK, err)
	}
	if movedPart.Category == nil || *movedPart.Category != parent.PK {
		t.Errorf("expected part to move to parent category %d, got %v", parent.PK, movedPart.Category)
	}

	var movedCat tools.PartCategory
	if err := c.Get(fmt.Sprintf("/api/part/category/%d/?format=json", grandchild.PK), &movedCat); err != nil {
		t.Fatalf("sub-category %d was deleted, expected it to move to the parent: %v", grandchild.PK, err)
	}
	if movedCat.Parent == nil || *movedCat.Parent != parent.PK {
		t.Errorf("expected sub-category to move to parent %d, got %v", parent.PK, movedCat.Parent)
	}

	if !strings.Contains(msg, "Moved to the parent category") {
		t.Errorf("expected the result to report the moved contents, got: %s", msg)
	}
	if !strings.Contains(msg, "1 part") || !strings.Contains(msg, "1 sub-category") {
		t.Errorf("expected the result to count both kinds of content, got: %s", msg)
	}
}

// TestDeletePartCategoryDeletesContents covers the opt-in cascade. Note that
// InvenTree deletes the part even though it is still active, which delete_part
// itself refuses to do - the tool description says so.
func TestDeletePartCategoryDeletesContents(t *testing.T) {
	ctx, cs, c := deleteToolSession(t)

	cat := makeCategory(t, c, "DELCAT_CASCADE", nil)
	part := makePart(t, c, "DELCAT_CASCADE_PART", &cat.PK)

	msg := callDeleteTool(t, ctx, cs, "delete_part_category", map[string]any{
		"id":           cat.PK,
		"delete_parts": true,
	})
	t.Logf("delete_part_category said: %s", msg)

	if !gone(c, fmt.Sprintf("/api/part/%d/?format=json", part.PK)) {
		t.Errorf("part %d survived delete_parts=true", part.PK)
	}
	if !strings.Contains(msg, "Deleted with it") {
		t.Errorf("expected the result to report the deleted contents, got: %s", msg)
	}
}

// TestDeleteToolsRejectMissingObject checks the pre-delete lookup produces a
// clear error rather than a confusing one from the delete itself.
func TestDeleteToolsRejectMissingObject(t *testing.T) {
	ctx, cs, _ := deleteToolSession(t)

	for _, tc := range []struct{ tool, want string }{
		{"delete_stock_location", "looking up location"},
		{"delete_part_category", "looking up category"},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name:      tc.tool,
			Arguments: map[string]any{"id": 999999999},
		})
		if err != nil {
			t.Fatalf("calling %s: %v", tc.tool, err)
		}
		if !res.IsError {
			t.Errorf("expected %s to report an error for a missing object", tc.tool)
			continue
		}
		var sb strings.Builder
		for _, content := range res.Content {
			if tcnt, ok := content.(*mcp.TextContent); ok {
				sb.WriteString(tcnt.Text)
			}
		}
		if !strings.Contains(sb.String(), tc.want) {
			t.Errorf("expected %s error to mention %q, got: %s", tc.tool, tc.want, sb.String())
		}
	}
}
