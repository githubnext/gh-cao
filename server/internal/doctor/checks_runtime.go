package doctor

import (
	"context"
	"fmt"
	"runtime"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const areaRuntime = "runtime"

func (d Doctor) checkBuild(context.Context) Check {
	return Check{
		ID: "runtime.build", Area: areaRuntime, Title: "Build and runtime",
		Status:  StatusPass,
		Summary: fmt.Sprintf("cao-dashboard %s on %s/%s", d.Version, runtime.GOOS, runtime.GOARCH),
		Details: []Detail{
			detail("version", d.Version),
			detail("go", runtime.Version()),
			detail("platform", runtime.GOOS+"/"+runtime.GOARCH),
			detail("canonicalSchemaVersion", fmt.Sprint(model.SchemaVersion)),
			detail("reportSchemaVersion", fmt.Sprint(ReportSchemaVersion)),
		},
	}
}
