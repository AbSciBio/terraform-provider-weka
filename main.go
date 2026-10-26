package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"gitlab.com/absci/devops/terraform-weka/internal/provider"
)

//go:generate go tool tfplugindocs generate --provider-name weka

// version is set by the release build.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/absci/weka",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
