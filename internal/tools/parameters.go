package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Part parameters live under /api/parameter/ and /api/parameter/template/ —
// NOT under /api/part/parameter/..., which 404s despite the naming used by
// the rest of the part API.

// ParameterTemplate is the definition of a parameter (its name and units),
// shared by every part that carries a value for it.
type ParameterTemplate struct {
	PK          int    `json:"pk"`
	Name        string `json:"name"`
	Units       string `json:"units"`
	Description string `json:"description"`
	ModelType   string `json:"model_type"`
	Checkbox    bool   `json:"checkbox"`
	Choices     string `json:"choices"`
	Enabled     bool   `json:"enabled"`
}

// Parameter is one template's value on one part.
type Parameter struct {
	PK             int                `json:"pk"`
	Template       int                `json:"template"`
	ModelType      string             `json:"model_type"`
	ModelID        int                `json:"model_id"`
	Data           string             `json:"data"`
	DataNumeric    *float64           `json:"data_numeric"`
	Note           string             `json:"note"`
	Updated        string             `json:"updated"`
	TemplateDetail *ParameterTemplate `json:"template_detail,omitempty"`
}

// partModelType is the model_type every tool here works with. InvenTree
// parameters are generic over model types, but parts are the only one this
// server exposes.
const partModelType = "part.part"

// -- Search Parameter Templates --

type SearchParameterTemplatesInput struct {
	Search string `json:"search" jsonschema:"Search query to find parameter templates by name"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default 25)"`
}

func RegisterSearchParameterTemplates(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "search_parameter_templates",
		Description: "Search parameter templates by name. Use this before create_parameter_template to avoid creating a near-duplicate template " +
			"(e.g. 'Nennspannung' vs 'Nennspannung (V)'), the same way search_part_categories is used before creating a category.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchParameterTemplatesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/parameter/template/?search=%s&limit=%d&format=json",
			url.QueryEscape(input.Search), limit)
		var resp client.PaginatedResponse[ParameterTemplate]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching parameter templates: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{"count": resp.Count, "results": resp.Results})
	})
}

// -- List Parameter Templates --

type ListParameterTemplatesInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum number of results (default 100)"`
	Offset int `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterListParameterTemplates(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_parameter_templates",
		Description: "List all parameter templates defined on this InvenTree instance.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListParameterTemplatesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		path := fmt.Sprintf("/api/parameter/template/?limit=%d&offset=%d&format=json", limit, input.Offset)
		var resp client.PaginatedResponse[ParameterTemplate]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing parameter templates: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{"count": resp.Count, "results": resp.Results})
	})
}

// -- Create Parameter Template --

type CreateParameterTemplateInput struct {
	Name        string `json:"name" jsonschema:"Template name, e.g. 'Leiternennquerschnitt' (required)"`
	Units       string `json:"units,omitempty" jsonschema:"Units, e.g. 'mm^2' or 'V'. InvenTree validates values against these once set."`
	Description string `json:"description,omitempty" jsonschema:"Description of the template"`
	Checkbox    *bool  `json:"checkbox,omitempty" jsonschema:"Whether values are booleans rather than free text"`
	Choices     string `json:"choices,omitempty" jsonschema:"Comma-separated list of allowed values"`
}

func RegisterCreateParameterTemplate(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_parameter_template",
		Description: "Create a parameter template (the definition of a technical attribute, shared across parts). " +
			"Search first with search_parameter_templates - templates are global, and duplicates with slightly different names are hard to clean up later. " +
			"In most cases you do not need this tool directly: set_part_parameter creates a missing template on the fly.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateParameterTemplateInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errResult(fmt.Errorf("name is required")), nil, nil
		}
		created, err := createParameterTemplate(c, input)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(created)
	})
}

func createParameterTemplate(c *client.Client, input CreateParameterTemplateInput) (*ParameterTemplate, error) {
	payload := map[string]any{
		"name":       input.Name,
		"model_type": partModelType,
	}
	if input.Units != "" {
		payload["units"] = input.Units
	}
	if input.Description != "" {
		payload["description"] = input.Description
	}
	if input.Checkbox != nil {
		payload["checkbox"] = *input.Checkbox
	}
	if input.Choices != "" {
		payload["choices"] = input.Choices
	}
	var created ParameterTemplate
	if err := c.Post("/api/parameter/template/", payload, &created); err != nil {
		return nil, fmt.Errorf("creating parameter template %q: %w", input.Name, err)
	}
	return &created, nil
}

// -- Get Part Parameters --

type GetPartParametersInput struct {
	Part int `json:"part" jsonschema:"Part ID (pk) whose parameter values should be listed"`
}

func RegisterGetPartParameters(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_part_parameters",
		Description: "List all parameter values for one part. Each entry includes template_detail with the template's name and units, " +
			"so a single call is enough to render a part's technical attributes.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetPartParametersInput) (*mcp.CallToolResult, any, error) {
		params, err := listPartParameters(c, input.Part)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(map[string]any{"count": len(params), "results": params})
	})
}

func listPartParameters(c *client.Client, partID int) ([]Parameter, error) {
	path := fmt.Sprintf("/api/parameter/?model_type=%s&model_id=%d&limit=250&format=json", partModelType, partID)
	var resp client.PaginatedResponse[Parameter]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("getting parameters for part %d: %w", partID, err)
	}
	return resp.Results, nil
}

// -- Set Part Parameter (upsert) --

type SetPartParameterInput struct {
	Part         int    `json:"part" jsonschema:"Part ID (pk) to set the parameter on"`
	Template     int    `json:"template,omitempty" jsonschema:"Existing template ID. Omit if using template_name."`
	TemplateName string `json:"template_name,omitempty" jsonschema:"Template name - looked up by exact name, and created if it does not exist yet. Ignored when template is given."`
	Units        string `json:"units,omitempty" jsonschema:"Units for a newly-created template (ignored when the template already exists)"`
	Data         string `json:"data" jsonschema:"Parameter value, as text. InvenTree parses it into data_numeric where the units allow."`
	Note         string `json:"note,omitempty" jsonschema:"Free-text note stored alongside the value"`
}

func RegisterSetPartParameter(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "set_part_parameter",
		Description: "Set a technical parameter on a part, creating whatever is missing along the way: the template is looked up by name (or created), " +
			"and the value is updated if the part already has one for that template, or created if not. " +
			"This is the tool to use for bulk-populating a part's attributes - one call per attribute, no existence checks needed. " +
			"Pass template_name (plus units for a new template) or an existing template ID.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetPartParameterInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}
		if input.Template == 0 && input.TemplateName == "" {
			return errResult(fmt.Errorf("either template or template_name is required")), nil, nil
		}

		templateID := input.Template
		if templateID == 0 {
			tpl, err := findOrCreateTemplate(c, input.TemplateName, input.Units)
			if err != nil {
				return errResult(err), nil, nil
			}
			templateID = tpl.PK
		}

		existing, err := findPartParameter(c, input.Part, templateID)
		if err != nil {
			return errResult(err), nil, nil
		}

		payload := map[string]any{"data": input.Data}
		if input.Note != "" {
			payload["note"] = input.Note
		}

		var result Parameter
		if existing != nil {
			path := fmt.Sprintf("/api/parameter/%d/", existing.PK)
			if err := c.Patch(path, payload, &result); err != nil {
				return errResult(fmt.Errorf("updating parameter %d: %w", existing.PK, err)), nil, nil
			}
		} else {
			payload["template"] = templateID
			payload["model_type"] = partModelType
			payload["model_id"] = input.Part
			if err := c.Post("/api/parameter/", payload, &result); err != nil {
				return errResult(fmt.Errorf("creating parameter for part %d: %w", input.Part, err)), nil, nil
			}
		}
		return jsonResult(result)
	})
}

// findOrCreateTemplate resolves a template by exact name, creating it when
// absent. The ?name= filter is an exact match, unlike ?search=.
func findOrCreateTemplate(c *client.Client, name, units string) (*ParameterTemplate, error) {
	path := fmt.Sprintf("/api/parameter/template/?name=%s&limit=2&format=json", url.QueryEscape(name))
	var resp client.PaginatedResponse[ParameterTemplate]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("looking up parameter template %q: %w", name, err)
	}
	for i := range resp.Results {
		if resp.Results[i].Name == name {
			return &resp.Results[i], nil
		}
	}
	return createParameterTemplate(c, CreateParameterTemplateInput{Name: name, Units: units})
}

// findPartParameter returns the part's existing value for a template, or nil.
func findPartParameter(c *client.Client, partID, templateID int) (*Parameter, error) {
	path := fmt.Sprintf("/api/parameter/?model_type=%s&model_id=%d&template=%d&limit=2&format=json",
		partModelType, partID, templateID)
	var resp client.PaginatedResponse[Parameter]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("looking up existing parameter for part %d: %w", partID, err)
	}
	for i := range resp.Results {
		if resp.Results[i].Template == templateID && resp.Results[i].ModelID == partID {
			return &resp.Results[i], nil
		}
	}
	return nil, nil
}

// -- Delete Parameter --

type DeleteParameterInput struct {
	ID int `json:"id" jsonschema:"The parameter value ID (pk) to delete - from get_part_parameters, not the template ID"`
}

func RegisterDeleteParameter(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "delete_parameter",
		Description: "Delete a parameter value from a part. Takes the parameter value's own pk (as returned by get_part_parameters), " +
			"not the template ID - the template itself is left alone and stays available to other parts.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteParameterInput) (*mcp.CallToolResult, any, error) {
		if err := c.Delete(fmt.Sprintf("/api/parameter/%d/", input.ID)); err != nil {
			return errResult(fmt.Errorf("deleting parameter %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Parameter %d deleted successfully.", input.ID))
	})
}
