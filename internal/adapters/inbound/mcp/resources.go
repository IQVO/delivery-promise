package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// registerResources adds the scoped read-model resource (Agentic Standard
// v2, AS-3; docs/adr/0013). Per the MCP charter (docs/adr/0007) resources
// stay READ-ONLY and are bounded-context contracts tied to a decision, not
// bulk dumps: the promise-books resource answers "how much pickup capacity
// does one node still have on one date?" for one node and one cutoff date,
// backed by the same ListPromiseBooks read model the tool and the REST GET
// /promise-books use. The URI values are read from the request URI at call
// time; the response is the bounded JSON view of at most that node's books
// of that one date (never a table dump).
func (d Deps) registerResources(server *mcp.Server) {
	const bookTemplate = "promise://books/{nodeId}/{date}"
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: bookTemplate,
		Name:        "promise books of a node on a date",
		Description: "The capacity ledger books of one fulfillment node whose pickup cutoff falls on one date (YYYY-MM-DD in the node's own time zone): per book its OPEN or CLOSED state and its capacity (share, provisional and confirmed holds). Bounded to one node and one date, like GET /promise-books.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		nodeID, date, err := parseBooksURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		list, err := d.Reads.ListPromiseBooks(ctx, nodeID, date)
		if err != nil {
			return nil, mapError(ctx, err)
		}
		return booksResourceResult(req.Params.URI, nodeID, date, list)
	})
}

// booksResourceResult renders one bounded book list as the resource's JSON
// text: the queried node and date plus the books, the same camelCase wire
// shape as the list_promise_books tool.
func booksResourceResult(uri, nodeID, date string, list views.BookList) (*mcp.ReadResourceResult, error) {
	body, err := json.Marshal(booksResource{NodeID: nodeID, Date: date, Items: nonEmptyBooks(list.Items)})
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: mimeTypeJSON,
			Text:     string(body),
		}},
	}, nil
}

// booksResource is the JSON body of the promise-books resource.
type booksResource struct {
	NodeID string       `json:"nodeId"`
	Date   string       `json:"date"`
	Items  []views.Book `json:"items"`
}

// parseBooksURI extracts nodeId and date from a
// promise://books/{nodeId}/{date} URI. The two segments are model-supplied
// and therefore untrusted; a malformed URI or an empty segment is rejected
// rather than silently defaulted.
func parseBooksURI(uri string) (nodeID, date string, err error) {
	const scheme = "promise://books/"
	if !strings.HasPrefix(uri, scheme) {
		return "", "", fmt.Errorf("resource uri %q must start with %q", uri, scheme)
	}
	rest := strings.TrimPrefix(uri, scheme)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("resource uri %q must be promise://books/{nodeId}/{date}", uri)
	}
	nodeID, date = parts[0], parts[1]
	if nodeID == "" || date == "" {
		return "", "", fmt.Errorf("resource uri %q has an empty nodeId or date segment", uri)
	}
	return nodeID, date, nil
}

func nonEmptyBooks(items []views.Book) []views.Book {
	if items == nil {
		return []views.Book{}
	}
	return items
}
