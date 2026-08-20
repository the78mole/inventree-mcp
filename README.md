# InvenTree MCP Server

An [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) server that connects AI assistants to your [InvenTree](https://inventree.org/) inventory management system. Talk to your inventory in natural language — create parts, manage stock, organize locations, and more.

**Example prompts:**

- *"Add 10 ESP32-P4 boards to Green 1 in the office"*
- *"How many potentiometers do I have in stock?"*
- *"Create a new category for voltage regulators under Electronic Components"*
- *"Move 5 resistors from Blue 1 to Green 1"*

## Features

- **41 MCP tools** covering parts, parameters, suppliers, stock, locations, and categories
- **Fuzzy search** — say "green box" and it finds "Green 1"
- **Hierarchical navigation** — locations and categories with full path display
- **Stock management** — add, remove, transfer, and track inventory
- **Image search** — optionally find and attach product images via Google
- **Type coercion middleware** — handles client quirks gracefully

## Prerequisites

- **Go 1.23+** (for building from source)
- **InvenTree instance** (v1.x) accessible over HTTP/HTTPS
- **InvenTree API token** (see [Generating an API Token](#generating-an-api-token))

## Installation

### Build from source

```bash
git clone https://github.com/syntaxerr66/inventree-mcp.git
cd inventree-mcp
go build -o inventree-mcp ./cmd/inventree-mcp
```

The binary is self-contained — copy it wherever you like.

### Verify it works

```bash
INVENTREE_URL=http://your-inventree-host \
INVENTREE_TOKEN=your-token-here \
./inventree-mcp
```

The server communicates over stdin/stdout using the MCP protocol. If it starts without errors, you're good. Press `Ctrl+C` to stop.

## Generating an API Token

The MCP server authenticates with InvenTree using an API token. There are two ways to get one:

### Option 1: From the InvenTree web UI

1. Log in to your InvenTree instance
2. Go to your user settings (click your username → **Settings**)
3. Find the **API Tokens** section
4. Create or copy your token

### Option 2: Programmatically

Send a GET request with your username and password using basic authentication:

```bash
curl -u your-username:your-password http://your-inventree-host/api/user/token/
```

Response:

```json
{
    "token": "inv-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-xxxxxxxx"
}
```

### Token notes

- Tokens are **persistent** — they don't expire unless an administrator revokes them
- API access is **scoped to user permissions** — the token inherits the permissions of the user it belongs to
- Use a dedicated user account for the MCP server if you want to restrict what the AI can do

## Configuration

The server is configured via environment variables:

| Variable | Required | Description |
|---|---|---|
| `INVENTREE_URL` | Yes | Base URL of your InvenTree instance (e.g., `http://192.168.1.100`) |
| `INVENTREE_TOKEN` | Yes | API token for authentication |
| `GOOGLE_API_KEY` | No | Google Cloud API key for image search |
| `GOOGLE_CSE_ID` | No | Google Custom Search Engine ID for image search |

### Optional: Image search

The `search_part_images` tool lets the AI find product photos and attach them to parts. To enable it:

1. Create a [Google Custom Search Engine](https://cse.google.com/) configured for image search
2. Enable the Custom Search API in your [Google Cloud Console](https://console.cloud.google.com/)
3. Set `GOOGLE_API_KEY` and `GOOGLE_CSE_ID`

If not configured, the server starts normally — only the image search tool will return an informative error. All other tools work regardless.

## Setup with Claude Code (CLI)

Add a `.mcp.json` file to your project root (or `~/.claude/.mcp.json` for global config):

```json
{
  "mcpServers": {
    "inventree": {
      "command": "/path/to/inventree-mcp",
      "env": {
        "INVENTREE_URL": "http://your-inventree-host",
        "INVENTREE_TOKEN": "your-token-here"
      }
    }
  }
}
```

> **Security:** Add `.mcp.json` to your `.gitignore` — it contains your API token.

After adding the config, restart Claude Code. The tools will be available automatically. You can verify with:

```
/mcp
```

This lists all connected MCP servers and their tools.

## Setup with Claude Desktop

Edit your Claude Desktop configuration file:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

Add the server to the `mcpServers` section:

```json
{
  "mcpServers": {
    "inventree": {
      "command": "/path/to/inventree-mcp",
      "env": {
        "INVENTREE_URL": "http://your-inventree-host",
        "INVENTREE_TOKEN": "your-token-here"
      }
    }
  }
}
```

Restart Claude Desktop. You should see a hammer icon indicating MCP tools are available.

## Available Tools

### Parts

| Tool | Description |
|---|---|
| `search_parts` | Search parts by name, keyword, or description |
| `get_part` | Get detailed info about a specific part |
| `list_parts` | List parts with optional category filter |
| `create_part` | Create a new part |
| `update_part` | Update part fields (name, description, category, tags, etc.) |
| `delete_part` | Delete a part (auto-deactivates first) |
| `set_part_image` | Attach an image to a part from a URL |
| `upload_part_image` | Same thing under a name that says how it works |
| `search_part_images` | Find product images via Google (requires API keys) |

#### Part tags

`create_part` and `update_part` accept a `tags` list, which maps to InvenTree's
`tags` field (django-taggit). A typical use is marking preferred and deprecated
alternatives in a catalogue, e.g. `["recommended"]` vs `["discouraged"]`.

On `update_part`, `tags` **replaces** the whole list rather than appending to it —
pass an empty list to clear all tags, or omit the field to leave them untouched.

Two InvenTree API quirks are worth knowing:

- **Tags don't come back from a GET.** They are returned in the response to a
  `POST`/`PATCH`, but `GET /api/part/<pk>/` omits the field, so `get_part` and
  `list_parts` always report `tags: null`.
- **There is no tag filter.** `?tag=`, `?tags__name=`, `?tags__slug=` and
  `?has_tags=` are silently ignored and return *every* part, which is easy to
  mistake for "all parts carry this tag". Tags *are* covered by the full-text
  search, so `search_parts` with the tag name is the working way to find them.
  That search is a substring match, so pick tag names where neither is a
  substring of the other (`recommended`/`discouraged`, not
  `recommended`/`not-recommended`).

#### Part images

**InvenTree 1.x can only be given image *bytes*.** The `remote_image` field,
which used to hand InvenTree a URL for the server to fetch itself, is gone —
verified against 1.4.3 / API 511:

- `OPTIONS /api/part/` offers `image` (file upload) and `existing_image`, no
  `remote_image`;
- the field does not appear in that version's part serializer at all;
- `GET /api/settings/global/INVENTREE_DOWNLOAD_FROM_URL/` returns **404** — the
  setting that used to gate the feature no longer exists.

Django REST Framework ignores unknown keys silently, so writing `remote_image`
returned HTTP 200 with `image: null` and no error anywhere. That is the silent
no-op earlier versions of these tools tried to detect and report; there is
nothing to detect any more, because there is no code path that could work.

So `set_part_image`, `upload_part_image` and the `image_url` option of
`create_part`/`update_part` all do the same thing: download the image in **this**
process and PATCH the bytes to InvenTree as `multipart/form-data`. The machine
running this MCP server needs outbound access to the image URL; the InvenTree
host does not.

`create_part` and `update_part` upload the image in a second request after the
part itself is written. If the part is created but the image fails, the result
carries the part plus an `image_error` field rather than failing the call.

### Part Parameters

Parameter templates define a technical attribute (name + units) globally;
parameters are one template's value on one part. Note that these live under
`/api/parameter/…`, not `/api/part/parameter/…`.

| Tool | Description |
|---|---|
| `search_parameter_templates` | Find templates by name before creating a near-duplicate |
| `list_parameter_templates` | List all parameter templates |
| `create_parameter_template` | Create a template explicitly |
| `get_part_parameters` | List a part's parameter values, with template details expanded |
| `set_part_parameter` | Upsert a value — creates the template and/or the value as needed |
| `delete_parameter` | Remove a value from a part (the template survives) |

`set_part_parameter` is the one to reach for: given a `template_name` it looks
the template up, creates it if missing, then updates or creates the part's
value. Populating a part's attributes is one call per attribute with no
existence checks in between.

### Suppliers

| Tool | Description |
|---|---|
| `search_companies` | Find companies, optionally filtered by supplier/manufacturer/customer role |
| `get_supplier_parts` | List supplier links by part and/or supplier |
| `create_supplier_part` | Link a part to a supplier under that supplier's SKU |
| `update_supplier_part` | Update an existing supplier link |
| `delete_supplier_part` | Remove a supplier link |
| `get_supplier_price_breaks` | List a supplier part's quantity/price tiers |
| `set_supplier_price_break` | Upsert the price at one quantity |

Two traps worth knowing here:

- **`part` on a price break means the SupplierPart pk**, not the Part pk. That
  is InvenTree's own naming; these tools call it `supplier_part` to keep it
  straight.
- **`MPN` is read-only** on a supplier part (verified via `OPTIONS`) — it
  mirrors the linked `manufacturer_part`. Writing it is accepted and changes
  nothing, so set `manufacturer_part` instead.

### Stock

| Tool | Description |
|---|---|
| `get_stock` | List stock items with part/location filters |
| `get_stock_item` | Get a specific stock item by ID |
| `add_stock` | Create a new stock entry (part + quantity + location) |
| `stock_add_quantity` | Add quantity to existing stock items |
| `stock_remove_quantity` | Remove quantity from existing stock items |
| `stock_transfer` | Move stock between locations |
| `get_stock_history` | Read a stock item's tracking history (movements, counts, notes) |
| `delete_stock_item` | Delete a stock entry |

### Stock Locations

| Tool | Description |
|---|---|
| `search_stock_locations` | Fuzzy search locations by name or description |
| `get_stock_location` | Get location details with full path |
| `list_stock_locations` | List all locations with hierarchy |
| `create_stock_location` | Create a new location (supports nesting) |
| `update_stock_location` | Update location fields |
| `delete_stock_location` | Delete an empty location |

### Part Categories

| Tool | Description |
|---|---|
| `search_part_categories` | Search categories by name |
| `list_part_categories` | List the full category hierarchy |
| `create_part_category` | Create a new category (supports nesting) |
| `update_part_category` | Update category fields |
| `delete_part_category` | Delete an empty category |

## How It Works

The server runs as a stdio process — the MCP client (Claude Code, Claude Desktop) launches it and communicates via JSON-RPC over stdin/stdout.

```
User → Claude → MCP Client → inventree-mcp (stdio) → InvenTree REST API
```

When you say something like *"Add 10 ESP32-P4 boards to Green 1"*, the AI:

1. Calls `search_parts("ESP32-P4")` to check if the part exists
2. Calls `search_stock_locations("Green 1")` to find the location
3. Creates the part with `create_part` if needed (after finding the right category)
4. Calls `add_stock(part=X, quantity=10, location=Y)` to add inventory

The AI handles disambiguation — if "green" matches multiple locations, it presents options. If a part doesn't exist, it asks whether to create it.

## Development

```bash
# Build all packages
go build ./...

# Build the binary
go build -o inventree-mcp ./cmd/inventree-mcp

# Run directly
INVENTREE_URL=http://... INVENTREE_TOKEN=... go run ./cmd/inventree-mcp

# Run integration tests (requires a live InvenTree instance)
INVENTREE_URL=http://... INVENTREE_TOKEN=... go test -v ./internal/tools/

# Run all tests
go test ./...
```

### Project structure

```
cmd/inventree-mcp/main.go     Entry point — config, server, tool registration
internal/
  client/client.go             HTTP client for InvenTree REST API
  config/config.go             Environment variable configuration
  coerce/coerce.go             Type coercion middleware for MCP compatibility
  imagesearch/google.go        Google Custom Search image client
  tools/
    parts.go                   Part CRUD + image tools
    stock.go                   Stock item management tools
    locations.go               Stock location tools
    categories.go              Part category tools
    register.go                Tool registration orchestrator
    tools_integration_test.go  Integration tests
```

## License

MIT
