package tools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests in this file exercise the tool handlers through a real MCP
// session rather than calling the REST API directly, because the behaviour
// under test (the upsert branches, the image no-op detection) lives in the
// handlers - a direct API call would bypass exactly the code being checked.

// connectSession wires an in-memory MCP client to the tool server.
func connectSession(t *testing.T) (context.Context, *mcp.ClientSession, *client.Client) {
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

// callTool invokes a tool and returns its text content, failing the test if
// the tool reported an error.
func callTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	text := toolText(res)
	if res.IsError {
		t.Fatalf("tool %s returned an error: %s", name, text)
	}
	return text
}

// callToolExpectingError invokes a tool that is expected to fail and returns
// its error text.
func callToolExpectingError(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("expected tool %s to report an error, got: %s", name, toolText(res))
	}
	return toolText(res)
}

func toolText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func decodeInto(t *testing.T, payload string, dest any) {
	t.Helper()
	if err := json.Unmarshal([]byte(payload), dest); err != nil {
		t.Fatalf("decoding tool result: %v (payload: %.300s)", err, payload)
	}
}

// createTestPart makes a throwaway part and registers its cleanup.
func createTestPart(t *testing.T, c *client.Client, name string) tools.Part {
	t.Helper()
	var created tools.Part
	err := c.Post("/api/part/", map[string]any{
		"name":        name,
		"description": "Integration test part - safe to delete",
		"component":   true,
	}, &created)
	if err != nil {
		t.Fatalf("create part %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = c.Patch(fmt.Sprintf("/api/part/%d/", created.PK), map[string]any{"active": false}, nil)
		if err := c.Delete(fmt.Sprintf("/api/part/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete part %d: %v", created.PK, err)
		}
	})
	return created
}

// -- Parameters --

// TestSetPartParameterUpsert covers all three branches of set_part_parameter:
// creating a template on the fly, reusing an existing one, and updating a
// value the part already has.
func TestSetPartParameterUpsert(t *testing.T) {
	ctx, cs, c := connectSession(t)
	part := createTestPart(t, c, "_MCP_TEST_PARAM_DELETE_ME")

	templateName := "_MCP_TEST_Template"

	// Branch 1: template does not exist yet - it gets created.
	var first tools.Parameter
	decodeInto(t, callTool(t, ctx, cs, "set_part_parameter", map[string]any{
		"part":          part.PK,
		"template_name": templateName,
		"units":         "mm^2",
		"data":          "16",
	}), &first)

	t.Cleanup(func() {
		_ = c.Delete(fmt.Sprintf("/api/parameter/%d/", first.PK))
		_ = c.Delete(fmt.Sprintf("/api/parameter/template/%d/", first.Template))
	})

	if first.Template == 0 {
		t.Fatal("expected a template to have been created")
	}
	if first.Data != "16" {
		t.Errorf("expected data 16, got %q", first.Data)
	}
	if first.ModelID != part.PK {
		t.Errorf("expected parameter to be attached to part %d, got %d", part.PK, first.ModelID)
	}

	// Branch 2: same template name again - must reuse, not duplicate.
	var second tools.Parameter
	decodeInto(t, callTool(t, ctx, cs, "set_part_parameter", map[string]any{
		"part":          part.PK,
		"template_name": templateName,
		"data":          "25",
	}), &second)

	if second.Template != first.Template {
		t.Errorf("expected template %d to be reused, got %d", first.Template, second.Template)
	}

	// Branch 3: the value itself must have been updated in place, not added
	// alongside the old one.
	if second.PK != first.PK {
		t.Errorf("expected parameter %d to be updated in place, got a new one (%d)", first.PK, second.PK)
	}
	if second.Data != "25" {
		t.Errorf("expected data 25 after update, got %q", second.Data)
	}

	// And the part must end up with exactly one value, not two.
	var listed struct {
		Count   int               `json:"count"`
		Results []tools.Parameter `json:"results"`
	}
	decodeInto(t, callTool(t, ctx, cs, "get_part_parameters", map[string]any{"part": part.PK}), &listed)
	if listed.Count != 1 {
		t.Errorf("expected exactly 1 parameter on the part, got %d", listed.Count)
	}
	if listed.Count == 1 && listed.Results[0].TemplateDetail == nil {
		t.Error("expected template_detail to be expanded in get_part_parameters")
	}
}

func TestSetPartParameterRequiresTemplate(t *testing.T) {
	ctx, cs, c := connectSession(t)
	part := createTestPart(t, c, "_MCP_TEST_PARAM_ARGS_DELETE_ME")

	msg := callToolExpectingError(t, ctx, cs, "set_part_parameter", map[string]any{
		"part": part.PK,
		"data": "16",
	})
	if !strings.Contains(msg, "template") {
		t.Errorf("expected the error to mention the missing template, got: %s", msg)
	}
}

