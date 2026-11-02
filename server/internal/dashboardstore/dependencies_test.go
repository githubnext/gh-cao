package dashboardstore_test

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func TestContractDependencies(t *testing.T) {
	command := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list production contract dependencies: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		switch {
		case strings.HasSuffix(dependency, "/postgresx"), strings.HasSuffix(dependency, "/sqlitex"),
			strings.HasPrefix(dependency, "github.com/jackc/"), strings.HasPrefix(dependency, "modernc.org/"):
			t.Errorf("canonical read contract depends on a storage implementation: %s", dependency)
		}
	}
}

func TestContractTypeOwnershipAndCompatibility(t *testing.T) {
	const owner = "github.com/githubnext/gh-cao/server/internal/dashboardstore"
	for _, contract := range []reflect.Type{
		reflect.TypeFor[dashboardstore.State](),
		reflect.TypeFor[dashboardstore.Page](),
		reflect.TypeFor[dashboardstore.ResourceLimits](),
		reflect.TypeFor[dashboardstore.ExecutionOptions](),
		reflect.TypeFor[dashboardstore.Reader](),
		reflect.TypeFor[dashboardstore.Store](),
	} {
		if contract.PkgPath() != owner {
			t.Errorf("%s is owned by %s, want %s", contract.Name(), contract.PkgPath(), owner)
		}
	}
	for _, pair := range [][2]reflect.Type{
		{reflect.TypeFor[dashboardstore.State](), reflect.TypeFor[postgresx.State]()},
		{reflect.TypeFor[dashboardstore.Page](), reflect.TypeFor[postgresx.SQLPage]()},
		{reflect.TypeFor[dashboardstore.ResourceLimits](), reflect.TypeFor[postgresx.SQLResourceLimits]()},
		{reflect.TypeFor[dashboardstore.ExecutionOptions](), reflect.TypeFor[postgresx.SQLExecutionOptions]()},
		{reflect.TypeFor[dashboardstore.Reader](), reflect.TypeFor[postgresx.NativeReader]()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("PostgreSQL compatibility name %v is not an alias of %v", pair[1], pair[0])
		}
	}
}
