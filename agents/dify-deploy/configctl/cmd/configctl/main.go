package main

import (
	"flag"
	"fmt"
	"os"

	configpkg "DeploymentPlatform/agents/dify-deploy/configctl/internal/config"
)

func main() {
	output := flag.String("output", "env", "output format: env or images")
	requireSecrets := flag.Bool("require-secrets", false, "reject placeholder secrets")
	flag.Parse()
	if flag.NArg() != 1 {
		fatal("用法: dp-dify-config [--output env|images] [--require-secrets] config/config.json")
	}

	cfg, err := configpkg.Load(flag.Arg(0), *requireSecrets)
	if err != nil {
		fatal(err.Error())
	}
	switch *output {
	case "env":
		err = configpkg.WriteEnv(os.Stdout, cfg)
	case "images":
		err = configpkg.WriteImages(os.Stdout, cfg)
	default:
		fatal("output 必须为 env 或 images")
	}
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