// -- Suppliers --

// TestSupplierPartRoundTrip covers create/get/update for a supplier link and
// both branches of the price-break upsert.
func TestSupplierPartRoundTrip(t *testing.T) {
	ctx, cs, c := connectSession(t)
	part := createTestPart(t, c, "_MCP_TEST_SUPPLIER_DELETE_ME")

	// Any supplier company will do; skip if the instance has none.
	var companies struct {
		Count   int             `json:"count"`
		Results []tools.Company `json:"results"`
	}
	decodeInto(t, callTool(t, ctx, cs, "search_companies", map[string]any{
		"is_supplier": true,
		"limit":       1,
	}), &companies)
	if companies.Count == 0 {
		t.Skip("no supplier companies on this instance")
	}
	supplier := companies.Results[0]

	var created tools.SupplierPart
	decodeInto(t, callTool(t, ctx, cs, "create_supplier_part", map[string]any{
		"part":     part.PK,
		"supplier": supplier.PK,
		"SKU":      "_MCP_TEST_SKU",
		"note":     "verfügbar (Statusflag, keine Menge)",
	}), &created)

	t.Cleanup(func() {
		if err := c.Delete(fmt.Sprintf("/api/company/part/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete supplier part %d: %v", created.PK, err)
		}
	})

	if created.SKU != "_MCP_TEST_SKU" {
		t.Errorf("expected SKU to round-trip, got %q", created.SKU)
	}

	// The link must be findable by part.
	var found struct {
		Count   int                  `json:"count"`
		Results []tools.SupplierPart `json:"results"`
	}
	decodeInto(t, callTool(t, ctx, cs, "get_supplier_parts", map[string]any{"part": part.PK}), &found)
	if found.Count != 1 || found.Results[0].PK != created.PK {
		t.Errorf("expected to find supplier part %d for part %d, got %+v", created.PK, part.PK, found.Results)
	}

	// Update a field. Note deliberately, not MPN: MPN is read-only in the
	// API and mirrors the linked manufacturer_part, so a tool that offered
	// it would appear to work and change nothing.
	var updated tools.SupplierPart
	decodeInto(t, callTool(t, ctx, cs, "update_supplier_part", map[string]any{
		"id":   created.PK,
		"note": "_MCP_TEST_NOTE",
	}), &updated)
	if updated.Note == nil || *updated.Note != "_MCP_TEST_NOTE" {
		t.Errorf("expected note to be set, got %v", updated.Note)
	}

	// Price break: create branch.
	var pb1 tools.SupplierPriceBreak
	decodeInto(t, callTool(t, ctx, cs, "set_supplier_price_break", map[string]any{
		"supplier_part": created.PK,
		"quantity":      1,
		"price":         11.55,
	}), &pb1)
	if pb1.PK == 0 {
		t.Fatal("expected a price break to have been created")
	}

	// Price break: update branch - same quantity must not create a second tier.
	var pb2 tools.SupplierPriceBreak
	decodeInto(t, callTool(t, ctx, cs, "set_supplier_price_break", map[string]any{
		"supplier_part": created.PK,
		"quantity":      1,
		"price":         10.16,
	}), &pb2)
	if pb2.PK != pb1.PK {
		t.Errorf("expected price break %d to be updated in place, got a new one (%d)", pb1.PK, pb2.PK)
	}

	var breaks struct {
		Count int `json:"count"`
	}
	decodeInto(t, callTool(t, ctx, cs, "get_supplier_price_breaks", map[string]any{
		"supplier_part": created.PK,
	}), &breaks)
	if breaks.Count != 1 {
		t.Errorf("expected exactly 1 price break after two calls at the same quantity, got %d", breaks.Count)
	}
}

func TestGetSupplierPartsRequiresAFilter(t *testing.T) {
	ctx, cs, _ := connectSession(t)
	msg := callToolExpectingError(t, ctx, cs, "get_supplier_parts", map[string]any{})
	if !strings.Contains(msg, "part") && !strings.Contains(msg, "supplier") {
		t.Errorf("expected the error to name the required filters, got: %s", msg)
	}
}

// -- Image upload --

