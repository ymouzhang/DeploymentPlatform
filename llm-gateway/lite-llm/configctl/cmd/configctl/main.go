package main

import (
	"flag"
	"fmt"
	"os"

	configpkg "DeploymentPlatform/llm-gateway/lite-llm/configctl/internal/config"
)

func main() {
	output := flag.String("output", "env", "output format: env, litellm, or images")
	requireSecrets := flag.Bool("require-secrets", false, "reject placeholder secrets")
	flag.Parse()
	if flag.NArg() != 1 {
		fatal("用法: dp-litellm-config [--output env|litellm|images] [--require-secrets] config/config.json")
	}

	cfg, err := configpkg.Load(flag.Arg(0), *requireSecrets)
	if err != nil {
		fatal(err.Error())
	}

	switch *output {
	case "env":
		err = configpkg.WriteEnv(os.Stdout, cfg)
	case "litellm":
		err = configpkg.WriteLiteLLM(os.Stdout, cfg)
	case "images":
		err = configpkg.WriteImages(os.Stdout, cfg)
	default:
		fatal("output 必须为 env、litellm 或 images")
	}
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
