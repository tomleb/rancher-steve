package main

import (
	"os"

	controllergen "github.com/rancher/wrangler/v3/pkg/controller-gen"
	"github.com/rancher/wrangler/v3/pkg/controller-gen/args"
)

func main() {
	os.Unsetenv("GOPATH")

	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/steve/cmd/example/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"ext.cattle.io": {
				PackageName: "ext.cattle.io",
				Types: []interface{}{
					// All structs with an embedded ObjectMeta field will be picked up
					"./cmd/example/apis/ext.cattle.io/v1",
				},
				GenerateTypes:   true,
				GenerateOpenAPI: true,
				OpenAPIDependencies: []string{
					"k8s.io/apimachinery/pkg/apis/meta/v1",
					"k8s.io/apimachinery/pkg/runtime",
					"k8s.io/apimachinery/pkg/version",
				},
			},
		},
	})
}
