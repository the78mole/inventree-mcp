package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Company is a supplier, manufacturer and/or customer.
type Company struct {
	PK             int    `json:"pk"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	IsSupplier     bool   `json:"is_supplier"`
	IsManufacturer bool   `json:"is_manufacturer"`
	IsCustomer     bool   `json:"is_customer"`
	Active         bool   `json:"active"`
}

// SupplierPart links a Part to a Company under that supplier's own SKU.
//
// MPN is read-only in the API (verified via OPTIONS): it mirrors the MPN of
// whatever manufacturer_part the supplier part is linked to. Writing it is
// accepted and silently ignored, so set manufacturer_part instead.
type SupplierPart struct {
	PK                  int      `json:"pk"`
	Part                int      `json:"part"`
	Supplier            int      `json:"supplier"`
	SKU                 string   `json:"SKU"`
	MPN                 *string  `json:"MPN"`
	Link                *string  `json:"link"`
	Note                *string  `json:"note"`
	Notes               *string  `json:"notes"`
	Description         *string  `json:"description"`
	Available           float64  `json:"available"`
	AvailabilityUpdated *string  `json:"availability_updated"`
	InStock             float64  `json:"in_stock"`
	OnOrder             float64  `json:"on_order"`
	Active              bool     `json:"active"`
	Packaging           *string  `json:"packaging"`
	PackQuantity        string   `json:"pack_quantity"`
	Tags                []string `json:"tags"`
}

// ManufacturerPart links a Part to the Company that makes it, under that
// manufacturer's own part number (MPN).
//
// A SupplierPart points at a ManufacturerPart rather than carrying an MPN of
// its own, so this is what has to exist first when a supplier's listing should
// show a manufacturer part number.
type ManufacturerPart struct {
	PK           int      `json:"pk"`
	Part         int      `json:"part"`
	Manufacturer int      `json:"manufacturer"`
	MPN          *string  `json:"MPN"`
	Description  *string  `json:"description"`
	Link         *string  `json:"link"`
	Notes        *string  `json:"notes"`
	Tags         []string `json:"tags"`
}

// SupplierPriceBreak is a quantity/price tier for one SupplierPart.
type SupplierPriceBreak struct {
	PK       int     `json:"pk"`
	Part     int     `json:"part"` // SupplierPart pk, NOT Part pk — InvenTree's own naming
	Quantity float64 `json:"quantity"`
	// Price arrives as a JSON string in some InvenTree versions and a number
	// in others, so it is decoded loosely and echoed back as received.
	Price         any    `json:"price"`
	PriceCurrency string `json:"price_currency"`
	Supplier      int    `json:"supplier"`
	Updated       string `json:"updated"`
}

// -- Search Companies --

type SearchCompaniesInput struct {
	Search         string `json:"search,omitempty" jsonschema:"Search query to find companies by name. Omit to list all."`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Filter to companies flagged as suppliers"`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Filter to companies flagged as manufacturers"`
	IsCustomer     *bool  `json:"is_customer,omitempty" jsonschema:"Filter to companies flagged as customers"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 25)"`
}

func RegisterSearchCompanies(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "search_companies",
		Description: "Search companies (suppliers, manufacturers, customers) by name, with optional role filters. " +
			"Use this to resolve a supplier's name to the company ID that create_supplier_part needs.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCompaniesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/company/?limit=%d&format=json", limit)
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}
		if input.IsSupplier != nil {
			path += fmt.Sprintf("&is_supplier=%t", *input.IsSupplier)
		}
		if input.IsManufacturer != nil {
			path += fmt.Sprintf("&is_manufacturer=%t", *input.IsManufacturer)
		}
		if input.IsCustomer != nil {
			path += fmt.Sprintf("&is_customer=%t", *input.IsCustomer)
		}

		var resp client.PaginatedResponse[Company]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching companies: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{"count": resp.Count, "results": resp.Results})
	})
}

