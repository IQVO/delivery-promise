package architecture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

const (
	a2aAdapterDir = "../adapters/inbound/a2a"
	a2aClientPkg  = "github.com/a2aproject/a2a-go/v2/a2aclient"
)

// TestA2AAdapterDependencyRule encodes docs/adr/0010: the A2A inbound adapter
// is additive, never load-bearing for the OLTP composition root. It may
// depend only on the application and domain layers (never on outbound
// adapters, never on cmd) and nothing else in this codebase may depend on it:
// cmd/a2a wires it up and nothing else touches it.
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})
		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})
		assertArchGoPasses(t, result)
	})
}

// importsOf returns the import paths of one Go source file.
func importsOf(t *testing.T, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := make([]string, 0, len(f.Imports))
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: bad import %s", path, spec.Path.Value)
		}
		out = append(out, p)
	}
	return out
}

// a2aClientImporters returns the non-test Go files under roots importing the
// A2A client SDK package.
func a2aClientImporters(t *testing.T, roots ...string) []string {
	t.Helper()
	var hits []string
	for _, root := range roots {
		for _, path := range goFilesUnder(t, root, false) {
			for _, imp := range importsOf(t, path) {
				if imp == a2aClientPkg {
					hits = append(hits, filepath.ToSlash(path))
				}
			}
		}
	}
	return hits
}

// TestNoOutboundA2AClient: delivery-promise has no use for delegating its work
// to a sibling agent (it calls no sibling at all, docs/adr/0003), so no
// production code may import the A2A client SDK. The server adapter is inbound
// only.
func TestNoOutboundA2AClient(t *testing.T) {
	if hits := a2aClientImporters(t, "..", "../../cmd"); len(hits) != 0 {
		t.Fatalf("non-test code imports the A2A client (%s): this context only serves A2A, it never calls an agent", strings.Join(hits, ", "))
	}
}

// TestA2AAdapterIsReadOnly is the write gate of the A2A rollout, pinned in
// code: the MCP charter (docs/adr/0007) is read-only, so the A2A adapter may
// reach the application layer through its Reads interface only. It must import
// neither the usecases package (where every write use case lives) nor any
// outbound adapter, the event machinery or the outbox.
func TestA2AAdapterIsReadOnly(t *testing.T) {
	files := goFilesUnder(t, a2aAdapterDir, false)
	if len(files) == 0 {
		t.Fatal("internal/adapters/inbound/a2a has no Go sources; the sensor is looking in the wrong place")
	}
	banned := []string{
		modulePath + "/internal/application/usecases",
		modulePath + "/internal/adapters/outbound",
		modulePath + "/internal/adapters/kafka",
		modulePath + "/internal/adapters/inbound/kafka",
		modulePath + "/internal/adapters/inbound/http",
	}
	for _, path := range files {
		for _, imp := range importsOf(t, path) {
			for _, ban := range banned {
				if imp == ban || strings.HasPrefix(imp, ban+"/") {
					t.Errorf("%s imports %s: the A2A adapter is read-only and must reach the application layer through its Reads interface only", filepath.ToSlash(path), imp)
				}
			}
		}
	}
}
