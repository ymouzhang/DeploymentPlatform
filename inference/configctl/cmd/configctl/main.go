package main

import (
	"flag"
	"fmt"
	"os"

	configpkg "DeploymentPlatform/inference/configctl/internal/config"
)

func main() {
	engine := flag.String("engine", "", "expected inference engine: vllm or sglang")
	output := flag.String("output", "env", "output format: env or image")
	flag.Parse()
	if *engine == "" || flag.NArg() != 1 {
		fatal("用法: dp-inference-config --engine <vllm|sglang> [--output env|image] config/config.json")
	}

	cfg, err := configpkg.Load(flag.Arg(0), *engine)
	if err != nil {
		fatal(err.Error())
	}
	switch *output {
	case "env":
		err = configpkg.WriteEnv(os.Stdout, cfg)
	case "image":
		err = configpkg.WriteImage(os.Stdout, cfg)
	default:
		fatal("output 必须为 env 或 image")
	}
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