// -- Get Supplier Parts --

type GetSupplierPartsInput struct {
	Part     int `json:"part,omitempty" jsonschema:"Filter by InvenTree part ID"`
	Supplier int `json:"supplier,omitempty" jsonschema:"Filter by supplier company ID"`
	Limit    int `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterGetSupplierParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_supplier_parts",
		Description: "List supplier part links, filtered by part and/or supplier. At least one filter is required. " +
			"Use this before create_supplier_part to check whether a part is already linked to that supplier - " +
			"InvenTree happily accepts a second link with the same SKU.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetSupplierPartsInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 && input.Supplier == 0 {
			return errResult(fmt.Errorf("at least one of part or supplier is required")), nil, nil
		}
		results, err := listSupplierParts(c, input.Part, input.Supplier, input.Limit)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(map[string]any{"count": len(results), "results": results})
	})
}

func listSupplierParts(c *client.Client, partID, supplierID, limit int) ([]SupplierPart, error) {
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/api/company/part/?limit=%d&format=json", limit)
	if partID != 0 {
		path += fmt.Sprintf("&part=%d", partID)
	}
	if supplierID != 0 {
		path += fmt.Sprintf("&supplier=%d", supplierID)
	}
	var resp client.PaginatedResponse[SupplierPart]
	if err := c.Get(path, &resp); err != nil {
		// Filtering by a part that is not purchaseable fails outright.
		err = explainQuerysetError(err, "part", partID,
			"supplier parts only exist for parts marked purchaseable; call update_part with purchaseable=true first")
		return nil, fmt.Errorf("listing supplier parts: %w", err)
	}
	return resp.Results, nil
}

// availableWarning is repeated in both tool descriptions that write the
// field, because inventing a quantity where the supplier only gives a status
// flag is an easy and silently wrong thing for a caller to do.
const availableWarning = "IMPORTANT about 'available': it is a plain quantity, and writing it makes InvenTree stamp " +
	"availability_updated. Only write a number the supplier actually reports. Some distributor APIs expose no " +
	"quantity at all, only an in-stock/out-of-stock flag - in that case leave available unset and put the status " +
	"in note instead, rather than turning a flag into a made-up number. Check the field semantics before trusting " +
	"a value: a field named after something else may carry the quantity (FEGA & Schmitt's price/availability " +
	"service reports it in a field called warehouse_number, while warehouse_name stays constant)."

// -- Create Supplier Part --

type CreateSupplierPartInput struct {
	Part             int     `json:"part" jsonschema:"InvenTree part ID (required). The part must be marked purchaseable."`
	Supplier         int     `json:"supplier" jsonschema:"Supplier company ID (required). The company must have is_supplier set."`
	SKU              string  `json:"SKU" jsonschema:"The supplier's own article number for this part (required)"`
	ManufacturerPart int     `json:"manufacturer_part,omitempty" jsonschema:"Manufacturer part ID to link. The MPN shown on the supplier part is derived from this - MPN itself is read-only in the InvenTree API."`
	Link             string  `json:"link,omitempty" jsonschema:"URL of the supplier's product page"`
	Note             string  `json:"note,omitempty" jsonschema:"Free-text note, e.g. an availability status that is not a quantity. Limited to 100 characters by InvenTree; use 'notes' for anything longer."`
	Notes            string  `json:"notes,omitempty" jsonschema:"Long-form notes, not length-limited (unlike 'note')"`
	Packaging        string  `json:"packaging,omitempty" jsonschema:"Packaging unit, e.g. 'Ring 100m'"`
	Available        float64 `json:"available,omitempty" jsonschema:"Quantity available at the supplier. Leave unset unless you have a real number."`
}

func RegisterCreateSupplierPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_supplier_part",
		Description: "Link a part to a supplier under that supplier's SKU. part, supplier and SKU are required. The part must be " +
			"marked purchaseable and the company must have is_supplier set. " +
			"Check with get_supplier_parts first - duplicate links are accepted and have to be cleaned up by hand. " + availableWarning,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateSupplierPartInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 || input.Supplier == 0 || input.SKU == "" {
			return errResult(fmt.Errorf("part, supplier and SKU are all required")), nil, nil
		}
		payload := map[string]any{
			"part":     input.Part,
			"supplier": input.Supplier,
			"SKU":      input.SKU,
		}
		if input.ManufacturerPart != 0 {
			payload["manufacturer_part"] = input.ManufacturerPart
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.Note != "" {
			payload["note"] = input.Note
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}
		if input.Packaging != "" {
			payload["packaging"] = input.Packaging
		}
		if input.Available != 0 {
			payload["available"] = input.Available
		}

		var created SupplierPart
		if err := c.Post("/api/company/part/", payload, &created); err != nil {
			err = explainQuerysetError(err, "part", input.Part,
				"supplier parts can only be attached to parts marked purchaseable; call update_part with purchaseable=true first")
			err = explainQuerysetError(err, "supplier", input.Supplier,
				"only companies with is_supplier set can supply parts; call update_company with is_supplier=true first")
			return errResult(fmt.Errorf("creating supplier part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Update Supplier Part --

type UpdateSupplierPartInput struct {
	ID               int      `json:"id" jsonschema:"The supplier part ID (pk) to update"`
	SKU              string   `json:"SKU,omitempty" jsonschema:"New supplier article number"`
	ManufacturerPart int      `json:"manufacturer_part,omitempty" jsonschema:"Manufacturer part ID to link. The MPN shown on the supplier part is derived from this - MPN itself is read-only in the InvenTree API."`
	Link             string   `json:"link,omitempty" jsonschema:"New supplier product page URL"`
	Note             string   `json:"note,omitempty" jsonschema:"New free-text note. Limited to 100 characters by InvenTree; use 'notes' for anything longer."`
	Notes            string   `json:"notes,omitempty" jsonschema:"New long-form notes, not length-limited (unlike 'note')"`
	Packaging        string   `json:"packaging,omitempty" jsonschema:"New packaging unit"`
	Available        *float64 `json:"available,omitempty" jsonschema:"New available quantity. Writing this makes InvenTree stamp availability_updated."`
	Active           *bool    `json:"active,omitempty" jsonschema:"Whether this supplier link is active"`
}

func RegisterUpdateSupplierPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "update_supplier_part",
		Description: "Update an existing supplier part link. Only provided fields are changed. " +
			"Writing 'available' makes InvenTree stamp availability_updated server-side. " + availableWarning,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateSupplierPartInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{}
		if input.SKU != "" {
			payload["SKU"] = input.SKU
		}
		if input.ManufacturerPart != 0 {
			payload["manufacturer_part"] = input.ManufacturerPart
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.Note != "" {
			payload["note"] = input.Note
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}
		if input.Packaging != "" {
			payload["packaging"] = input.Packaging
		}
		if input.Available != nil {
			payload["available"] = *input.Available
		}
		if input.Active != nil {
			payload["active"] = *input.Active
		}
		if len(payload) == 0 {
			return errResult(fmt.Errorf("no fields to update")), nil, nil
		}

		var updated SupplierPart
		path := fmt.Sprintf("/api/company/part/%d/", input.ID)
		if err := c.Patch(path, payload, &updated); err != nil {
			return errResult(fmt.Errorf("updating supplier part %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Delete Supplier Part --

type DeleteSupplierPartInput struct {
	ID int `json:"id" jsonschema:"The supplier part ID (pk) to delete"`
}

func RegisterDeleteSupplierPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "delete_supplier_part",
		Description: "Remove a supplier link from a part. This deletes the link and its price breaks, not the part itself. " +
			"Mainly for cleaning up duplicate links.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteSupplierPartInput) (*mcp.CallToolResult, any, error) {
		if err := c.Delete(fmt.Sprintf("/api/company/part/%d/", input.ID)); err != nil {
			return errResult(fmt.Errorf("deleting supplier part %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Supplier part %d deleted successfully.", input.ID))
	})
}

// -- Get Supplier Price Breaks --

type GetSupplierPriceBreaksInput struct {
	SupplierPart int `json:"supplier_part" jsonschema:"Supplier part ID (pk) - from get_supplier_parts, NOT the part ID"`
}

func RegisterGetSupplierPriceBreaks(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_supplier_price_breaks",
		Description: "List the quantity/price tiers of one supplier part. Takes a supplier part pk (from get_supplier_parts), not a part pk - " +
			"InvenTree calls this field 'part' in its own API, which is a well-worn trap.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetSupplierPriceBreaksInput) (*mcp.CallToolResult, any, error) {
		if input.SupplierPart == 0 {
			return errResult(fmt.Errorf("supplier_part is required")), nil, nil
		}
		breaks, err := listPriceBreaks(c, input.SupplierPart)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(map[string]any{"count": len(breaks), "results": breaks})
	})
}

func listPriceBreaks(c *client.Client, supplierPartID int) ([]SupplierPriceBreak, error) {
	path := fmt.Sprintf("/api/company/price-break/?part=%d&limit=100&format=json", supplierPartID)
	var resp client.PaginatedResponse[SupplierPriceBreak]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("listing price breaks for supplier part %d: %w", supplierPartID, err)
	}
	return resp.Results, nil
}

// -- Set Supplier Price Break (upsert) --

type SetSupplierPriceBreakInput struct {
	SupplierPart  int     `json:"supplier_part" jsonschema:"Supplier part ID (pk) - from get_supplier_parts, NOT the part ID"`
	Quantity      float64 `json:"quantity" jsonschema:"Quantity threshold this price applies from (e.g. 1, 10, 100)"`
	Price         float64 `json:"price" jsonschema:"Unit price at this quantity"`
	PriceCurrency string  `json:"price_currency,omitempty" jsonschema:"ISO currency code, e.g. 'EUR'. Defaults to the instance's currency."`
}

func RegisterSetSupplierPriceBreak(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "set_supplier_price_break",
		Description: "Set the price of a supplier part at a given quantity, updating the existing tier at that exact quantity if there is one " +
			"and creating it otherwise. Safe to call repeatedly when refreshing prices from a supplier feed - it will not pile up duplicate tiers.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetSupplierPriceBreakInput) (*mcp.CallToolResult, any, error) {
		if input.SupplierPart == 0 {
			return errResult(fmt.Errorf("supplier_part is required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than 0")), nil, nil
		}

		existing, err := listPriceBreaks(c, input.SupplierPart)
		if err != nil {
			return errResult(err), nil, nil
		}
		var match *SupplierPriceBreak
		for i := range existing {
			if existing[i].Quantity == input.Quantity {
				match = &existing[i]
				break
			}
		}

		payload := map[string]any{"price": input.Price}
		if input.PriceCurrency != "" {
			payload["price_currency"] = input.PriceCurrency
		}

		var result SupplierPriceBreak
		if match != nil {
			path := fmt.Sprintf("/api/company/price-break/%d/", match.PK)
			if err := c.Patch(path, payload, &result); err != nil {
				return errResult(fmt.Errorf("updating price break %d: %w", match.PK, err)), nil, nil
			}
		} else {
			payload["part"] = input.SupplierPart
			payload["quantity"] = input.Quantity
			if err := c.Post("/api/company/price-break/", payload, &result); err != nil {
				return errResult(fmt.Errorf("creating price break: %w", err)), nil, nil
			}
		}
		return jsonResult(result)
	})
}

// -- Create Company --

type CreateCompanyInput struct {
	Name           string `json:"name" jsonschema:"Company name (required)"`
	Description    string `json:"description,omitempty" jsonschema:"Short description of the company"`
	Website        string `json:"website,omitempty" jsonschema:"Company website URL"`
	Currency       string `json:"currency,omitempty" jsonschema:"ISO currency code this company trades in, e.g. 'EUR'. InvenTree requires it; when omitted the instance's default currency is looked up and used."`
	Phone          string `json:"phone,omitempty" jsonschema:"Contact phone number"`
	Email          string `json:"email,omitempty" jsonschema:"Contact email address"`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Company supplies parts. Required before create_supplier_part will accept it."`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Company manufactures parts. Required before create_manufacturer_part will accept it."`
	IsCustomer     *bool  `json:"is_customer,omitempty" jsonschema:"Company buys parts from us"`
	Active         *bool  `json:"active,omitempty" jsonschema:"Whether the company is active (default true)"`
	Notes          string `json:"notes,omitempty" jsonschema:"Long-form notes"`
}