// TestUploadPartImage covers the fallback path that exists because
// remote_image silently no-ops on instances without outbound access. The
// image is served by a local test server, so this does not depend on the
// InvenTree host being able to reach anything.
func TestUploadPartImage(t *testing.T) {
	ctx, cs, c := connectSession(t)
	part := createTestPart(t, c, "_MCP_TEST_IMAGE_DELETE_ME")

	pngBytes := onePixelPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	var updated tools.Part
	decodeInto(t, callTool(t, ctx, cs, "upload_part_image", map[string]any{
		"id":        part.PK,
		"image_url": srv.URL + "/testimage.png",
	}), &updated)

	if updated.Image == nil || *updated.Image == "" {
		t.Fatal("expected the part to have an image after upload")
	}
	t.Logf("uploaded image stored as %s", *updated.Image)
}

func TestUploadPartImageRejectsNonImages(t *testing.T) {
	ctx, cs, c := connectSession(t)
	part := createTestPart(t, c, "_MCP_TEST_IMAGE_REJECT_DELETE_ME")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png") // lies about the content
		_, _ = w.Write([]byte("<!doctype html><html><body>not an image</body></html>"))
	}))
	defer srv.Close()

	msg := callToolExpectingError(t, ctx, cs, "upload_part_image", map[string]any{
		"id":        part.PK,
		"image_url": srv.URL + "/notreally.png",
	})
	if !strings.Contains(msg, "not an image") {
		t.Errorf("expected the error to say the content is not an image, got: %s", msg)
	}
}

// onePixelPNG returns a minimal valid PNG.
func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	const b64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decoding test PNG: %v", err)
	}
	return data
}

// -- Stock history --

func TestGetStockHistory(t *testing.T) {
	ctx, cs, c := connectSession(t)

	// Find any stock item on the instance.
	var stock client.PaginatedResponse[tools.StockItem]
	if err := c.Get("/api/stock/?limit=1&format=json", &stock); err != nil {
		t.Fatalf("listing stock: %v", err)
	}
	if stock.Count == 0 {
		t.Skip("no stock items on this instance")
	}
	item := stock.Results[0]

	var history struct {
		Count   int                        `json:"count"`
		Results []tools.StockTrackingEntry `json:"results"`
	}
	decodeInto(t, callTool(t, ctx, cs, "get_stock_history", map[string]any{"item": item.PK}), &history)

	if history.Count == 0 {
		t.Skipf("stock item %d has no tracking entries", item.PK)
	}
	for _, entry := range history.Results {
		if entry.Item != item.PK {
			t.Errorf("history contains an entry for item %d, expected only %d", entry.Item, item.PK)
		}
	}
	t.Logf("stock item %d has %d tracking entries, latest: %q", item.PK, history.Count, history.Results[0].Label)
}

// TestGetStockHistoryRejectsItemZero checks the handler's own guard. Omitting
// "item" entirely is already rejected one layer up by MCP schema validation
// (the field is required), so the guard is only reachable via an explicit 0.
func TestGetStockHistoryRejectsItemZero(t *testing.T) {
	ctx, cs, _ := connectSession(t)
	msg := callToolExpectingError(t, ctx, cs, "get_stock_history", map[string]any{"item": 0})
	if !strings.Contains(msg, "item") {
		t.Errorf("expected the error to mention the required item, got: %s", msg)
	}
}

// TestGetStockHistoryRequiresItemInSchema pins the layer above: the tool's
// input schema marks item as required, so a call without it never reaches
// the handler at all.
func TestGetStockHistoryRequiresItemInSchema(t *testing.T) {
	ctx, cs, _ := connectSession(t)
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_stock_history", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("expected schema validation to reject a call without item")
	}
	if !strings.Contains(err.Error(), "item") {
		t.Errorf("expected the validation error to name item, got: %v", err)
	}
}

// -- Registration --

// TestAllToolsAreRegistered guards against adding a Register* function and
// forgetting to wire it into RegisterAll.
func TestAllToolsAreRegistered(t *testing.T) {
	if os.Getenv("INVENTREE_URL") == "" {
		t.Skip("INVENTREE_URL and INVENTREE_TOKEN must be set for integration tests")
	}
	ctx, cs, _ := connectSession(t)

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	registered := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		registered[tool.Name] = true
	}

	for _, name := range []string{
		"search_parameter_templates", "list_parameter_templates", "create_parameter_template",
		"get_part_parameters", "set_part_parameter", "delete_parameter",
		"search_companies", "get_supplier_parts", "create_supplier_part",
		"update_supplier_part", "delete_supplier_part",
		"get_supplier_price_breaks", "set_supplier_price_break",
		"get_stock_history", "upload_part_image",
	} {
		if !registered[name] {
			t.Errorf("tool %q is not registered in RegisterAll", name)
		}
	}
	t.Logf("%d tools registered", len(res.Tools))
}