func RegisterCreateCompany(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_company",
		Description: "Create a supplier, manufacturer and/or customer. Search with search_companies first - InvenTree does not " +
			"reject a second company with the same name. The role flags are not cosmetic: create_supplier_part only accepts a " +
			"company with is_supplier, create_manufacturer_part only one with is_manufacturer, and a company can carry several roles at once.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateCompanyInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errResult(fmt.Errorf("name is required")), nil, nil
		}
		currency := input.Currency
		if currency == "" {
			resolved, err := defaultCurrency(c)
			if err != nil {
				return errResult(fmt.Errorf("currency is required by InvenTree and the instance default could not be read (%w) - "+
					"pass currency explicitly, e.g. 'EUR'", err)), nil, nil
			}
			currency = resolved
		}

		payload := map[string]any{
			"name":     input.Name,
			"currency": currency,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Website != "" {
			payload["website"] = input.Website
		}
		if input.Phone != "" {
			payload["phone"] = input.Phone
		}
		if input.Email != "" {
			payload["email"] = input.Email
		}
		if input.IsSupplier != nil {
			payload["is_supplier"] = *input.IsSupplier
		}
		if input.IsManufacturer != nil {
			payload["is_manufacturer"] = *input.IsManufacturer
		}
		if input.IsCustomer != nil {
			payload["is_customer"] = *input.IsCustomer
		}
		if input.Active != nil {
			payload["active"] = *input.Active
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created Company
		if err := c.Post("/api/company/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating company: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// defaultCurrency reads the instance-wide default currency, which InvenTree
// requires on every company but does not fill in itself on the API.
func defaultCurrency(c *client.Client) (string, error) {
	var setting struct {
		Value string `json:"value"`
	}
	if err := c.Get("/api/settings/global/INVENTREE_DEFAULT_CURRENCY/?format=json", &setting); err != nil {
		return "", err
	}
	if setting.Value == "" {
		return "", fmt.Errorf("instance reports an empty default currency")
	}
	return setting.Value, nil
}

// -- Update Company --

type UpdateCompanyInput struct {
	ID             int    `json:"id" jsonschema:"The company ID (pk) to update"`
	Name           string `json:"name,omitempty" jsonschema:"New company name"`
	Description    string `json:"description,omitempty" jsonschema:"New description"`
	Website        string `json:"website,omitempty" jsonschema:"New website URL"`
	Currency       string `json:"currency,omitempty" jsonschema:"New ISO currency code, e.g. 'EUR'"`
	Phone          string `json:"phone,omitempty" jsonschema:"New contact phone number"`
	Email          string `json:"email,omitempty" jsonschema:"New contact email address"`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Whether the company supplies parts"`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Whether the company manufactures parts"`
	IsCustomer     *bool  `json:"is_customer,omitempty" jsonschema:"Whether the company buys parts from us"`
	Active         *bool  `json:"active,omitempty" jsonschema:"Whether the company is active"`
	Notes          string `json:"notes,omitempty" jsonschema:"New long-form notes"`
}

func RegisterUpdateCompany(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "update_company",
		Description: "Update an existing company. Only provided fields are changed. The usual reason to call this is adding a role " +
			"flag to a company that already exists - e.g. setting is_manufacturer on a supplier that also makes its own products - " +
			"rather than creating a second company for the second role.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateCompanyInput) (*mcp.CallToolResult, any, error) {
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}
		payload := map[string]any{}
		if input.Name != "" {
			payload["name"] = input.Name
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Website != "" {
			payload["website"] = input.Website
		}
		if input.Currency != "" {
			payload["currency"] = input.Currency
		}
		if input.Phone != "" {
			payload["phone"] = input.Phone
		}
		if input.Email != "" {
			payload["email"] = input.Email
		}
		if input.IsSupplier != nil {
			payload["is_supplier"] = *input.IsSupplier
		}
		if input.IsManufacturer != nil {
			payload["is_manufacturer"] = *input.IsManufacturer
		}
		if input.IsCustomer != nil {
			payload["is_customer"] = *input.IsCustomer
		}
		if input.Active != nil {
			payload["active"] = *input.Active
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}
		if len(payload) == 0 {
			return errResult(fmt.Errorf("no fields to update")), nil, nil
		}

		var updated Company
		if err := c.Patch(fmt.Sprintf("/api/company/%d/", input.ID), payload, &updated); err != nil {
			return errResult(fmt.Errorf("updating company %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Get Manufacturer Parts --

type GetManufacturerPartsInput struct {
	Part         int    `json:"part,omitempty" jsonschema:"Filter by InvenTree part ID"`
	Manufacturer int    `json:"manufacturer,omitempty" jsonschema:"Filter by manufacturer company ID"`
	MPN          string `json:"MPN,omitempty" jsonschema:"Filter by exact manufacturer part number"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterGetManufacturerParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_manufacturer_parts",
		Description: "List manufacturer part links (part + manufacturer + MPN), filtered by part, manufacturer and/or MPN. " +
			"At least one filter is required. Use this to find the manufacturer part ID that create_supplier_part takes - " +
			"a supplier part's MPN is read-only and comes from the manufacturer part it links to.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetManufacturerPartsInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 && input.Manufacturer == 0 && input.MPN == "" {
			return errResult(fmt.Errorf("at least one of part, manufacturer or MPN is required")), nil, nil
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/company/part/manufacturer/?limit=%d&format=json", limit)
		if input.Part != 0 {
			path += fmt.Sprintf("&part=%d", input.Part)
		}
		if input.Manufacturer != 0 {
			path += fmt.Sprintf("&manufacturer=%d", input.Manufacturer)
		}
		if input.MPN != "" {
			path += "&MPN=" + url.QueryEscape(input.MPN)
		}

		var resp client.PaginatedResponse[ManufacturerPart]
		if err := c.Get(path, &resp); err != nil {
			// Filtering by a part that is not purchaseable fails outright.
			err = explainQuerysetError(err, "part", input.Part,
				"manufacturer parts only exist for parts marked purchaseable; call update_part with purchaseable=true first")
			return errResult(fmt.Errorf("listing manufacturer parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{"count": resp.Count, "results": resp.Results})
	})
}

// -- Create Manufacturer Part --

type CreateManufacturerPartInput struct {
	Part         int    `json:"part" jsonschema:"InvenTree part ID (required). The part must be marked purchaseable."`
	Manufacturer int    `json:"manufacturer" jsonschema:"Manufacturer company ID (required). The company must have is_manufacturer set."`
	MPN          string `json:"MPN,omitempty" jsonschema:"Manufacturer part number, max 100 characters"`
	Description  string `json:"description,omitempty" jsonschema:"Manufacturer's own description of the part, max 250 characters"`
	Link         string `json:"link,omitempty" jsonschema:"URL of the manufacturer's product page"`
	Notes        string `json:"notes,omitempty" jsonschema:"Long-form notes"`
}

func RegisterCreateManufacturerPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_manufacturer_part",
		Description: "Record who manufactures a part, under that manufacturer's own part number. part and manufacturer are required. " +
			"InvenTree allows one link per (part, manufacturer, MPN) combination, so check get_manufacturer_parts first. " +
			"Create the manufacturer with create_company (is_manufacturer=true) if it does not exist yet.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateManufacturerPartInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 || input.Manufacturer == 0 {
			return errResult(fmt.Errorf("part and manufacturer are both required")), nil, nil
		}
		payload := map[string]any{
			"part":         input.Part,
			"manufacturer": input.Manufacturer,
		}
		if input.MPN != "" {
			payload["MPN"] = input.MPN
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created ManufacturerPart
		if err := c.Post("/api/company/part/manufacturer/", payload, &created); err != nil {
			err = explainQuerysetError(err, "part", input.Part,
				"manufacturer parts can only be attached to parts marked purchaseable; call update_part with purchaseable=true first")
			err = explainQuerysetError(err, "manufacturer", input.Manufacturer,
				"only companies with is_manufacturer set can manufacture parts; call update_company with is_manufacturer=true first")
			return errResult(fmt.Errorf("creating manufacturer part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Update Manufacturer Part --

type UpdateManufacturerPartInput struct {
	ID           int    `json:"id" jsonschema:"The manufacturer part ID (pk) to update"`
	Manufacturer int    `json:"manufacturer,omitempty" jsonschema:"New manufacturer company ID. The company must have is_manufacturer set."`
	MPN          string `json:"MPN,omitempty" jsonschema:"New manufacturer part number, max 100 characters"`
	Description  string `json:"description,omitempty" jsonschema:"New manufacturer description, max 250 characters"`
	Link         string `json:"link,omitempty" jsonschema:"New manufacturer product page URL"`
	Notes        string `json:"notes,omitempty" jsonschema:"New long-form notes"`
}

func RegisterUpdateManufacturerPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "update_manufacturer_part",
		Description: "Update an existing manufacturer part link. Only provided fields are changed. Changing the MPN here also " +
			"changes the MPN reported by every supplier part linked to it, since that field is derived rather than stored twice.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateManufacturerPartInput) (*mcp.CallToolResult, any, error) {
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}
		payload := map[string]any{}
		if input.Manufacturer != 0 {
			payload["manufacturer"] = input.Manufacturer
		}
		if input.MPN != "" {
			payload["MPN"] = input.MPN
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}
		if len(payload) == 0 {
			return errResult(fmt.Errorf("no fields to update")), nil, nil
		}

		var updated ManufacturerPart
		path := fmt.Sprintf("/api/company/part/manufacturer/%d/", input.ID)
		if err := c.Patch(path, payload, &updated); err != nil {
			err = explainQuerysetError(err, "manufacturer", input.Manufacturer,
				"only companies with is_manufacturer set can manufacture parts; call update_company with is_manufacturer=true first")
			return errResult(fmt.Errorf("updating manufacturer part %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Delete Manufacturer Part --

type DeleteManufacturerPartInput struct {
	ID int `json:"id" jsonschema:"The manufacturer part ID (pk) to delete"`
}

func RegisterDeleteManufacturerPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "delete_manufacturer_part",
		Description: "Remove a manufacturer link from a part. This deletes the link, not the part or the company - but it also " +
			"deletes every supplier part linked to it, so check get_supplier_parts before removing one that suppliers point at.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteManufacturerPartInput) (*mcp.CallToolResult, any, error) {
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}
		if err := c.Delete(fmt.Sprintf("/api/company/part/manufacturer/%d/", input.ID)); err != nil {
			return errResult(fmt.Errorf("deleting manufacturer part %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Manufacturer part %d deleted successfully.", input.ID))
	})
}
